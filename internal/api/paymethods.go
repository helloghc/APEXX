package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/payments"
)

// Payment methods besides card: cash at OXXO and SPEI transfer.
func (s *Server) routesPayMethods(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/payment-methods", s.authed("settings", auth.Read, s.getPayMethods))
	handle("PUT /v1/settings/payment-methods", s.authed("settings", auth.Admin, s.putPayMethods))
	handle("GET /s/{tenant}/api/payment-methods", s.shopPayMethods)
	// Sandbox only: the buyer pays the voucher (QA and tests).
	handle("POST /v1/sandbox/orders/{id}/pay-voucher", s.authed("orders", auth.Edit, s.sandboxPayVoucher))
}

func (s *Server) getPayMethods(w http.ResponseWriter, r *http.Request) {
	pm, err := s.Orders.PaymentMethods(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, pm)
}

func (s *Server) putPayMethods(w http.ResponseWriter, r *http.Request) {
	var in orders.PaymentMethodSettings
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "payments", "methods", "", in); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, in)
}

// shopPayMethods tells the checkout which voucher methods to offer.
func (s *Server) shopPayMethods(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) {
		return
	}
	pm, err := s.Orders.PaymentMethods(r.Context(), r.PathValue("tenant"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpx.JSON(w, r, http.StatusOK, pm)
}

func (s *Server) sandboxPayVoucher(w http.ResponseWriter, r *http.Request) {
	var sb *payments.Sandbox
	ok := false
	if s.Payments != nil {
		sb, ok = s.Payments.Sandbox.(*payments.Sandbox)
	}
	if !ok {
		apierr.Write(w, apierr.Problem{Status: http.StatusNotFound, Code: "not_found", Detail: "Solo en modo de prueba"})
		return
	}
	p := principal(r)
	// The order must belong to this store before anything is touched.
	if _, err := s.Orders.Get(r.Context(), p.Tenant, r.PathValue("id"), false); err != nil {
		s.fail(w, err)
		return
	}
	if !sb.PayVoucher(r.PathValue("id")) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "invalid_state", Detail: "Ese pedido no tiene una referencia por pagar"})
		return
	}
	if err := s.Orders.ResolvePending(r.Context(), p.Tenant, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
