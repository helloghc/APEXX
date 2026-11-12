package api

import (
	"net/http"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Checkout blocks: a store or its app (with settings permission) adds a
// notice, a required checkbox or an extra field to the payment page.

func (s *Server) routesCheckoutBlocks(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/checkout-blocks", s.authed("settings", auth.Read, s.getCheckoutBlocks))
	handle("PUT /v1/settings/checkout-blocks", s.authed("settings", auth.Admin, s.putCheckoutBlocks))
	handle("GET /s/{tenant}/api/checkout-blocks", s.shopCheckoutBlocks)
}

func (s *Server) getCheckoutBlocks(w http.ResponseWriter, r *http.Request) {
	var d struct {
		Blocks []cart.CheckoutBlock `json:"blocks"`
	}
	if _, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "checkout_blocks", "config", &d); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if d.Blocks == nil {
		d.Blocks = []cart.CheckoutBlock{}
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) putCheckoutBlocks(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var d struct {
		Blocks []cart.CheckoutBlock `json:"blocks"`
	}
	if !decode(w, r, &d) {
		return
	}
	clean, errs := cart.CleanCheckoutBlocks(d.Blocks)
	if len(errs) > 0 {
		out := make([]apierr.FieldError, len(errs))
		for i, e := range errs {
			out[i] = apierr.FieldError{Field: e.Field, Code: e.Code, Message: e.Message}
		}
		apierr.BadRequest(w, "Revisa los bloques", out...)
		return
	}
	// A block added by an app says which one, so the store knows where it came from.
	for i := range clean {
		if pr.Kind == "app" {
			clean[i].Source = pr.Name
		} else if strings.HasPrefix(clean[i].Source, " ") {
			clean[i].Source = ""
		}
	}
	d.Blocks = clean
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "checkout_blocks", "config", store.KeyCheckoutBlocks, d); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) shopCheckoutBlocks(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(r.PathValue("tenant"))
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	blocks := t.CheckoutBlocks()
	if blocks == nil {
		blocks = []cart.CheckoutBlock{}
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"blocks": blocks})
}
