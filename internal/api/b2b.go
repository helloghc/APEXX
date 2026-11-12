package api

import (
	"github.com/helloghc/apexx/internal/apierr"
	"net/http"
	"strings"
	"time"

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
	handle("GET /v1/companies/{id}/members", s.authed(mod, auth.Read, s.companyMembers))
	handle("PUT /v1/companies/{id}/members", s.authed(mod, auth.Admin, s.saveCompanyMembers))
	handle("GET /v1/companies/{id}/locations", s.authed(mod, auth.Read, s.companyLocations))
	handle("PUT /v1/companies/{id}/locations", s.authed(mod, auth.Admin, s.saveCompanyLocations))
	handle("GET /v1/companies/{id}/requests", s.authed(mod, auth.Read, s.companyRequests))

	// The company's own portal in the store: its administrators manage buyers;
	// every buyer sees their requests and month.
	handle("GET /s/{tenant}/api/account/company", s.shopCompanyPortal)
	handle("PUT /s/{tenant}/api/account/company/members", s.shopCompanyMembers)
	handle("POST /s/{tenant}/api/account/company/requests/{id}/cancel", s.shopCancelRequest)

	handle("GET /s/{tenant}/api/company", s.shopCompany)
	handle("GET /s/{tenant}/api/company/prices", s.shopCompanyPrices)
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
	out := map[string]any{"name": c.Name, "terms": c.Terms, "discountBps": c.DiscountBps, "available": c.Available, "email": email, "approvalThreshold": c.ApprovalThreshold, "approver": c.Approver, "requirePO": c.RequirePO, "creditOnly": c.CreditOnly, "admin": c.Admin, "group": c.PriceGroup != ""}
	if c.CatalogRestricted {
		// The pages stay the same for everyone (cacheable); the script hides
		// what is not in the company's catalog, and the cart refuses it.
		out["restricted"], out["handles"] = true, s.companyHandles(r.PathValue("tenant"), r.URL.Query().Get("channel"), c.PriceGroup)
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// companyHandles lists the products a company with a negotiated catalog can
// buy: those with a variant priced by a price list of its group.
func (s *Server) companyHandles(tenant, channel, group string) []string {
	if channel == "" {
		channel = "mx"
	}
	out := []string{}
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return out
	}
	now := time.Now()
	for _, p := range t.Catalog.Products() {
		if p.Status != "active" {
			continue
		}
		for _, v := range p.Variants {
			if t.Pricing.GroupCovers(channel, v.SKU, group, now) {
				out = append(out, p.Handle)
				break
			}
		}
		if len(out) >= 5000 {
			break
		}
	}
	return out
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
	httpx.JSON(w, r, http.StatusOK, map[string]any{"status": v.Status, "level": v.Level, "nextLevel": v.StepLevel})
}

func (s *Server) companyMembers(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.Permissions.Allows("customers", "edit") {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Ver a los compradores requiere permiso de edición de clientes"})
		return
	}
	list, err := s.Orders.Members(r.Context(), p.Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) saveCompanyMembers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []orders.Member `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	list, err := s.Orders.SaveMembers(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), "", body.Items)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) companyLocations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Orders.Locations(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) saveCompanyLocations(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []orders.Location `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	list, err := s.Orders.SaveLocations(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.Items)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) companyRequests(w http.ResponseWriter, r *http.Request) {
	list, err := s.Orders.PurchaseRequests(r.Context(), principal(r).Tenant, r.PathValue("id"), nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

// portalCompany is the company of the signed-in shopper, or a 403 written.
func (s *Server) portalCompany(w http.ResponseWriter, r *http.Request) (*orders.CompanyAccess, string, bool) {
	if _, ok := s.shopperAPI(w, r); !ok {
		return nil, "", false
	}
	w.Header().Set("Cache-Control", "no-store")
	c, email := s.shopperCompany(r)
	if c == nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "no_company", Detail: "Tu cuenta no compra para una empresa"})
		return nil, "", false
	}
	return c, email, true
}

func (s *Server) shopCompanyPortal(w http.ResponseWriter, r *http.Request) {
	c, email, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	tenant := r.PathValue("tenant")
	out := map[string]any{"company": c, "email": email}
	var buyer []byte
	if !c.Admin {
		buyer = c.BuyerHash // a buyer sees only their own requests
	}
	reqs, err := s.Orders.PurchaseRequests(r.Context(), tenant, c.ID, buyer)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out["requests"] = nonNil(reqs)
	if c.Admin {
		members, err := s.Orders.Members(r.Context(), tenant, c.ID)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		locs, err := s.Orders.Locations(r.Context(), tenant, c.ID)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		out["members"], out["locations"] = nonNil(members), nonNil(locs)
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) shopCompanyMembers(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	c, email, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	if !c.Admin {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "not_company_admin", Detail: "Solo quien administra la empresa cambia a sus compradores"})
		return
	}
	var body struct {
		Items []orders.Member `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	list, err := s.Orders.SaveMembers(r.Context(), r.PathValue("tenant"), "company-admin:"+c.ID, c.ID, email, body.Items)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) shopCancelRequest(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	c, _, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	if err := s.Orders.CancelPurchaseRequest(r.Context(), r.PathValue("tenant"), c.ID, r.PathValue("id"), c.BuyerHash); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shopCompanyPrices gives a signed-in company buyer the prices of its own
// price list for the cards and product shown, so the page (the same for
// everyone, cacheable) shows what they will pay.
func (s *Server) shopCompanyPrices(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, accountLimiter) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	c, _ := s.shopperCompany(r)
	t, ok := s.Proj.Tenant(r.PathValue("tenant"))
	if c == nil || c.PriceGroup == "" || !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ch := r.URL.Query().Get("channel")
	if !shopChannel(t, ch) {
		apierr.BadRequest(w, "Canal de venta no disponible")
		return
	}
	if ch == "" {
		ch = mainChannel
	}
	now := time.Now()
	type pv struct {
		Price     string `json:"price"`
		CompareAt string `json:"compareAt,omitempty"`
	}
	quote := func(sku string) (pv, bool) {
		q, ok := t.Pricing.ResolveAt(ch, sku, 1, c.PriceGroup, now)
		if !ok {
			return pv{}, false
		}
		v := pv{Price: pesos(q.Unit)}
		if q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
			v.CompareAt = pesos(*q.CompareAt)
		}
		return v, true
	}
	handles, skus := map[string]pv{}, map[string]pv{}
	for i, h := range strings.Split(r.URL.Query().Get("handles"), ",") {
		if i >= 60 {
			break
		}
		p, ok := t.Catalog.ByHandle(strings.TrimSpace(h))
		if !ok || p.Status != "active" {
			continue
		}
		for j, v := range p.Variants {
			if q, ok := quote(v.SKU); ok {
				skus[v.SKU] = q
				if j == 0 {
					handles[p.Handle] = q
				}
			}
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"handles": handles, "skus": skus})
}
