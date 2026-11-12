package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// Quote templates (agreements): the store sets them up for a company in the
// panel; the company's buyers order from them in "Mi empresa".

func (s *Server) routesQuoteTemplates(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/quote-templates", s.authed("customers", auth.Read, noStorefront(s.listQuoteTemplates)))
	handle("POST /v1/quote-templates", s.authed("customers", auth.Admin, s.saveQuoteTemplate))
	handle("PUT /v1/quote-templates/{id}", s.authed("customers", auth.Admin, s.saveQuoteTemplate))
	handle("GET /s/{tenant}/api/account/company/templates", s.shopQuoteTemplates)
	handle("POST /s/{tenant}/api/account/company/templates/{id}/order", s.shopOrderFromTemplate)
}

func (s *Server) titleTemplates(tenant string, list []orders.QuoteTemplate) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return
	}
	for i := range list {
		for j := range list[i].Items {
			if v, p, err := t.Catalog.VariantBySKU(list[i].Items[j].SKU); err == nil {
				list[i].Items[j].Title = p.Title
				if v.Title != "" && len(p.Variants) > 1 {
					list[i].Items[j].Title += " · " + v.Title
				}
			}
		}
	}
}

func (s *Server) listQuoteTemplates(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	list, err := s.Orders.QuoteTemplates(r.Context(), p.Tenant, r.URL.Query().Get("company"), false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.titleTemplates(p.Tenant, list)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) saveQuoteTemplate(w http.ResponseWriter, r *http.Request) {
	var in orders.QuoteTemplate
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	in.ID = r.PathValue("id")
	tn, _ := s.Proj.Tenant(p.Tenant)
	for _, it := range in.Items {
		if _, prod, err := tn.Catalog.VariantBySKU(it.SKU); err != nil || prod.Status != "active" {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "items", Code: "unknown", Message: "El SKU " + it.SKU + " no existe o no está a la venta"})
			return
		}
	}
	out, err := s.Orders.SaveQuoteTemplate(r.Context(), p.Tenant, p.Actor(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	list := []orders.QuoteTemplate{out}
	s.titleTemplates(p.Tenant, list)
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, list[0])
}

func (s *Server) shopQuoteTemplates(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	list, err := s.Orders.QuoteTemplates(r.Context(), r.PathValue("tenant"), c.ID, true)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.titleTemplates(r.PathValue("tenant"), list)
	for i := range list {
		list[i].Note, list[i].CompanyName = "", ""
		if !c.CanViewCredit {
			list[i].Used, list[i].CommitTotal = 0, 0
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list, "canCredit": c.CanCredit && c.Terms != "prepaid"})
}

func (s *Server) shopOrderFromTemplate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	c, email, ok := s.portalCompany(w, r)
	if !ok {
		return
	}
	var in struct {
		Items []orders.TemplateLine `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Items) > 300 {
		apierr.BadRequest(w, "Demasiados productos")
		return
	}
	actor := "company-buyer:" + c.ID
	if a := s.shopperActing(r); a != nil {
		actor = "member:" + a.MemberID
	}
	o, d, err := s.Orders.OrderFromTemplate(r.Context(), r.PathValue("tenant"), actor, r.PathValue("id"), c, orders.Customer{Email: email, Name: c.Name}, in.Items)
	if err != nil {
		s.fail(w, err)
		return
	}
	if o != nil {
		httpx.JSON(w, r, http.StatusCreated, map[string]any{"order": map[string]any{"id": o.ID, "number": o.Number, "grandTotal": o.GrandTotal, "currency": o.Currency}})
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"payUrl": d.PayURL, "grandTotal": d.GrandTotal})
}
