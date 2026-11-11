package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// routesWallet registers gift cards, store credit, instalments and risk rules.
func (s *Server) routesWallet(handle func(string, http.HandlerFunc)) {
	const mod = "payments"
	handle("GET /v1/gift-cards", s.authed(mod, auth.Read, s.listGiftCards))
	handle("POST /v1/gift-cards", s.authed(mod, auth.Edit, s.createGiftCard))
	handle("GET /v1/gift-cards/{id}", s.authed(mod, auth.Read, s.getGiftCard))
	handle("PATCH /v1/gift-cards/{id}", s.authed(mod, auth.Edit, s.patchGiftCard))
	handle("GET /v1/payment-rules", s.authed(mod, auth.Read, s.getPaymentRules))
	handle("PUT /v1/payment-rules", s.authed(mod, auth.Admin, s.putPaymentRules))
	handle("POST /v1/orders/{id}/risk/clear", s.authed("orders", auth.Edit, s.clearRisk))

	handle("POST /s/{tenant}/api/giftcard", s.shopGiftCard)
}

var giftCardLimiter = &shopLimiter{n: 10} // code guesses per address per minute

func (s *Server) walletFail(w http.ResponseWriter, err error) {
	var gce *orders.GiftCardError
	if errors.As(err, &gce) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: gce.Code, Detail: gce.Message})
		return
	}
	s.fail(w, err)
}

func (s *Server) listGiftCards(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := queryLimit(r, 50, 100)
	var before time.Time
	if c := q.Get("cursor"); c != "" {
		before, _ = time.Parse(time.RFC3339Nano, c)
	}
	list, stats, err := s.Orders.GiftCards(r.Context(), principal(r).Tenant, q.Get("kind"), q.Get("q"), before, limit+1)
	if err != nil {
		s.walletFail(w, err)
		return
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].CreatedAt.Format(time.RFC3339Nano)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list), "nextCursor": next, "stats": stats})
}

func (s *Server) createGiftCard(w http.ResponseWriter, r *http.Request) {
	var in orders.IssueInput
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	c, err := s.Orders.IssueGiftCard(r.Context(), pr.Tenant, pr.Actor(), in)
	if err != nil {
		s.walletFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusCreated, c)
}

func (s *Server) getGiftCard(w http.ResponseWriter, r *http.Request) {
	c, txs, err := s.Orders.GiftCard(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if err != nil {
		s.walletFail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"card": c, "movements": nonNil(txs)})
}

func (s *Server) patchGiftCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
		Delta  int64  `json:"delta"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	pr := principal(r)
	c, err := s.Orders.UpdateGiftCard(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), body.Status, body.Delta, body.Note)
	if err != nil {
		s.walletFail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, c)
}

func (s *Server) getPaymentRules(w http.ResponseWriter, r *http.Request) {
	inst, risk, err := s.Orders.PaymentRules(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = inst.Normalize()
	_ = risk.Normalize()
	httpx.JSON(w, r, http.StatusOK, map[string]any{"installments": inst, "risk": risk})
}

func (s *Server) putPaymentRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Installments *orders.InstallmentRules `json:"installments"`
		Risk         *orders.RiskRules        `json:"risk"`
	}
	if !decode(w, r, &body) {
		return
	}
	pr := principal(r)
	if body.Installments != nil {
		if err := body.Installments.Normalize(); err != nil {
			s.fail(w, err)
			return
		}
		if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "payments", "installments", "", body.Installments); err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	if body.Risk != nil {
		if err := body.Risk.Normalize(); err != nil {
			s.fail(w, err)
			return
		}
		if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "payments", "risk", "", body.Risk); err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	s.getPaymentRules(w, r)
}

func (s *Server) clearRisk(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.Orders.ClearRisk(r.Context(), pr.Tenant, pr.Actor(), pr.Name, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shopGiftCard tells the shopper how much a code has. Guesses are rate
// limited and every wrong code gets the same answer.
func (s *Server) shopGiftCard(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, giftCardLimiter) {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	c, err := s.Orders.CheckGiftCard(r.Context(), r.PathValue("tenant"), body.Code, "")
	if err != nil {
		s.walletFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"last4": c.Last4, "balance": map[string]any{"amount": c.Balance, "currency": c.Currency}, "kind": c.Kind})
}
