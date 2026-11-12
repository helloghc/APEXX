package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Preorders: sell a SKU before it is in stock, from a pool of units, with an
// optional deposit. The balance is charged later with a payment link sent to
// every buyer at once when the product is ready.

func (s *Server) routesPreorders(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/preorders", s.authed("settings", auth.Read, s.listPreorders))
	handle("GET /v1/preorders/{sku}", s.authed("settings", auth.Read, s.getPreorder))
	handle("PUT /v1/preorders/{sku}", s.authed("catalog", auth.Edit, s.putPreorder))
	handle("DELETE /v1/preorders/{sku}", s.authed("catalog", auth.Edit, s.deletePreorder))
	handle("POST /v1/preorders/{sku}/collect", s.authed("orders", auth.Edit, s.collectPreorder))
}

func (s *Server) listPreorders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.DB.Preorders(r.Context(), principal(r).Tenant, "", false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if ps == nil {
		ps = []store.Preorder{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ps})
}

func (s *Server) getPreorder(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	ps, err := s.DB.Preorders(r.Context(), pr.Tenant, r.PathValue("sku"), false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if len(ps) == 0 {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"sku": r.PathValue("sku"), "active": false, "configured": false})
		return
	}
	owed, err := s.Orders.PreorderBalances(r.Context(), pr.Tenant, r.PathValue("sku"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"preorder": ps[0], "configured": true, "ordersOwing": len(owed)})
}

func (s *Server) putPreorder(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	sku := r.PathValue("sku")
	if _, _, err := t.Catalog.VariantBySKU(sku); err != nil {
		apierr.NotFound(w, "No existe un producto con ese SKU")
		return
	}
	var in store.Preorder
	if !decode(w, r, &in) {
		return
	}
	in.SKU = sku
	var errs []apierr.FieldError
	day, err := time.Parse("2006-01-02", in.ShipsOn)
	switch {
	case err != nil:
		errs = append(errs, apierr.FieldError{Field: "shipsOn", Code: "invalid", Message: "Escribe la fecha de envío (AAAA-MM-DD)"})
	case in.Active && day.Before(time.Now().UTC().Truncate(24*time.Hour)):
		errs = append(errs, apierr.FieldError{Field: "shipsOn", Code: "past", Message: "La fecha de envío ya pasó"})
	case day.After(time.Now().AddDate(2, 0, 0)):
		errs = append(errs, apierr.FieldError{Field: "shipsOn", Code: "too_far", Message: "La fecha de envío es de hasta dos años"})
	}
	if in.DepositBps == 0 {
		in.DepositBps = 10000
	}
	if in.DepositBps < 500 || in.DepositBps > 10000 {
		errs = append(errs, apierr.FieldError{Field: "depositBps", Code: "invalid", Message: "El anticipo es de 5% a 100%"})
	}
	if in.MaxUnits < 0 || in.MaxUnits > 1e9 {
		errs = append(errs, apierr.FieldError{Field: "maxUnits", Code: "invalid", Message: "Límite inválido"})
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 200 {
		errs = append(errs, apierr.FieldError{Field: "note", Code: "too_long", Message: "Nota de hasta 200 caracteres"})
	}
	if len(t.Inventory.Bundle(sku)) > 0 {
		errs = append(errs, apierr.FieldError{Field: "sku", Code: "kit", Message: "Un kit no se vende en preventa; pon en preventa sus componentes"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	out, err := s.DB.SetPreorder(r.Context(), pr.Tenant, pr.Actor(), in)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" {
		apierr.BadRequest(w, "El límite no puede ser menor que lo ya vendido", apierr.FieldError{Field: "maxUnits", Code: "below_sold", Message: "Ya se vendieron más piezas que ese límite"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) deletePreorder(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.DB.DeletePreorder(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("sku")); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// collectPreorder emails every buyer who still owes the balance a link to pay
// it. Each link is new; an order already paid in full is skipped.
func (s *Server) collectPreorder(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	ids, err := s.Orders.PreorderBalances(r.Context(), pr.Tenant, r.PathValue("sku"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	sent, failed := 0, 0
	for _, id := range ids {
		if _, err := s.Orders.SendBalanceLink(r.Context(), pr.Tenant, pr.Actor(), id); err != nil {
			if !errors.Is(err, orders.ErrInvalidState) {
				s.Log.Error("preorder balance link", "tenant", pr.Tenant, "order", id, "err", err)
			}
			failed++
			continue
		}
		sent++
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"sent": sent, "failed": failed})
}
