package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// Loyalty: the store's points program, each customer's account, and the
// shopper's balance and redemption into store credit.

func (s *Server) routesLoyalty(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/loyalty", s.authed("settings", auth.Read, s.getLoyalty))
	handle("PUT /v1/settings/loyalty", s.authed("settings", auth.Admin, s.putLoyalty))
	handle("GET /v1/customers/{id}/loyalty", s.authed("customers", auth.Read, s.customerLoyalty))
	handle("POST /v1/customers/{id}/loyalty/adjust", s.authed("customers", auth.Edit, s.adjustLoyalty))
	handle("GET /s/{tenant}/api/account/loyalty", s.shopLoyalty)
	handle("POST /s/{tenant}/api/account/loyalty/redeem", s.shopRedeemPoints)
	handle("GET /s/{tenant}/api/account/groups", s.shopMyGroups)
}

func (s *Server) getLoyalty(w http.ResponseWriter, r *http.Request) {
	var p orders.LoyaltyProgram
	if _, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "loyalty", "program", &p); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if p.Tiers == nil {
		p.Tiers = []orders.LoyaltyTier{}
	}
	httpx.JSON(w, r, http.StatusOK, p)
}

func (s *Server) putLoyalty(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var p orders.LoyaltyProgram
	if !decode(w, r, &p) {
		return
	}
	var errs []apierr.FieldError
	if p.Active && (p.EarnPer < 100 || p.EarnPer > 10_000_000) {
		errs = append(errs, apierr.FieldError{Field: "earnPer", Code: "invalid", Message: "Un punto cada $1 a $100,000"})
	}
	if p.PointValue < 0 || p.PointValue > 100_000 || p.MinRedeem < 0 || p.MinRedeem > 1_000_000 {
		errs = append(errs, apierr.FieldError{Field: "pointValue", Code: "invalid", Message: "Valor del punto de $0 a $1,000"})
	}
	if len(p.Tiers) > 10 {
		errs = append(errs, apierr.FieldError{Field: "tiers", Code: "too_many", Message: "Hasta 10 niveles"})
	}
	names := map[string]bool{}
	for i := range p.Tiers {
		t := &p.Tiers[i]
		t.Name = strings.TrimSpace(t.Name)
		if t.MultiplierPct == 0 {
			t.MultiplierPct = 100
		}
		if t.Name == "" || len([]rune(t.Name)) > 40 || names[t.Name] || t.MinSpend < 0 || t.MultiplierPct < 100 || t.MultiplierPct > 1000 {
			errs = append(errs, apierr.FieldError{Field: "tiers", Code: "invalid", Message: "Cada nivel tiene nombre único, gasto mínimo y multiplicador de 1x a 10x"})
			break
		}
		names[t.Name] = true
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa el programa", errs...)
		return
	}
	if p.Tiers == nil {
		p.Tiers = []orders.LoyaltyTier{}
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "loyalty", "program", "", p); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, p)
}

func (s *Server) customerLoyalty(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if _, err := s.Customers.Get(r.Context(), pr.Tenant, r.PathValue("id"), false); err != nil {
		apierr.NotFound(w, "Cliente no encontrado")
		return
	}
	a, err := s.Orders.LoyaltyAccountOf(r.Context(), pr.Tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, a)
}

func (s *Server) adjustLoyalty(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in struct {
		Points int64  `json:"points"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, err := s.Customers.Get(r.Context(), pr.Tenant, r.PathValue("id"), false); err != nil {
		apierr.NotFound(w, "Cliente no encontrado")
		return
	}
	var fe orders.FieldError
	if err := s.Orders.AdjustPoints(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), in.Points, in.Note); errors.As(err, &fe) {
		apierr.BadRequest(w, fe.Message, apierr.FieldError{Field: fe.Field, Code: "invalid", Message: fe.Message})
		return
	} else if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.customerLoyalty(w, r)
}

func (s *Server) shopLoyalty(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a, err := s.Orders.LoyaltyAccountOf(r.Context(), r.PathValue("tenant"), cid)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var p orders.LoyaltyProgram
	_, _ = s.DB.GetDoc(r.Context(), r.PathValue("tenant"), "loyalty", "program", &p)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"account": a, "active": p.Active, "pointValue": p.PointValue, "minRedeem": p.MinRedeem, "earnPer": p.EarnPer})
}

func (s *Server) shopRedeemPoints(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "cross_site", Detail: "Solicitud de otro sitio"})
		return
	}
	var in struct {
		Points int64 `json:"points"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Customers.Get(r.Context(), r.PathValue("tenant"), cid, true)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	card, err := s.Orders.RedeemPoints(r.Context(), r.PathValue("tenant"), "customer:"+cid, cid, c.Email, c.Name, in.Points)
	if errors.Is(err, orders.ErrLoyalty) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "loyalty", Detail: strings.TrimPrefix(err.Error(), "puntos: ")})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"code": card.Code, "amount": card.Balance, "currency": card.Currency})
}

// shopMyGroups tells the store's page which customer groups the signed-in
// shopper is in, so sections for a segment show (or hide) for them. A guest
// gets an empty list.
func (s *Server) shopMyGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	cid, _ := s.shopper(r)
	out := []string{}
	if cid != "" {
		if c, err := s.Customers.Get(r.Context(), r.PathValue("tenant"), cid, false); err == nil {
			for _, g := range c.Groups {
				out = append(out, g.ID)
			}
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"groups": out})
}
