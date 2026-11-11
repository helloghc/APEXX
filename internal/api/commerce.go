package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/pricing"
)

func (s *Server) getInventory(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	snap, found := t.Inventory.Get(r.PathValue("sku"))
	if !found {
		apierr.NotFound(w, "SKU "+r.PathValue("sku")+" sin registros de inventario")
		return
	}
	httpx.JSON(w, r, http.StatusOK, snap)
}

func (s *Server) putInventory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OnHand int64 `json:"onHand"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.OnHand < 0 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "onHand", Code: "out_of_range", Message: "onHand no puede ser negativo"})
		return
	}
	p := principal(r)
	if err := s.DB.SetStock(r.Context(), p.Tenant, p.Actor(), r.PathValue("sku"), r.PathValue("location"), body.OnHand); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putPrices(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tiers []pricing.Tier `json:"tiers"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Tiers) == 0 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "tiers", Code: "required", Message: "Envía al menos un precio"})
		return
	}
	cur := body.Tiers[0].Unit.Currency
	for i, t := range body.Tiers {
		if t.MinQty < 1 || t.Unit.Amount < 0 || t.Unit.Currency != cur || len(cur) != 3 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "tiers[" + strconv.Itoa(i) + "]", Code: "invalid", Message: "minQty ≥ 1, monto ≥ 0 y una sola moneda ISO"})
			return
		}
	}
	p := principal(r)
	if err := s.DB.SetPrices(r.Context(), p.Tenant, p.Actor(), r.PathValue("channel"), r.PathValue("sku"), body.Tiers); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPrices(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	tiers := t.Pricing.Tiers(r.PathValue("channel"), r.PathValue("sku"))
	if len(tiers) == 0 {
		apierr.NotFound(w, "Sin precios para ese SKU en ese canal")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"channel": r.PathValue("channel"), "sku": r.PathValue("sku"), "tiers": tiers})
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	c, found := t.Channel(r.PathValue("id"))
	if !found {
		apierr.NotFound(w, "Canal no encontrado")
		return
	}
	httpx.JSON(w, r, http.StatusOK, c)
}

func (s *Server) putChannel(w http.ResponseWriter, r *http.Request) {
	var c cart.Channel
	if !decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	errs, err := s.validateChannel(r, &c)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Moneda ISO de 3 letras, impuesto y máximo no negativos", errs...)
		return
	}
	p := principal(r)
	if err := s.DB.SetChannel(r.Context(), p.Tenant, p.Actor(), c.ID, c); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var req cart.Request
	if !decode(w, r, &req) {
		return
	}
	if errs := append(validateItems(req.Channel, req.Items), validateCartExtras(&req)...); len(errs) > 0 {
		apierr.BadRequest(w, "La solicitud tiene errores", errs...)
		return
	}
	if err := s.resolveCart(r, principal(r).Tenant, &req); err != nil {
		internalError(w, s.Log, err)
		return
	}
	res, err := t.Simulate(req)
	var uc cart.ErrUnknownChannel
	if errors.As(err, &uc) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "channel", Code: "unknown", Message: err.Error()})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, res)
}

func validateItems(channel string, items []cart.Item) []apierr.FieldError {
	var errs []apierr.FieldError
	if channel == "" {
		errs = append(errs, apierr.FieldError{Field: "channel", Code: "required", Message: "channel es obligatorio"})
	}
	if len(items) == 0 || len(items) > 200 {
		errs = append(errs, apierr.FieldError{Field: "items", Code: "out_of_range", Message: "items debe tener entre 1 y 200 líneas"})
	}
	for i, it := range items {
		if it.Qty < 1 || it.Qty > 10000 {
			errs = append(errs, apierr.FieldError{Field: "items[" + strconv.Itoa(i) + "].qty", Code: "out_of_range", Message: "qty debe estar entre 1 y 10000"})
		}
	}
	return errs
}

// ---------- orders ----------

func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.Storefront && !p.Permissions.Allows("orders", "edit") {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Se necesita una llave de storefront o acceso de edición a pedidos"})
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 200 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "Idempotency-Key", Code: "required", Message: "Envía un encabezado Idempotency-Key único por compra (8 a 200 caracteres)"})
		return
	}
	var req orders.CheckoutRequest
	if !decode(w, r, &req) {
		return
	}
	errs := validateItems(req.Channel, req.Items)
	if !strings.Contains(req.Customer.Email, "@") {
		errs = append(errs, apierr.FieldError{Field: "customer.email", Code: "invalid", Message: "Correo inválido"})
	}
	if !strings.HasPrefix(req.PaymentMethod, "pm_") && req.PaymentMethod != "credit" {
		errs = append(errs, apierr.FieldError{Field: "paymentMethod", Code: "invalid", Message: "Envía el id pm_… que genera Stripe.js; Apexx nunca recibe datos de tarjeta"})
	}
	if req.CompanyID != "" {
		// A publishable storefront key must never be able to spend a company's credit.
		if !p.Permissions.Allows("orders", "edit") {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Comprar a nombre de una empresa por API requiere acceso de edición a pedidos"})
			return
		}
		c, err := s.Orders.CompanyAccessByID(r.Context(), p.Tenant, req.CompanyID)
		if err != nil {
			s.walletFail(w, err)
			return
		}
		req.Company = c
	}
	errs = append(errs, validateCartExtras(&cart.Request{Coupons: req.Coupons})...)
	if len(errs) > 0 {
		apierr.BadRequest(w, "La compra tiene errores", errs...)
		return
	}
	res, err := s.Orders.Checkout(r.Context(), p.Tenant, p.Actor(), key, req)
	switch {
	case errors.Is(err, orders.ErrIdempotencyReuse):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Detail: "Esa Idempotency-Key ya se usó con otra compra"})
	case errors.Is(err, orders.ErrInProgress):
		w.Header().Set("Retry-After", "2")
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_progress", Detail: "Esta compra se está procesando"})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		writeJSONRaw(w, res.Status, res.Body, res.Status >= 400)
	}
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	withPII := p.Permissions.Allows("orders", "edit") // readers see orders without personal data
	o, err := s.Orders.Get(r.Context(), p.Tenant, r.PathValue("id"), withPII)
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Pedido no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, o)
}

func (s *Server) orderAction(w http.ResponseWriter, r *http.Request, fn func() error) {
	err := fn()
	switch {
	case errors.Is(err, orders.ErrNotFound):
		apierr.NotFound(w, "Pedido no encontrado")
	case errors.Is(err, orders.ErrInvalidState):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "invalid_state", Detail: err.Error()})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		s.getOrder(w, r)
	}
}

func (s *Server) fulfillOrder(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	s.orderAction(w, r, func() error { return s.Orders.Fulfill(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")) })
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	p := principal(r)
	s.orderAction(w, r, func() error { return s.Orders.Cancel(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.Reason) })
}

// CardFor renders a price for builder sections.
func formatPrice(m money.Money) string {
	return strconv.FormatInt(m.Amount/100, 10) + "." + leftPad(strconv.FormatInt(m.Amount%100, 10)) + " " + string(m.Currency)
}

func leftPad(s string) string {
	if len(s) < 2 {
		return "0" + s
	}
	return s
}
