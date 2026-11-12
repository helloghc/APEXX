package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Personalization of a product: text (engraving), list (finish) or checkbox
// (gift wrap), each one able to add to the unit price.

func (s *Server) getCustoms(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	if _, err := t.Catalog.Get(r.PathValue("id")); err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	var fs []cart.CustomField
	if _, err := s.DB.GetDoc(r.Context(), t.ID, "product_customs", r.PathValue("id"), &fs); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if fs == nil {
		fs = []cart.CustomField{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"fields": fs})
}

func (s *Server) putCustoms(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := t.Catalog.Get(id); err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	var body struct {
		Fields []cart.CustomField `json:"fields"`
	}
	if !decode(w, r, &body) {
		return
	}
	clean, errs := cart.CleanCustomFields(body.Fields)
	if len(errs) > 0 {
		out := make([]apierr.FieldError, len(errs))
		for i, e := range errs {
			out[i] = apierr.FieldError{Field: e.Field, Code: e.Code, Message: e.Message}
		}
		apierr.BadRequest(w, "Revisa las personalizaciones", out...)
		return
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "product_customs", id, store.KeyProductCustoms, clean); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"fields": clean})
}
