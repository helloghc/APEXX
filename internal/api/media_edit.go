package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/store"
)

// Image editor: rotate, mirror, crop and adjust light and colour. Edits are
// a recipe over the untouched original, so the store can change or undo
// them at any time; products using the image show the new version at once.
// "As a copy" keeps the image and adds the edited one to the library.

var mediaExt = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}

func (s *Server) editMedia(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "media_unavailable", Detail: "El almacenamiento de imágenes no está configurado"})
		return
	}
	var in struct {
		Edits  media.Edits `json:"edits"`
		AsCopy bool        `json:"asCopy"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := in.Edits.Validate(); err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "edits", Code: "invalid", Message: "Giro de 90°, ajustes de -100 a 100 y un recorte dentro de la imagen"})
		return
	}
	pr := principal(r)
	id := r.PathValue("id")
	ms, err := s.DB.Media(r.Context(), pr.Tenant, id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if len(ms) == 0 {
		apierr.NotFound(w, "La imagen no existe")
		return
	}
	m := ms[0]
	original, _, err := s.readOriginal(r.Context(), pr.Tenant, m)
	if err != nil {
		internalError(w, s.Log, fmt.Errorf("open original: %w", err))
		return
	}
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	case <-r.Context().Done():
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "busy", Detail: "Hay muchas imágenes procesándose; intenta de nuevo en unos segundos"})
		return
	}
	data, editCT := original, m.ContentType
	if !in.Edits.Empty() {
		var e error
		data, editCT, _, _, e = media.Edit(original, in.Edits)
		if errors.Is(e, media.ErrBadEdit) {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "edits.crop", Code: "too_small", Message: "El recorte es demasiado pequeño"})
			return
		}
		if e != nil {
			s.mediaProblem(w, e)
			return
		}
	}
	if in.AsCopy {
		if in.Edits.Empty() {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "edits", Code: "required", Message: "Haz al menos un cambio para guardar una copia"})
			return
		}
		name := strings.TrimSuffix(m.Filename, "."+mediaExt[m.ContentType])
		cp, dup, err := s.saveImage(r, data, name+" (editada)."+mediaExt[editCT], m.Alt)
		if err != nil {
			s.mediaProblem(w, err)
			return
		}
		status := http.StatusCreated
		if dup {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{"media": viewMedia(pr.Tenant, cp), "copy": true})
		return
	}
	proc, err := media.Process(data)
	if err != nil {
		s.mediaProblem(w, err)
		return
	}
	// New keys: browsers and CDNs never show the previous version.
	ver := time.Now().UnixNano()
	var vs []catalog.MediaVariant
	for _, rd := range proc.Renditions {
		key := fmt.Sprintf("%s/e%d-%d.%s", id, ver, rd.Width, rd.Ext)
		if err := s.Media.Put(r.Context(), pr.Tenant, key, rd.Data, rd.ContentType); err != nil {
			internalError(w, s.Log, err)
			return
		}
		vs = append(vs, catalog.MediaVariant{Width: rd.Width, Height: rd.Height, Format: rd.Format, Key: key, Bytes: int64(len(rd.Data))})
	}
	var edits []byte
	if !in.Edits.Empty() {
		edits, _ = json.Marshal(in.Edits)
	}
	err = s.DB.SetMediaEdit(r.Context(), pr.Tenant, pr.Actor(), id, edits, proc.Width, proc.Height, vs)
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "La imagen no existe")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.settle(r.Context())
	m.Width, m.Height, m.Variants, m.Edits = proc.Width, proc.Height, vs, edits
	writeJSON(w, http.StatusOK, map[string]any{"media": viewMedia(pr.Tenant, m), "copy": false})
}

// mediaOriginal streams the untouched upload to the panel's editor. It is
// never public: an original can carry the camera's location.
func (s *Server) mediaOriginal(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	ms, err := s.DB.Media(r.Context(), pr.Tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if len(ms) == 0 || s.Media == nil {
		apierr.NotFound(w, "La imagen no existe")
		return
	}
	data, ct, err := s.readOriginal(r.Context(), pr.Tenant, ms[0])
	if err != nil {
		apierr.NotFound(w, "La imagen no existe")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

// readOriginal returns the untouched upload; images brought in without one
// (an import) use their largest stored size.
func (s *Server) readOriginal(ctx context.Context, tenant string, m catalog.Media) ([]byte, string, error) {
	read := func(key string) ([]byte, error) {
		obj, err := s.Media.Open(ctx, tenant, key)
		if err != nil {
			return nil, err
		}
		defer obj.Close()
		return io.ReadAll(io.LimitReader(obj, media.MaxUploadBytes+1))
	}
	if b, err := read(m.ID + "/original." + mediaExt[m.ContentType]); err == nil {
		return b, m.ContentType, nil
	}
	var best *catalog.MediaVariant
	for i := range m.Variants {
		v := &m.Variants[i]
		if v.Format != "webp" && !strings.Contains(v.Key, "/e") && (best == nil || v.Width > best.Width) {
			best = v
		}
	}
	if best == nil {
		return nil, "", store.ErrNotFound
	}
	b, err := read(best.Key)
	ct := "image/jpeg"
	if best.Format == "png" {
		ct = "image/png"
	}
	return b, ct, err
}
