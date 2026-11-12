package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/orders"
)

// Digital products: files attached to a SKU and delivered after payment
// through signed links with a download limit and an expiry. Cancelling or
// refunding the order takes the access away.

const maxDigitalBytes = 100 << 20 // 100 MiB per file

func (s *Server) routesDigital(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/digital", s.authed("settings", auth.Read, s.listDigital))
	handle("GET /v1/digital/{sku}", s.authed("settings", auth.Read, s.listDigital))
	handle("POST /v1/digital/{sku}", s.authed("catalog", auth.Edit, s.uploadDigital))
	handle("PATCH /v1/digital/{sku}/{id}", s.authed("catalog", auth.Edit, s.patchDigital))
	handle("DELETE /v1/digital/{sku}/{id}", s.authed("catalog", auth.Edit, s.deleteDigital))
	handle("GET /v1/orders/{id}/downloads", s.authed("orders", auth.Read, s.orderDownloads))
	handle("POST /v1/orders/{id}/downloads/{grant}/reset", s.authed("orders", auth.Edit, s.resetDownload))
	handle("GET /s/{tenant}/api/download/{token}", s.shopDownload)
}

func (s *Server) listDigital(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Orders.DigitalFiles(r.Context(), principal(r).Tenant, r.PathValue("sku"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if fs == nil {
		fs = []orders.DigitalFile{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": fs})
}

// cleanFilename keeps a name safe for a Content-Disposition header and for
// every operating system.
func cleanFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(c rune) rune {
		switch {
		case c < 32 || c == 127 || strings.ContainsRune(`"/\:*?<>|;`, c):
			return -1
		case unicode.IsSpace(c):
			return ' '
		}
		return c
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return name
}

func digitalLimits(maxS, daysS string) (int, int, []apierr.FieldError) {
	var errs []apierr.FieldError
	maxD, days := 5, 30
	if maxS != "" {
		n, err := strconv.Atoi(maxS)
		if err != nil || n < 1 || n > 100 {
			errs = append(errs, apierr.FieldError{Field: "maxDownloads", Code: "invalid", Message: "De 1 a 100 descargas"})
		}
		maxD = n
	}
	if daysS != "" {
		n, err := strconv.Atoi(daysS)
		if err != nil || n < 1 || n > 3650 {
			errs = append(errs, apierr.FieldError{Field: "linkDays", Code: "invalid", Message: "El enlace dura de 1 a 3,650 días"})
		}
		days = n
	}
	return maxD, days, errs
}

func (s *Server) uploadDigital(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "media_unavailable", Detail: "El almacenamiento de archivos no está configurado"})
		return
	}
	pr := principal(r)
	sku := r.PathValue("sku")
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	if _, _, err := t.Catalog.VariantBySKU(sku); err != nil {
		apierr.NotFound(w, "No existe un producto con ese SKU")
		return
	}
	tooLarge := func() {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large", Detail: fmt.Sprintf("El archivo pasa de %d MB.", maxDigitalBytes>>20)})
	}
	// Files are held in memory while stored: only two uploads at a time per node.
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	case <-r.Context().Done():
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDigitalBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			tooLarge()
			return
		}
		apierr.BadRequest(w, "Envía el archivo como multipart/form-data en el campo file")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		apierr.BadRequest(w, "", fe("file", "required", "Falta el archivo"))
		return
	}
	defer f.Close()
	if hdr.Size > maxDigitalBytes {
		tooLarge()
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDigitalBytes+1))
	if err != nil {
		apierr.BadRequest(w, "No se pudo leer el archivo")
		return
	}
	if len(data) == 0 {
		apierr.BadRequest(w, "", fe("file", "empty", "El archivo está vacío"))
		return
	}
	name := cleanFilename(firstNonEmpty(r.FormValue("filename"), hdr.Filename))
	maxD, days, errs := digitalLimits(r.FormValue("maxDownloads"), r.FormValue("linkDays"))
	if name == "" {
		errs = append(errs, apierr.FieldError{Field: "filename", Code: "required", Message: "Ponle nombre al archivo"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	ctype := http.DetectContentType(data)
	if ext := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); ext != "" && strings.HasPrefix(ctype, "application/octet-stream") {
		ctype = ext
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := "dfl_" + hex.EncodeToString(b)
	key := "dl_" + hex.EncodeToString(b) + "/file.bin"
	if err := s.Media.Put(r.Context(), pr.Tenant, key, data, "application/octet-stream"); err != nil {
		internalError(w, s.Log, err)
		return
	}
	sum := sha256.Sum256(data)
	df := orders.DigitalFile{ID: id, SKU: sku, Filename: name, ContentType: ctype, Size: int64(len(data)), MaxDownloads: maxD, LinkDays: days, StorageKey: key}
	if err := s.Orders.AddDigitalFile(r.Context(), pr.Tenant, pr.Actor(), df, sum[:]); err != nil {
		_ = s.Media.DeletePrefix(r.Context(), pr.Tenant, "dl_"+hex.EncodeToString(b))
		var fe orders.FieldError
		if errors.As(err, &fe) {
			apierr.BadRequest(w, fe.Message, apierr.FieldError{Field: fe.Field, Code: "invalid", Message: fe.Message})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, df)
}

func (s *Server) patchDigital(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in struct {
		Filename     string `json:"filename"`
		MaxDownloads int    `json:"maxDownloads"`
		LinkDays     int    `json:"linkDays"`
	}
	if !decode(w, r, &in) {
		return
	}
	maxD, days, errs := digitalLimits(strconv.Itoa(in.MaxDownloads), strconv.Itoa(in.LinkDays))
	name := cleanFilename(in.Filename)
	if name == "" {
		errs = append(errs, apierr.FieldError{Field: "filename", Code: "required", Message: "Ponle nombre al archivo"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	f, err := s.Orders.UpdateDigitalFile(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("sku"), r.PathValue("id"), name, maxD, days)
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Archivo no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, f)
}

func (s *Server) deleteDigital(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	key, err := s.Orders.DeleteDigitalFile(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("sku"), r.PathValue("id"))
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Archivo no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if s.Media != nil {
		if prefix, _, ok := strings.Cut(key, "/"); ok {
			_ = s.Media.DeletePrefix(r.Context(), pr.Tenant, prefix)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) orderDownloads(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if _, err := s.Orders.Get(r.Context(), pr.Tenant, r.PathValue("id"), false); errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Pedido no encontrado")
		return
	}
	ds, err := s.Orders.Downloads(r.Context(), pr.Tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if ds == nil {
		ds = []orders.Download{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ds})
}

func (s *Server) resetDownload(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	err := s.Orders.ResetDownload(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), r.PathValue("grant"))
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Descarga no encontrada o ya sin acceso")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.orderDownloads(w, r)
}

// shopDownload serves a file through its signed link. A request that starts
// the file counts as one download; a resumed one (Range past the start) does
// not, so a dropped connection does not spend a download.
func (s *Server) shopDownload(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) {
		return
	}
	tenant := r.PathValue("tenant")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	rng := r.Header.Get("Range")
	count := rng == "" || strings.HasPrefix(rng, "bytes=0-")
	f, err := s.Orders.OpenDownload(r.Context(), tenant, r.PathValue("token"), count && r.Method != http.MethodHead)
	if err != nil {
		status := http.StatusGone
		switch {
		case errors.Is(err, orders.ErrDownloadInvalid):
			status = http.StatusNotFound
		case errors.Is(err, orders.ErrDownloadLimit):
			status = http.StatusForbidden
		case !errors.Is(err, orders.ErrDownloadRevoked) && !errors.Is(err, orders.ErrDownloadExpired):
			internalError(w, s.Log, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "No se puede descargar: "+err.Error()+". Escribe a la tienda para pedir un enlace nuevo.\n")
		return
	}
	if s.Media == nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "media_unavailable", Detail: "Archivos no disponibles"})
		return
	}
	obj, err := s.Media.Open(r.Context(), tenant, f.StorageKey)
	if errors.Is(err, media.ErrNotFound) {
		w.WriteHeader(http.StatusGone)
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	defer obj.Close()
	// Always a download, never rendered inline on the store's origin.
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", contentDisposition(f.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", obj.ModTime, obj)
}

// contentDisposition names the file for every browser, with the accents of
// Spanish, French and Portuguese turned into plain letters.
func contentDisposition(name string) string {
	ascii := strings.Map(func(c rune) rune {
		if c > 126 {
			switch c {
			case 'á', 'à', 'ä', 'â':
				return 'a'
			case 'é', 'è', 'ë', 'ê':
				return 'e'
			case 'í', 'ì', 'ï', 'î':
				return 'i'
			case 'ó', 'ò', 'ö', 'ô':
				return 'o'
			case 'ú', 'ù', 'ü', 'û':
				return 'u'
			case 'ñ':
				return 'n'
			case 'Á':
				return 'A'
			case 'É':
				return 'E'
			case 'Í':
				return 'I'
			case 'Ó':
				return 'O'
			case 'Ú':
				return 'U'
			case 'Ñ':
				return 'N'
			}
			return '_'
		}
		return c
	}, name)
	// Only the ASCII name: some browsers ignore or mishandle filename* and
	// fall back to a generic name, which is worse than a missing accent.
	return mime.FormatMediaType("attachment", map[string]string{"filename": strings.ReplaceAll(ascii, `"`, "")})
}
