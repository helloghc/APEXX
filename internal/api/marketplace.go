package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/orders"
)

// routesMarketplace registers seller management (for the store) and the
// seller portal (for the seller's own people).
func (s *Server) routesMarketplace(handle func(string, http.HandlerFunc)) {
	const mod = "marketplace"
	handle("GET /v1/sellers", s.authed(mod, auth.Read, s.listSellers))
	handle("POST /v1/sellers", s.authed(mod, auth.Admin, s.saveSeller))
	handle("GET /v1/sellers/{id}", s.authed(mod, auth.Read, s.getSeller))
	handle("PUT /v1/sellers/{id}", s.authed(mod, auth.Admin, s.saveSeller))
	handle("PUT /v1/sellers/{id}/products", s.authed(mod, auth.Edit, s.setSellerProducts))
	handle("POST /v1/sellers/{id}/entries", s.authed(mod, auth.Admin, s.sellerEntry))
	handle("POST /v1/sellers/{id}/invite", s.authed(mod, auth.Admin, s.inviteSeller))

	handle("GET /v1/seller/me", s.authed("", "", s.sellerOnly(s.sellerMe)))
	handle("GET /v1/seller/orders", s.authed("", "", s.sellerOnly(s.sellerOrders)))
	handle("POST /v1/seller/orders/{id}/ship", s.authed("", "", s.sellerOnly(s.sellerShip)))
}

// sellerOnly lets through only people who work for an active seller.
func (s *Server) sellerOnly(h func(http.ResponseWriter, *http.Request, orders.Seller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		if p.SellerID == "" {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "sellers_only", Detail: "Esta sección es el portal de vendedores"})
			return
		}
		sel, _, err := s.Orders.Seller(r.Context(), p.Tenant, p.SellerID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if sel.Status != "active" {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "seller_paused", Detail: "Tu cuenta de vendedor está en pausa. Escribe a la tienda"})
			return
		}
		h(w, r, sel)
	}
}

func (s *Server) listSellers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Orders.Sellers(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) getSeller(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	sel, products, err := s.Orders.Seller(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	st, err := s.Orders.SellerStatement(r.Context(), tenant, sel.ID, 200)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var refs []productRef
	if t, ok := s.Proj.Tenant(tenant); ok {
		refs = refsOf(t, products)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"seller": sel, "products": nonNil(refs), "statement": nonNil(st)})
}

func (s *Server) saveSeller(w http.ResponseWriter, r *http.Request) {
	var in orders.Seller
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("id")
	p := principal(r)
	out, err := s.Orders.SaveSeller(r.Context(), p.Tenant, p.Actor(), in)
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

func (s *Server) setSellerProducts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProductIDs []string `json:"productIds"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	if err := s.Orders.SetSellerProducts(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.ProductIDs); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sellerEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string `json:"kind"`
		Amount int64  `json:"amount"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	if err := s.Orders.SellerPayout(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.Kind, body.Amount, body.Note); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// inviteSeller invites a person of the seller to the seller portal.
func (s *Server) inviteSeller(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	sel, _, err := s.Orders.Seller(r.Context(), p.Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	inv, token, err := s.Auth.Invite(r.Context(), p.Tenant, p.Actor(), body.Email, body.Name, auth.Permissions{}, sel.ID)
	if err != nil {
		s.authFail(w, err)
		return
	}
	link := s.adminURL(r) + "#invitacion/" + token
	emailed := false
	if s.Notify != nil {
		_, err := s.Notify.Enqueue(r.Context(), p.Tenant, notify.Message{Template: notify.TplTeamInvite, To: inv.Email, Name: inv.Name, DedupeKey: "invite-" + inv.ID, Force: true,
			Data: notify.Data{ActionURL: link, Vars: map[string]string{"cliente.nombre": firstName(inv.Name), "equipo.invita": p.Name}}})
		emailed = err == nil
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"invitation": inv, "link": link, "emailed": emailed})
}

// ---------- seller portal ----------

func (s *Server) sellerMe(w http.ResponseWriter, r *http.Request, sel orders.Seller) {
	tenant := principal(r).Tenant
	_, ids, err := s.Orders.Seller(r.Context(), tenant, sel.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	st, err := s.Orders.SellerStatement(r.Context(), tenant, sel.ID, 100)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	type variant struct {
		SKU       string `json:"sku"`
		Title     string `json:"title"`
		Available int64  `json:"available"`
		Price     int64  `json:"price"`
	}
	type product struct {
		ID       string    `json:"id"`
		Title    string    `json:"title"`
		Status   string    `json:"status"`
		Variants []variant `json:"variants"`
	}
	products := []product{}
	if t, ok := s.Proj.Tenant(tenant); ok {
		for _, id := range ids {
			p, err := t.Catalog.Get(id)
			if err != nil {
				continue
			}
			row := product{ID: p.ID, Title: p.Title, Status: p.Status, Variants: []variant{}}
			for _, v := range p.Variants {
				x := variant{SKU: v.SKU, Title: v.Title, Available: t.Inventory.Available(v.SKU)}
				if q, ok := t.Pricing.Resolve("mx", v.SKU, 1); ok {
					x.Price = q.Unit.Amount
				}
				row.Variants = append(row.Variants, x)
			}
			products = append(products, row)
		}
	}
	// The seller sees its own numbers, not the store's note about it.
	sel.Note = ""
	httpx.JSON(w, r, http.StatusOK, map[string]any{"seller": sel, "products": products, "statement": nonNil(st)})
}

func (s *Server) sellerOrders(w http.ResponseWriter, r *http.Request, sel orders.Seller) {
	list, err := s.Orders.SellerOrders(r.Context(), principal(r).Tenant, sel.ID, r.URL.Query().Get("pending") == "1", 300)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) sellerShip(w http.ResponseWriter, r *http.Request, sel orders.Seller) {
	var body struct {
		Carrier        string `json:"carrier"`
		TrackingNumber string `json:"trackingNumber"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	f, err := s.Orders.SellerShip(r.Context(), p.Tenant, p.Actor(), sel.ID, r.PathValue("id"), body.Carrier, body.TrackingNumber)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, f)
}
