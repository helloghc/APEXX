package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Point of sale: a counter sale in one step. The units come from the store's
// own location first, the payment is recorded as cash (with change) or a
// card terminal (with its reference), and the order is handed over at once.
// It is an ordinary order: stock, ledger, invoices, returns and points apply.

func (s *Server) routesPOS(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/pos/lookup", s.authed("orders", auth.Edit, noStorefront(s.posLookup)))
	handle("POST /v1/pos/sales", s.authed("orders", auth.Edit, s.posSale))
	handle("GET /v1/pos/locations", s.authed("orders", auth.Edit, noStorefront(s.posLocations)))
}

// posLocations lists the active locations a cashier can sell from: id, name
// and kind only, so the counter does not need access to the store's settings.
func (s *Server) posLocations(w http.ResponseWriter, r *http.Request) {
	c, err := s.DB.LoadLogistics(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	type loc struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Active bool   `json:"active"`
	}
	out := []loc{}
	for _, l := range c.Locations {
		if l.Active {
			out = append(out, loc{l.ID, l.Name, l.Kind, true})
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

type posItem struct {
	SKU       string `json:"sku"`
	Title     string `json:"title"`
	Price     int64  `json:"price"`
	Available int64  `json:"available"`
	Here      int64  `json:"here"` // units at the counter's location
}

// posLookup finds products by barcode, SKU or words.
func (s *Server) posLookup(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	q, loc := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("location")
	out := []posItem{}
	add := func(sku, title string) {
		it := posItem{SKU: sku, Title: title, Available: t.Inventory.Available(sku)}
		if p, ok := t.Pricing.Resolve(mainChannel, sku, 1); ok {
			it.Price = p.Unit.Amount
		}
		if snap, ok := t.Inventory.Get(sku); ok {
			for _, l := range snap.Levels {
				if l.LocationID == loc {
					it.Here = l.Available()
				}
			}
		}
		out = append(out, it)
	}
	if q == "" {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
		return
	}
	// A scanned barcode or a typed SKU matches exactly.
	list, _ := t.Catalog.Search(q, "", 20)
	for _, p := range list {
		for _, v := range p.Variants {
			if v.Barcode == q || strings.EqualFold(v.SKU, q) {
				add(v.SKU, variantTitle(p.Title, v.Title))
				httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "exact": true})
				return
			}
		}
	}
	for _, p := range list {
		for _, v := range p.Variants {
			if len(out) < 30 {
				add(v.SKU, variantTitle(p.Title, v.Title))
			}
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) posSale(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in struct {
		Location string             `json:"location"`
		Items    []orders.ItemInput `json:"items"`
		Customer *orders.Customer   `json:"customer"`
		Discount int64              `json:"discount"`
		Payment  struct {
			Method    string `json:"method"`    // cash | terminal
			Tendered  int64  `json:"tendered"`  // cash received
			Reference string `json:"reference"` // terminal authorization
		} `json:"payment"`
	}
	if !decode(w, r, &in) {
		return
	}
	cfg, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	loc, ok := cfg.LocationByID(in.Location)
	if !ok {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "location", Code: "unknown", Message: "Elige la tienda o ubicación de la caja"})
		return
	}
	in.Payment.Reference = strings.TrimSpace(in.Payment.Reference)
	switch in.Payment.Method {
	case "cash", "terminal":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "payment.method", Code: "invalid", Message: "Pago en efectivo o con terminal"})
		return
	}
	if in.Payment.Method == "terminal" && (in.Payment.Reference == "" || len(in.Payment.Reference) > 60) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "payment.reference", Code: "required", Message: "Escribe la autorización de la terminal"})
		return
	}
	cust := orders.Customer{Name: "Venta en mostrador"}
	if in.Customer != nil && (in.Customer.Email != "" || in.Customer.Name != "") {
		cust = *in.Customer
	}
	d, err := s.Orders.SaveDraft(r.Context(), pr.Tenant, pr.Actor(), "", orders.DraftInput{Channel: mainChannel, Items: in.Items, Customer: cust, Discount: in.Discount,
		Note: "Venta en caja: " + loc.Name, WalkIn: true})
	if err != nil {
		s.fail(w, err)
		return
	}
	if in.Payment.Method == "cash" && in.Payment.Tendered < d.GrandTotal {
		_ = s.Orders.CancelDraft(r.Context(), pr.Tenant, pr.Actor(), d.ID)
		apierr.BadRequest(w, "", apierr.FieldError{Field: "payment.tendered", Code: "short", Message: "El efectivo recibido no cubre el total"})
		return
	}
	// The counter's own units go first.
	ctx := store.WithPreferredLocation(r.Context(), loc.ID)
	o, err := s.Orders.CompleteDraft(ctx, pr.Tenant, pr.Actor(), d.ID, "manual")
	if err != nil {
		_ = s.Orders.CancelDraft(context.Background(), pr.Tenant, pr.Actor(), d.ID)
		s.fail(w, err)
		return
	}
	change := int64(0)
	if in.Payment.Method == "cash" {
		change = in.Payment.Tendered - o.GrandTotal
	}
	if err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE orders SET source='pos' WHERE tenant_id=$1 AND id=$2`, pr.Tenant, o.ID); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "pos.sale", o.ID, map[string]any{"location": loc.ID, "method": in.Payment.Method,
			"tendered": in.Payment.Tendered, "change": change, "reference": in.Payment.Reference})
	}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	// The buyer takes it with them.
	if err := s.Orders.Fulfill(r.Context(), pr.Tenant, pr.Actor(), o.ID); err != nil && s.Log != nil {
		s.Log.Warn("pos fulfill", "order", o.ID, "err", err)
	}
	o, _ = s.Orders.Get(r.Context(), pr.Tenant, o.ID, true)
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"order": o, "change": change, "method": in.Payment.Method, "location": loc.Name})
}
