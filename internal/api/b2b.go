package api

import (
	"github.com/helloghc/apexx/internal/apierr"
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
	handle("GET /s/{tenant}/{site}/aprobacion/{token}", s.shopApproval)
	handle("POST /s/{tenant}/api/approval/{token}/{action}", s.shopApprovalAct)
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
	httpx.JSON(w, r, http.StatusOK, map[string]any{"name": c.Name, "terms": c.Terms, "discountBps": c.DiscountBps, "available": c.Available, "email": email, "approvalThreshold": c.ApprovalThreshold, "approver": c.Approver, "requirePO": c.RequirePO, "creditOnly": c.CreditOnly})
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
	c, buyers, approvers, entries, err := s.Orders.CompanyFull(r.Context(), p.Tenant, r.PathValue("id"), p.Permissions.Allows("customers", "edit"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"company": c, "buyers": nonNil(buyers), "approvers": nonNil(approvers), "statement": nonNil(entries)})
}

func (s *Server) saveCompany(w http.ResponseWriter, r *http.Request) {
	var body struct {
		orders.Company
		BuyerEmails    []string `json:"buyerEmails"`
		ApproverEmails []string `json:"approverEmails"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Company.ID = r.PathValue("id")
	p := principal(r)
	out, err := s.Orders.SaveCompanyWith(r.Context(), p.Tenant, p.Actor(), body.Company, body.BuyerEmails, body.ApproverEmails)
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

type approvalPage struct {
	pageData
	Req   *orders.PurchaseRequest
	Token string
}

// shopApproval is the page where an approver of a company reviews a purchase.
// The emailed link is the key.
func (s *Server) shopApproval(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	pd.Title, pd.NoIndex = "Aprobar compra", true
	pg := approvalPage{pageData: pd, Token: r.PathValue("token")}
	if !subLimiter.allow(pd.Tenant + "|" + s.clientIP(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
	} else if v, err := s.Orders.PurchaseRequestByToken(r.Context(), pd.Tenant, pg.Token); err == nil {
		pg.Req = &v
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.renderShop(w, "approval", pg, false)
}

func (s *Server) shopApprovalAct(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, subLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	action := r.PathValue("action")
	if action != "approve" && action != "reject" {
		apierr.NotFound(w, "Acción desconocida")
		return
	}
	v, problem, err := s.Orders.DecidePurchase(r.Context(), r.PathValue("tenant"), r.PathValue("token"), action == "approve")
	if err != nil {
		s.fail(w, err)
		return
	}
	if problem != "" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "cannot_place", Detail: "No se pudo colocar el pedido: " + problem})
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"status": v.Status})
}
