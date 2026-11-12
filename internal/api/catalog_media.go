package api

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/store"
)

// mediaView is a media item with ready-to-use URLs.
type mediaView struct {
	catalog.Media
	URL        string `json:"url"`                // largest fallback rendition
	Thumb      string `json:"thumb"`              // about 320 px wide
	Srcset     string `json:"srcset"`             // fallback format (JPEG or PNG)
	WebpSrcset string `json:"webpSrcset"`         // empty when WebP would not be smaller
	Original   string `json:"original,omitempty"` // the untouched upload, for the editor (panel only)
}

func mediaURL(tenant, key string) string { return "/media/" + tenant + "/" + key }

// viewMedia builds the URLs of a media item. The WebP srcset is offered only
// when every width has a WebP rendition, so the browser never upscales.
func viewMedia(tenant string, m catalog.Media) mediaView {
	v := mediaView{Media: m}
	var fb, wp []catalog.MediaVariant
	for _, r := range m.Variants {
		if r.Format == "webp" {
			wp = append(wp, r)
		} else {
			fb = append(fb, r)
		}
	}
	sort.Slice(fb, func(i, j int) bool { return fb[i].Width < fb[j].Width })
	sort.Slice(wp, func(i, j int) bool { return wp[i].Width < wp[j].Width })
	set := func(rs []catalog.MediaVariant) string {
		parts := make([]string, len(rs))
		for i, r := range rs {
			parts[i] = mediaURL(tenant, r.Key) + " " + strconv.Itoa(r.Width) + "w"
		}
		return strings.Join(parts, ", ")
	}
	v.Srcset = set(fb)
	if len(wp) == len(fb) {
		v.WebpSrcset = set(wp)
	}
	for _, r := range fb {
		v.URL = mediaURL(tenant, r.Key)
		if v.Thumb == "" && r.Width >= 320 {
			v.Thumb = v.URL
		}
	}
	if v.Thumb == "" {
		v.Thumb = v.URL
	}
	if v.Variants == nil {
		v.Variants = []catalog.MediaVariant{}
	}
	v.Original = "/v1/catalog/media/" + m.ID + "/original"
	return v
}

func (s *Server) listMedia(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 48
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			apierr.BadRequest(w, "", fe("limit", "out_of_range", "limit debe estar entre 1 y 200"))
			return
		}
		limit = n
	}
	after, err := httpx.DecodeCursor(q.Get("cursor"))
	if err != nil {
		apierr.BadRequest(w, "", fe("cursor", "invalid", "cursor inválido"))
		return
	}
	search := strings.TrimSpace(q.Get("q"))
	if len(search) > 100 {
		search = search[:100]
	}
	tenant := principal(r).Tenant
	items, next, total, err := s.DB.ListMedia(r.Context(), tenant, search, after, limit)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]mediaView, len(items))
	for i, m := range items {
		out[i] = viewMedia(tenant, m)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "nextCursor": httpx.EncodeCursor(next), "total": total})
}

func (s *Server) getMedia(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	ms, err := s.DB.Media(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if len(ms) == 0 {
		apierr.NotFound(w, "La imagen no existe")
		return
	}
	use, err := s.DB.MediaUsage(r.Context(), tenant, ms[0].ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"media": viewMedia(tenant, ms[0]), "usedBy": use})
}

func validAlt(alt string) (string, *apierr.FieldError) {
	alt = strings.TrimSpace(alt)
	if alt == "" {
		e := fe("alt", "required", "Describe la imagen: el texto alternativo es obligatorio (lo leen los lectores de pantalla y los buscadores)")
		return "", &e
	}
	if len(alt) > 300 {
		e := fe("alt", "too_long", "El texto alternativo admite hasta 300 caracteres")
		return "", &e
	}
	return alt, nil
}

// uploadMedia receives one image as multipart/form-data (fields "file" and
// "alt"), validates it by content, generates the renditions and stores them.
func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "media_unavailable", Detail: "El almacenamiento de imágenes no está configurado"})
		return
	}
	tooLarge := func() {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large",
			Detail: fmt.Sprintf("La imagen pasa de %d MB. Redúcela y vuelve a subirla.", media.MaxUploadBytes>>20)})
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			tooLarge()
			return
		}
		apierr.BadRequest(w, "Envía la imagen como multipart/form-data con los campos file y alt")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	alt, altErr := validAlt(r.FormValue("alt"))
	f, hdr, err := r.FormFile("file")
	if err != nil {
		apierr.BadRequest(w, "", fe("file", "required", "Falta el archivo de imagen"))
		return
	}
	defer f.Close()
	if hdr.Size > media.MaxUploadBytes {
		tooLarge()
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, media.MaxUploadBytes+1))
	if err != nil {
		apierr.BadRequest(w, "No se pudo leer el archivo")
		return
	}
	// Type and size are checked before asking for the alt text again, so the
	// person fixes the file first.
	if _, _, err := media.Sniff(data); err != nil {
		s.mediaProblem(w, err)
		return
	}
	if altErr != nil {
		apierr.BadRequest(w, "Falta el texto alternativo", *altErr)
		return
	}
	m, dup, err := s.saveImage(r, data, hdr.Filename, alt)
	if errors.Is(err, errBusy) {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "busy", Detail: "Hay muchas imágenes procesándose; intenta de nuevo en unos segundos"})
		return
	}
	if err != nil {
		s.mediaProblem(w, err)
		return
	}
	status := http.StatusCreated
	if dup {
		status = http.StatusOK
	}
	httpx.JSON(w, r, status, map[string]any{"media": viewMedia(principal(r).Tenant, m), "duplicate": dup})
}

var errBusy = errors.New("media: busy")

// saveImage stores a new image of the library (or finds the identical one).
func (s *Server) saveImage(r *http.Request, data []byte, filename, alt string) (catalog.Media, bool, error) {
	pr := principal(r)
	sum := sha256.Sum256(data)
	if dup, found, err := s.DB.MediaBySHA(r.Context(), pr.Tenant, sum[:]); err != nil {
		return catalog.Media{}, false, err
	} else if found {
		// The same file is already in the library: reuse it instead of storing a copy.
		return dup, true, nil
	}
	// Decoding and resizing hold tens of megabytes; only two run at a time per node.
	if r.URL.Path != "" && !strings.HasSuffix(r.URL.Path, "/edit") {
		select {
		case s.uploadSlots <- struct{}{}:
			defer func() { <-s.uploadSlots }()
		case <-r.Context().Done():
			return catalog.Media{}, false, errBusy
		}
	}
	proc, err := media.Process(data)
	if err != nil {
		return catalog.Media{}, false, err
	}
	id := store.NewID("m_")
	name := filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	if name == "" || name == "." {
		name = "imagen." + proc.Ext
	}
	m := catalog.Media{ID: id, Filename: name, ContentType: proc.ContentType, Bytes: int64(len(data)), Width: proc.Width, Height: proc.Height,
		Alt: alt, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	ctx := r.Context()
	fail := func(err error) (catalog.Media, bool, error) {
		_ = s.Media.DeletePrefix(ctx, pr.Tenant, id)
		return catalog.Media{}, false, err
	}
	if err := s.Media.Put(ctx, pr.Tenant, id+"/original."+proc.Ext, data, proc.ContentType); err != nil {
		return fail(err)
	}
	for _, rd := range proc.Renditions {
		key := fmt.Sprintf("%s/%d.%s", id, rd.Width, rd.Ext)
		if err := s.Media.Put(ctx, pr.Tenant, key, rd.Data, rd.ContentType); err != nil {
			return fail(err)
		}
		m.Variants = append(m.Variants, catalog.MediaVariant{Width: rd.Width, Height: rd.Height, Format: rd.Format, Key: key, Bytes: int64(len(rd.Data))})
	}
	if err := s.DB.InsertMedia(ctx, pr.Tenant, pr.Actor(), m, sum[:]); err != nil {
		return fail(err)
	}
	s.settle(ctx)
	return m, false, nil
}

func (s *Server) mediaProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, media.ErrTooLarge):
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large",
			Detail: fmt.Sprintf("La imagen pasa de %d MB. Redúcela y vuelve a subirla.", media.MaxUploadBytes>>20)})
	case errors.Is(err, media.ErrUnsupported):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnsupportedMediaType, Code: "unsupported_image", Detail: "Sube una imagen JPG, PNG, WebP o GIF. El archivo no es ninguna de ellas."})
	case errors.Is(err, media.ErrDimensions):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "image_too_big",
			Detail: fmt.Sprintf("La imagen tiene demasiados píxeles (máximo %d megapíxeles y %d px por lado)", media.MaxPixels/1_000_000, media.MaxSide)})
	case errors.Is(err, media.ErrCorrupt):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "corrupt_image", Detail: "La imagen está dañada o incompleta y no se pudo leer"})
	default:
		internalError(w, s.Log, err)
	}
}

func (s *Server) patchMedia(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Alt string `json:"alt"`
	}
	if !decode(w, r, &body) {
		return
	}
	alt, e := validAlt(body.Alt)
	if e != nil {
		apierr.BadRequest(w, "", *e)
		return
	}
	pr := principal(r)
	err := s.DB.SetMediaAlt(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), alt)
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "La imagen no existe")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.settle(r.Context())
	ms, err := s.DB.Media(r.Context(), pr.Tenant, r.PathValue("id"))
	if err != nil || len(ms) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"media": viewMedia(pr.Tenant, ms[0])})
}

func (s *Server) deleteMedia(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	err := s.DB.DeleteMedia(r.Context(), pr.Tenant, pr.Actor(), id, r.URL.Query().Get("force") == "true")
	switch {
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "La imagen no existe")
		return
	case errors.Is(err, store.ErrInUse):
		use, _ := s.DB.MediaUsage(r.Context(), pr.Tenant, id)
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "media_in_use",
			Detail: fmt.Sprintf("La imagen se usa en %d producto(s) y %d marca(s). Confirma para quitarla de ahí y eliminarla.", len(use.Products), len(use.Brands))})
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	if s.Media != nil && media.ValidKey(pr.Tenant, id) {
		if err := s.Media.DeletePrefix(r.Context(), pr.Tenant, id); err != nil {
			s.Log.Warn("delete media files", "media", id, "err", err)
		}
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// serveMedia serves a rendition. Keys are never rewritten with other content,
// so the response is cacheable for a year. Originals are not served: they may
// carry EXIF data such as GPS position; renditions are re-encoded without it.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	tenant, id, file := r.PathValue("tenant"), r.PathValue("id"), r.PathValue("file")
	key := id + "/" + file
	ctype := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}[filepath.Ext(file)]
	if s.Media == nil || ctype == "" || strings.HasPrefix(file, "original.") || !media.ValidKey(tenant, key) {
		http.NotFound(w, r)
		return
	}
	obj, err := s.Media.Open(r.Context(), tenant, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer obj.Close()
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeContent(w, r, "", obj.ModTime, obj)
}
