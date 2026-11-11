package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// routesB2B registers business customers (companies) and their credit.
func (s *Server) routesB2B(handle func(string, http.HandlerFunc)) {
	const mod = "customers"
	handle("GET /v1/companies", s.authed(mod, auth.Read, s.listCompanies))
	handle("POST /v1/companies", s.authed(mod, auth.Admin, s.saveCompany))
	handle("GET /v1/companies/{id}", s.authed(mod, auth.Read, s.getCompany))
	handle("PUT /v1/companies/{id}", s.authed(mod, auth.Admin, s.saveCompany))
	handle("POST /v1/companies/{id}/entries", s.authed(mod, auth.Admin, s.companyEntry))

	handle("GET /s/{tenant}/api/company", s.shopCompany)
}

// shopperCompany returns the company the signed-in shopper buys for, and the
// email of their account. Nil when there is no session or no company.
func (s *Server) shopperCompany(r *http.Request) (*orders.CompanyAccess, string) {
	if s.Customers == nil || s.Orders == nil {
		return nil, ""
	}
	id, _ := s.shopper(r)
	if id == "" {
		return nil, ""
	}
	tenant := r.PathValue("tenant")
	c, err := s.Customers.Get(r.Context(), tenant, id, true)
	if err != nil || c.Email == "" {
		return nil, ""
	}
	a, err := s.Orders.CompanyForEmail(r.Context(), tenant, c.Email)
	if err != nil || a == nil {
		return nil, ""
	}
	return a, c.Email
}

// shopCompany tells the checkout whether the signed-in shopper buys for a company.
func (s *Server) shopCompany(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, accountLimiter) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	c, email := s.shopperCompany(r)
	if c == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"name": c.Name, "terms": c.Terms, "discountBps": c.DiscountBps, "available": c.Available, "email": email})
}

func (s *Server) listCompanies(w http.ResponseWriter, r *http.Request) {
	list, err := s.Orders.Companies(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) getCompany(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	c, buyers, entries, err := s.Orders.Company(r.Context(), p.Tenant, r.PathValue("id"), p.Permissions.Allows("customers", "edit"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"company": c, "buyers": nonNil(buyers), "statement": nonNil(entries)})
}

func (s *Server) saveCompany(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orders.Company
		BuyerEmails []string `json:"buyerEmails"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Company.ID = r.PathValue("id")
	p := principal(r)
	out, err := s.Orders.SaveCompany(r.Context(), p.Tenant, p.Actor(), body.Company, body.BuyerEmails)
	if err != nil {
		s.fail(w, err)
		return
	}
	code := http.StatusOK
	if r.Method == http.MethodPost {
		code = http.StatusCreated
	}
	httpx.JSON(w, r, code, out)
}

func (s *Server) companyEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string `json:"kind"`
		Amount int64  `json:"amount"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	if err := s.Orders.CompanyPayment(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.Kind, body.Amount, body.Note); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
