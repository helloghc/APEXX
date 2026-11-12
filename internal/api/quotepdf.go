package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/pdfdoc"
)

// Quote documents and per-line comments: the store and the buyer download
// the same PDF (the offer with its prices, or the request while it waits),
// and either side can comment on a single line of the quote.

func (s *Server) routesQuoteDocs(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/quotes/{id}/pdf", s.authed("orders", auth.Read, noStorefront(s.quotePDFPanel)))
	handle("POST /v1/quotes/{id}/comments", s.authed("orders", auth.Edit, s.quoteCommentPanel))
	handle("GET /s/{tenant}/api/quotes/{token}/pdf", s.quotePDFBuyer)
	handle("POST /s/{tenant}/api/quotes/{token}/comments", s.quoteCommentBuyer)
}

func (s *Server) quotePDF(tenant string, q orders.PriceQuote) []byte {
	store := tenant
	if t, ok := s.Proj.Tenant(tenant); ok && t.Site().Settings.Name != "" {
		store = t.Site().Settings.Name
	}
	title := "Cotización #" + strconv.FormatInt(q.Number, 10)
	d := pdfdoc.New(title + " · " + store)
	d.Text(0, store, 11, true, 0)
	d.Line(11)
	d.Text(0, title, 20, true, 0)
	d.Line(20)
	status := map[string]string{"requested": "Solicitada, en espera de precios", "offered": "Con precios, en espera de respuesta", "accepted": "Aceptada", "rejected": "Rechazada", "expired": "Vencida", "cancelled": "Cancelada"}[q.Status]
	d.Text(0, "Fecha: "+q.CreatedAt.Format("02/01/2006")+"   ·   Estado: "+status, 9, false, 0)
	d.Line(9)
	if q.ExpiresAt != nil {
		d.Text(0, "Válida hasta: "+q.ExpiresAt.In(time.Local).Format("02/01/2006 15:04"), 9, false, 0)
		d.Line(9)
	}
	if c := q.Customer; c != nil {
		d.Text(0, "Para: "+c.Name+"  <"+c.Email+">", 9, false, 0)
		d.Line(9)
	}
	d.Space(6)
	d.Rule()
	// Columns: product (0-300), quantity (right at 370), unit (right at 450), total (right at 516).
	d.Text(0, "Producto", 9, true, 290)
	d.TextRight(370, "Cantidad", 9, true)
	d.TextRight(450, "Precio", 9, true)
	d.TextRight(516, "Importe", 9, true)
	d.Line(9)
	d.Rule()
	comments := map[string][]orders.LineComment{}
	for _, c := range q.Comments {
		comments[c.SKU] = append(comments[c.SKU], c)
	}
	cur := "MXN"
	line := func(sku, title string, qty, unit, total int64, priced bool) {
		d.Text(0, title, 10, false, 290)
		d.TextRight(370, strconv.FormatInt(qty, 10), 10, false)
		if priced {
			d.TextRight(450, pdfdoc.Money(unit, cur), 10, false)
			d.TextRight(516, pdfdoc.Money(total, cur), 10, true)
		} else {
			d.TextRight(516, "Por cotizar", 10, false)
		}
		d.Line(10)
		d.Text(0, sku, 8, false, 290)
		d.Line(8)
		for _, c := range comments[sku] {
			who := "Comprador"
			if c.Who == "store" {
				who = "Tienda"
			}
			if c.Author != "" {
				who += " (" + c.Author + ")"
			}
			d.Paragraph(14, who+": "+c.Body, 8, 470)
		}
		d.Space(4)
	}
	if o := q.Offer; o != nil {
		cur = o.Currency
		for _, it := range o.Items {
			line(it.SKU, it.Title, it.Qty, it.Unit, it.Total, true)
		}
		d.Rule()
		row := func(label string, v int64, bold bool) {
			d.TextRight(450, label, 10, bold)
			d.TextRight(516, pdfdoc.Money(v, cur), 10, bold)
			d.Line(10)
		}
		row("Productos", o.ItemsTotal, false)
		if o.Discount > 0 {
			row("Descuento", -o.Discount, false)
		}
		row("Envío", o.Shipping, false)
		row("Total", o.GrandTotal, true)
	} else {
		t, _ := s.Proj.Tenant(tenant)
		for _, it := range q.Items {
			name := it.SKU
			if t != nil {
				if v, p, err := t.Catalog.VariantBySKU(it.SKU); err == nil {
					name = p.Title
					if v.Title != "" && len(p.Variants) > 1 {
						name += " · " + v.Title
					}
				}
			}
			line(it.SKU, name, it.Qty, 0, 0, false)
		}
	}
	d.Space(10)
	for _, ev := range q.History {
		if ev.Message == "" {
			continue
		}
		who := "Comprador"
		if ev.Who == "store" {
			who = "Tienda"
		}
		d.Paragraph(0, fmt.Sprintf("%s · %s: %s", ev.At.Format("02/01/2006"), who, ev.Message), 8, 0)
	}
	d.Space(8)
	d.Paragraph(0, "Precios en "+cur+". Esta cotización no es una factura; el pedido se confirma al aceptarla.", 8, 0)
	return d.Bytes()
}

func writePDF(w http.ResponseWriter, name string, b []byte) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(b)
}

func (s *Server) quotePDFPanel(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	q, err := s.Orders.GetQuote(r.Context(), p.Tenant, r.PathValue("id"), ordersPII(r))
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Cotización no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	writePDF(w, fmt.Sprintf("cotizacion-%d.pdf", q.Number), s.quotePDF(p.Tenant, q))
}

func (s *Server) quotePDFBuyer(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, accountLimiter) {
		return
	}
	q, err := s.Orders.QuoteByToken(r.Context(), r.PathValue("tenant"), r.PathValue("token"))
	if err != nil {
		apierr.NotFound(w, "Cotización no encontrada")
		return
	}
	writePDF(w, fmt.Sprintf("cotizacion-%d.pdf", q.Number), s.quotePDF(r.PathValue("tenant"), q))
}

func (s *Server) quoteCommentPanel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SKU  string `json:"sku"`
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	name := p.Name
	if name == "" {
		name = "Tienda"
	}
	c, err := s.Orders.CommentQuoteLine(r.Context(), p.Tenant, r.PathValue("id"), "store", name, in.SKU, in.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, c)
}

func (s *Server) quoteCommentBuyer(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, subLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	var in struct {
		SKU  string `json:"sku"`
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	q, err := s.Orders.QuoteByToken(r.Context(), r.PathValue("tenant"), r.PathValue("token"))
	if err != nil {
		apierr.NotFound(w, "Cotización no encontrada")
		return
	}
	author := ""
	if q.Customer != nil {
		author = q.Customer.Name
	}
	if a := s.shopperActing(r); a != nil {
		author = a.Name + " (representante)"
	}
	c, err := s.Orders.CommentQuoteLine(r.Context(), r.PathValue("tenant"), q.ID, "buyer", author, in.SKU, in.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, c)
}
