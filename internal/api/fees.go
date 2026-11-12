package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Extra charges per order (handling, packaging, small order).
func (s *Server) routesFees(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/fees", s.authed("settings", auth.Read, s.getFees))
	handle("PUT /v1/settings/fees", s.authed("settings", auth.Admin, s.putFees))
}

func (s *Server) getFees(w http.ResponseWriter, r *http.Request) {
	rules := []cart.FeeRule{}
	if _, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "order_fees", "rules", &rules); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(rules)})
}

func (s *Server) putFees(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []cart.FeeRule `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	clean, err := cart.CleanFeeRules(body.Items)
	if err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusBadRequest, Code: "validation_failed", Detail: "Revisa los cargos: " + err.Error()})
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "order_fees", "rules", store.KeyOrderFees, clean); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(clean)})
}
