// Package orders runs checkout, payment, fulfilment, cancellation and refunds
// as explicit, reversible steps:
//
//	checkout: idempotency → reserve stock + create order (one transaction)
//	          → charge (Stripe, idempotent by order id)
//	          → paid: consume reservation, ledger, invoice queued   (one transaction)
//	          → declined/error: release reservation, cancel order   (one transaction)
//	crash between steps: the reconciler asks Stripe what happened and finishes
//	the order either way; stock reservations also expire on their own.
//	cancel/refund: refund at Stripe (idempotent) → restock exact locations,
//	reversing ledger entries, events.
//
// Every state change appends an immutable order event and an outbox event.
package orders

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/promostore"
	"github.com/helloghc/apexx/internal/store"
)

// Statuses.
const (
	StatusPendingPayment = "pending_payment"
	StatusPaid           = "paid"
	StatusFulfilled      = "fulfilled"
	StatusCancelled      = "cancelled"
	StatusRefunded       = "refunded"
)

// Errors.
var (
	ErrNotFound         = errors.New("orders: not found")
	ErrInvalidState     = errors.New("orders: invalid state for this operation")
	ErrIdempotencyReuse = errors.New("orders: idempotency key reused with a different request")
	ErrInProgress       = errors.New("orders: a request with this idempotency key is in progress")
	ErrCartChanged      = errors.New("Algunos productos cambiaron de precio o disponibilidad; revisa tu carrito")
	ErrDeclined         = errors.New("orders: payment declined")
	ErrNoPaymentAccount = errors.New("La tienda todavía no conectó su cuenta de Stripe")
)

// Customer is personal data; it is stored encrypted with the tenant key.
type Customer struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Phone   string `json:"phone,omitempty"`
	Address string `json:"address,omitempty"`
	Notes   string `json:"notes,omitempty"` // note for the store, when the checkout offers the field
}

// CheckoutRequest places an order.
type CheckoutRequest struct {
	Channel       string      `json:"channel"`
	PostalCode    string      `json:"postalCode"`
	Items         []cart.Item `json:"items"`
	Customer      Customer    `json:"customer"`
	PaymentMethod string      `json:"paymentMethod"` // pm_... from Stripe.js
	Coupons       []string    `json:"coupons,omitempty"`
	// GiftCards are codes of gift cards or store credit to pay with first.
	GiftCards []string `json:"giftCards,omitempty"`
	// Installments is the number of monthly payments without interest (0 = one payment).
	Installments int `json:"installments,omitempty"`
	// ClientIP is set by the server (never by clients) for risk rules.
	ClientIP string `json:"-"`
	// SaveCard keeps the card for the next purchase of a signed-in customer;
	// SavedCard pays with one they kept. The store's server fills SaveFor and
	// Saved after checking the customer; a client can never set them.
	SaveCard  bool        `json:"saveCard,omitempty"`
	SavedCard string      `json:"savedCard,omitempty"`
	SaveFor   string      `json:"-"`
	Saved     *SavedToken `json:"-"`
	// CompanyID buys for a company. Only honoured for server-side integrations
	// with permission to edit orders; shoppers get their company from their session.
	CompanyID string `json:"companyId,omitempty"`
	// Company is set by the server when the signed-in shopper buys for a company.
	Company *CompanyAccess `json:"-"`
	// PONumber is the buyer's purchase order number (B2B).
	PONumber string `json:"poNumber,omitempty"`
	// Renewal is set by the subscription worker for the deliveries after the first.
	Renewal *Renewal `json:"-"`
	// ExpectedTotal is the grand total the shopper saw (minor units). When sent,
	// the order is only placed if the total is still the same.
	ExpectedTotal *int64 `json:"expectedTotal,omitempty"`
	// ShippingOption is the delivery option chosen by the shopper (empty = cheapest).
	ShippingOption string `json:"shippingOption,omitempty"`
	// AcceptedTerms must be true when the store asks to accept its terms.
	AcceptedTerms bool `json:"acceptedTerms,omitempty"`
}

// ShippingMethod is the delivery option stored with the order.
type ShippingMethod struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	Carrier       string              `json:"carrier,omitempty"`
	CarrierID     string              `json:"carrierId,omitempty"`
	Zone          string              `json:"zone,omitempty"`
	LocationID    string              `json:"locationId,omitempty"`
	LocationName  string              `json:"locationName,omitempty"`
	PickupAddress string              `json:"pickupAddress,omitempty"`
	Instructions  string              `json:"instructions,omitempty"`
	Price         int64               `json:"price"`
	PriceReason   string              `json:"priceReason,omitempty"`
	Reason        string              `json:"reason,omitempty"`
	Estimate      *logistics.Estimate `json:"estimate,omitempty"`
	WeightGrams   int64               `json:"weightGrams"`
}

// Line is an order line. Fulfillable units = Qty - FulfilledQty - CancelledQty.
type Line struct {
	Line         int    `json:"line"`
	SKU          string `json:"sku"`
	Title        string `json:"title"`
	Qty          int64  `json:"qty"`
	Unit         int64  `json:"unit"`
	Total        int64  `json:"total"`
	FulfilledQty int64  `json:"fulfilledQty"`
	ReturnedQty  int64  `json:"returnedQty"`
	RefundedQty  int64  `json:"refundedQty"`
	CancelledQty int64  `json:"cancelledQty"`
	Origin       string `json:"origin,omitempty"`
}

// Event is one entry in the order history.
type Event struct {
	Seq   int             `json:"seq"`
	At    time.Time       `json:"at"`
	Actor string          `json:"actor"`
	Type  string          `json:"type"`
	From  *string         `json:"from"`
	To    string          `json:"to"`
	Data  json.RawMessage `json:"data"`
}

// Order is the API view of an order.
type Order struct {
	ID          string    `json:"id"`
	Number      int64     `json:"number"`
	Channel     string    `json:"channel"`
	Status      string    `json:"status"`
	Currency    string    `json:"currency"`
	ItemsTotal  int64     `json:"itemsTotal"`
	Discounts   int64     `json:"discounts"`
	Shipping    int64     `json:"shipping"`
	TaxIncluded int64     `json:"taxIncluded"`
	GrandTotal  int64     `json:"grandTotal"`
	Customer    *Customer `json:"customer,omitempty"`
	Lines       []Line    `json:"lines"`
	Events      []Event   `json:"events,omitempty"`
	Payment     *Payment  `json:"payment,omitempty"`
	Invoice     *Invoice  `json:"invoice,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	// Promotions and coupons this order used, with their state (reserved, redeemed, released).
	Promotions []promostore.Redemption `json:"promotions,omitempty"`

	Risk         *Risk `json:"risk,omitempty"`         // set when fraud rules flagged the order
	Installments int   `json:"installments,omitempty"` // meses sin intereses chosen at checkout

	// After-sale state (see depth.go).
	CustomerID        string        `json:"customerId,omitempty"`
	CustomerName      string        `json:"customerName,omitempty"`
	CustomerEmail     string        `json:"customerEmail,omitempty"`
	PaymentStatus     string        `json:"paymentStatus"`
	FulfillmentStatus string        `json:"fulfillmentStatus"`
	RefundedTotal     int64         `json:"refundedTotal"`
	PaidTotal         int64         `json:"paidTotal"` // money collected minus money refunded
	Balance           int64         `json:"balance"`   // > 0: the customer owes; < 0: the store owes
	Source            string        `json:"source,omitempty"`
	ItemsCount        int64         `json:"itemsCount,omitempty"`
	Payments          []Payment     `json:"payments,omitempty"`
	Fulfillments      []Fulfillment `json:"fulfillments,omitempty"`
	Refunds           []Refund      `json:"refunds,omitempty"`
	Returns           []Return      `json:"returns,omitempty"`
	Notes             []Note        `json:"notes,omitempty"`
	Edits             []Edit        `json:"edits,omitempty"`
	// DisplayNumber is what the shopper sees: store prefix + number.
	DisplayNumber  string               `json:"displayNumber"`
	PONumber       string               `json:"poNumber,omitempty"`
	Fees           int64                `json:"fees"` // extra charges and import duties, in the total
	PostalCode     string               `json:"postalCode,omitempty"`
	Voucher        *payments.Voucher    `json:"voucher,omitempty"`  // cash or transfer still to be paid
	FeeLines       []cart.Fee           `json:"feeLines,omitempty"` // each one with its reason
	ShippingMethod *ShippingMethod      `json:"shippingMethod,omitempty"`
	TaxDetail      *logistics.TaxResult `json:"taxDetail,omitempty"`
}

// Payment is the API view of a payment.
type Payment struct {
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Refunded int64  `json:"refunded"`
}

// Invoice is the API view of an invoice.
type Invoice struct {
	Status   string `json:"status"`
	URL      string `json:"url,omitempty"`
	Attempts int    `json:"attempts"`
	Error    string `json:"lastError,omitempty"`
}

// Account is the resolved payment account of a store.
type Account struct {
	Provider payments.Provider
	Account  string // acct_ for Connect, "" for own keys
	FeeBps   int64
}

// Service implements the order lifecycle.
type Service struct {
	DB         *store.DB
	Keys       *crypto.Keyring
	Projector  *projection.Projector
	Accounts   func(ctx context.Context, tenant string) (Account, error)
	Notify     *notify.Service // optional: transactional emails
	PublicURL  string          // origin for links in emails
	ReserveTTL time.Duration
	Now        func() time.Time
	Log        *slog.Logger // optional
}

func (s *Service) ttl() time.Duration {
	if s.ReserveTTL > 0 {
		return s.ReserveTTL
	}
	return 15 * time.Minute
}

func newID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Result is what Checkout returns; Status is the HTTP status to replay.
type Result struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// staleKey is how old an "in progress" idempotency key must be to be treated
// as interrupted. Requests are cut at 15 seconds.
const staleKey = 45 * time.Second

// Checkout runs the whole purchase. The same idempotency key always returns
// the same result and never charges twice.
func (s *Service) Checkout(ctx context.Context, tenant, actor, idemKey string, req CheckoutRequest) (Result, error) {
	reqBytes, _ := json.Marshal(req)
	h := sha256.Sum256(reqBytes)

	// 1. Idempotency gate.
	var prior *Result
	var recovered string // order of an interrupted attempt that did get paid
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var hash []byte
		var status int
		var body []byte
		err := tx.QueryRow(ctx, `SELECT request_hash, status, response FROM idempotency_keys WHERE tenant_id=$1 AND key=$2`, tenant, idemKey).
			Scan(&hash, &status, &body)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO idempotency_keys (tenant_id, key, request_hash, status, response) VALUES ($1,$2,$3,0,'{}')`, tenant, idemKey, h[:])
			return err
		}
		if err != nil {
			return err
		}
		if string(hash) != string(h[:]) {
			return ErrIdempotencyReuse
		}
		if status == 0 {
			// Still running, or the process died mid-purchase. A request never
			// lasts this long, so an old "in progress" key is an interrupted one.
			var age time.Duration
			var orderID *string
			if err := tx.QueryRow(ctx, `SELECT now() - created_at, order_id FROM idempotency_keys WHERE tenant_id=$1 AND key=$2 FOR UPDATE`, tenant, idemKey).Scan(&age, &orderID); err != nil {
				return err
			}
			if age < staleKey {
				return ErrInProgress
			}
			if orderID != nil {
				var ost string
				if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, *orderID).Scan(&ost); err != nil {
					return err
				}
				switch ost {
				case StatusPendingPayment:
					return ErrInProgress // the reconciler is about to settle it with the provider
				case StatusCancelled:
					// The interrupted attempt ended without a charge: this retry starts over.
				default:
					recovered = *orderID // it was paid: answer with that order, never buy twice
					return nil
				}
			}
			_, err := tx.Exec(ctx, `UPDATE idempotency_keys SET created_at=now(), order_id=NULL WHERE tenant_id=$1 AND key=$2`, tenant, idemKey)
			return err
		}
		prior = &Result{Status: status, Body: body}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if prior != nil {
		return *prior, nil
	}
	if recovered != "" {
		o, err := s.Get(ctx, tenant, recovered, false)
		if err != nil {
			return Result{}, err
		}
		b, _ := json.Marshal(o)
		res := Result{Status: 201, Body: b}
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `UPDATE idempotency_keys SET status=$3, response=$4 WHERE tenant_id=$1 AND key=$2`, tenant, idemKey, res.Status, []byte(res.Body))
			return e
		})
		return res, nil
	}

	res, err := s.checkout(ctx, tenant, actor, idemKey, req)
	if err != nil {
		// Unexpected failure: free the key so the client can retry safely.
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id=$1 AND key=$2 AND status=0`, tenant, idemKey)
			return e
		})
		return Result{}, err
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE idempotency_keys SET status=$3, response=$4 WHERE tenant_id=$1 AND key=$2`, tenant, idemKey, res.Status, []byte(res.Body))
		return e
	})
	return res, err
}

func problem(status int, code, detail string, extra map[string]any) Result {
	m := map[string]any{"type": "https://docs.apexx.dev/errors/" + code, "title": code, "status": status, "code": code, "detail": detail}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return Result{Status: status, Body: b}
}

func (s *Service) checkout(ctx context.Context, tenant, actor, idemKey string, req CheckoutRequest) (Result, error) {
	t, ok := s.Projector.Tenant(tenant)
	if !ok {
		return Result{}, fmt.Errorf("orders: tenant %s not loaded", tenant)
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil && !errors.Is(err, ErrNoPaymentAccount) {
		return Result{}, err // infrastructure: the key is freed and the shopper can retry
	}
	if err != nil {
		return problem(409, "payment_account_missing", ErrNoPaymentAccount.Error(), nil), nil
	}
	// Checkout rules of the store (Configuración → Checkout).
	rules := t.Site().Settings
	var fieldErrs []map[string]string
	if req.Renewal != nil {
		rules.Checkout.PhoneRequired, rules.Checkout.TermsCheckbox = false, false // accepted when the subscription started
	}
	if rules.Checkout.PhoneRequired && len(strings.TrimSpace(req.Customer.Phone)) < 7 {
		fieldErrs = append(fieldErrs, map[string]string{"field": "customer.phone", "code": "required", "message": "Escribe un teléfono de contacto"})
	}
	if rules.Checkout.TermsCheckbox && !req.AcceptedTerms {
		fieldErrs = append(fieldErrs, map[string]string{"field": "acceptedTerms", "code": "required", "message": "Acepta los términos y condiciones para continuar"})
	}
	if len(req.Customer.Notes) > 500 {
		fieldErrs = append(fieldErrs, map[string]string{"field": "customer.notes", "code": "too_long", "message": "La nota admite hasta 500 caracteres"})
	}
	if len(fieldErrs) > 0 {
		return problem(400, "validation_failed", fieldErrs[0]["message"], map[string]any{"errors": fieldErrs}), nil
	}
	req.PONumber = strings.TrimSpace(req.PONumber)
	if len(req.PONumber) > 60 {
		fieldErrs = append(fieldErrs, map[string]string{"field": "poNumber", "code": "too_long", "message": "La orden de compra admite hasta 60 caracteres"})
	}
	if req.Company != nil && req.Company.RequirePO && req.PONumber == "" && req.Renewal == nil {
		fieldErrs = append(fieldErrs, map[string]string{"field": "poNumber", "code": "required", "message": "Escribe el número de orden de compra de tu empresa"})
	}
	if len(fieldErrs) > 0 {
		return problem(400, "validation_failed", fieldErrs[0]["message"], map[string]any{"errors": fieldErrs}), nil
	}
	if req.Company != nil && req.Company.CreditOnly && req.PaymentMethod != "credit" {
		return problem(409, "credit_only", "Tu empresa compra a crédito; elige pagar a crédito", nil), nil
	}
	if !rules.Checkout.NotesField {
		req.Customer.Notes = ""
	}
	// The customer is identified by a keyed digest of the email, so per-customer
	// limits work without storing the address in clear.
	custHash, err := s.Keys.BlindIndex(tenant, strings.ToLower(strings.TrimSpace(req.Customer.Email)))
	if err != nil {
		return Result{}, err
	}
	ps := &promostore.Store{DB: s.DB}
	lookup, err := ps.Lookup(ctx, tenant, req.Coupons, custHash)
	if err != nil {
		return Result{}, err
	}
	sim, err := t.Simulate(cart.Request{Channel: req.Channel, PostalCode: req.PostalCode, Items: req.Items, ShippingOption: req.ShippingOption,
		Coupons: req.Coupons, Email: req.Customer.Email, PaymentMethod: "card", Lookup: lookup, Extra: CompanyPromo(req.Company), Group: CompanyGroup(req.Company), RestrictGroup: req.Company != nil && req.Company.CatalogRestricted, TaxExempt: req.Company != nil && req.Company.TaxExempt})
	if err != nil {
		return problem(400, "invalid_request", err.Error(), nil), nil
	}
	// A coupon that does not apply stops the purchase with its exact reason:
	// the shopper never pays a total different from the one they expected.
	for _, c := range sim.Coupons {
		if !c.Applied {
			return problem(409, "coupon_invalid", c.Reason, map[string]any{"coupon": c, "simulation": sim}), nil
		}
	}
	var redemptions []promostore.RedemptionReq
	for _, r := range sim.Promotions {
		if !r.Applied {
			continue
		}
		amt := r.Discount.Amount
		if r.ShippingDiscount != nil {
			amt += r.ShippingDiscount.Amount
		}
		if r.ID == "subscription" || r.ID == "company" {
			continue // rules of the store (subscription, company price), not stored promotions with limits
		}
		redemptions = append(redemptions, promostore.RedemptionReq{PromotionID: r.ID, Name: r.Name, Coupon: r.Coupon, CampaignID: r.CampaignID, Amount: amt})
	}
	// Every requested unit must be allocated; otherwise show the simulation back.
	for i, it := range req.Items {
		if i >= len(sim.Lines) || sim.Lines[i].SKU != it.SKU || sim.Lines[i].Qty != it.Qty {
			return problem(409, "cart_changed", ErrCartChanged.Error(), map[string]any{"simulation": sim}), nil
		}
	}
	if req.ExpectedTotal != nil && sim.Shipping != nil && *req.ExpectedTotal != sim.Totals.Grand.Amount {
		return problem(409, "total_changed", "El total de tu compra cambió (una promoción o un precio se actualizó); revisa el resumen antes de pagar",
			map[string]any{"simulation": sim}), nil
	}
	if sim.Shipping == nil {
		for _, m := range sim.Messages {
			if m.Code == "no_shipping_option" {
				return problem(409, "no_shipping_option", m.Text, map[string]any{"simulation": sim}), nil
			}
		}
		return problem(400, "postal_code_required", "Se necesita un código postal válido para calcular el envío", nil), nil
	}
	if req.ShippingOption != "" && sim.Shipping.ID != req.ShippingOption {
		return problem(409, "shipping_option_unavailable", "La opción de entrega que elegiste ya no está disponible; revisa las opciones", map[string]any{"simulation": sim}), nil
	}
	method := ShippingMethod{ID: sim.Shipping.ID, Name: sim.Shipping.Name, Kind: sim.Shipping.Kind, Carrier: sim.Shipping.Carrier,
		Price: sim.Shipping.Price.Amount, PriceReason: sim.Shipping.PriceReason, Estimate: sim.Shipping.Estimate, WeightGrams: sim.Shipping.WeightGrams}
	if q := sim.Shipping.Quote; q != nil {
		method.CarrierID, method.Zone, method.Reason = q.CarrierID, q.Zone, q.Reason
		method.LocationID, method.LocationName, method.PickupAddress, method.Instructions = q.LocationID, q.LocationName, q.PickupAddress, q.Instructions
	}
	methodJSON, _ := json.Marshal(method)
	taxJSON := []byte("{}")
	if sim.Tax != nil {
		taxJSON, _ = json.Marshal(sim.Tax)
	}
	cust, _ := json.Marshal(req.Customer)
	id := newID("ord_")
	enc, err := s.Keys.Encrypt(tenant, cust, id)
	if err != nil {
		return Result{}, err
	}
	tot := sim.Totals
	var number, wallet, credit int64

	// Subscribed lines (Simulate cleared the plans the store does not offer).
	subscribed := false
	for _, it := range req.Items {
		subscribed = subscribed || it.Plan != ""
	}
	if subscribed && len(req.GiftCards) > 0 {
		return problem(409, "gift_card_not_with_subscription", "Una suscripción se paga con tarjeta bancaria, para poder cobrar las siguientes entregas. Quita la tarjeta de regalo o compra una sola vez.", nil), nil
	}
	if subscribed && req.Installments > 1 {
		return problem(409, "installments_unavailable", "Las suscripciones no se pagan a meses sin intereses", nil), nil
	}
	if kind, ok := payments.VoucherMethods[req.PaymentMethod]; ok {
		pm, err := s.PaymentMethods(ctx, tenant)
		if err != nil {
			return Result{}, err
		}
		switch {
		case !pm.Allows(req.PaymentMethod):
			return problem(409, "payment_method_unavailable", kind+" no está disponible en esta tienda", nil), nil
		case string(tot.Grand.Currency) != "MXN":
			return problem(409, "payment_method_unavailable", kind+" solo acepta pesos mexicanos", nil), nil
		case subscribed:
			return problem(409, "payment_method_unavailable", "Las suscripciones se pagan con tarjeta", nil), nil
		case req.Installments > 1:
			return problem(409, "installments_unavailable", "Los meses sin intereses son solo con tarjeta", nil), nil
		case req.PaymentMethod == "oxxo" && tot.Grand.Amount > 10_000_00:
			return problem(409, "payment_method_unavailable", "En OXXO se pagan pedidos de hasta $10,000; elige tarjeta o transferencia", nil), nil
		}
	}

	// Store rules: instalments offered and fraud checks.
	instRules, riskRules, err := s.PaymentRules(ctx, tenant)
	if err != nil {
		return Result{}, err
	}
	if !instRules.Allows(req.Installments, tot.Grand.Amount) {
		return problem(409, "installments_unavailable", "Ese plazo de meses sin intereses no está disponible para este pedido", nil), nil
	}
	if level, why := approvalNeeded(req, tot.Grand.Amount, sim.Lines); level > 0 {
		if err := s.requestApproval(ctx, tenant, req, sim.Lines, tot.Grand.Amount, string(tot.Grand.Currency), level, why); err != nil {
			var ce *CreditError
			if errors.As(err, &ce) {
				return problem(409, ce.Code, ce.Message, nil), nil
			}
			return Result{}, err
		}
		return problem(202, "approval_required", why+". Enviamos la solicitud a quien aprueba en tu empresa; te avisaremos por correo cuando se confirme el pedido.", map[string]any{"level": level}), nil
	}
	risk, ipHash, blocked, err := s.evaluateRisk(ctx, tenant, riskRules, req, tot.Grand.Amount)
	if err != nil {
		return Result{}, err
	}
	if blocked != "" {
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			return store.Audit(ctx, tx, tenant, "risk", "order.blocked", req.PostalCode, map[string]any{"reason": blocked, "total": tot.Grand.Amount})
		})
		return problem(403, "order_blocked", "No pudimos procesar tu pedido. Escríbenos y con gusto te ayudamos.", nil), nil
	}

	// 2. Reserve stock and create the order atomically.
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT nextval('order_numbers')`).Scan(&number); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO orders (tenant_id, id, number, channel, status, currency, items_total, discounts, shipping, tax_included, grand_total, customer, postal_code,
				shipping_method, tax_detail, display_number, tax_adjust, po_number, fees, fee_lines)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
			tenant, id, number, req.Channel, StatusPendingPayment, string(tot.Grand.Currency), tot.Items.Amount, tot.Discounts.Amount,
			tot.Shipping.Amount, tot.Tax.Amount, tot.Grand.Amount, enc, req.PostalCode, methodJSON, taxJSON, rules.OrderNumber(number), tot.TaxAdjust.Amount, req.PONumber,
			tot.Fees.Amount, feeJSON(sim.Fees)); err != nil {
			return err
		}
		if err := s.linkCustomerTx(ctx, tx, tenant, id, req.Customer, req.PostalCode); err != nil {
			return err
		}
		// The key remembers its order, so a retry after a crash finds it.
		if _, err := tx.Exec(ctx, `UPDATE idempotency_keys SET order_id=$3 WHERE tenant_id=$1 AND key=$2`, tenant, idemKey, id); err != nil {
			return err
		}
		for i, l := range sim.Lines {
			if _, err := tx.Exec(ctx, `INSERT INTO order_lines (tenant_id, order_id, line, sku, title, qty, unit, total) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				tenant, id, i+1, l.SKU, l.Title, l.Qty, l.Unit.Amount, l.Total.Amount); err != nil {
				return err
			}
			if err := store.ReserveTx(ctx, tx, tenant, id, l.SKU, l.Qty, s.ttl()); err != nil {
				return err
			}
		}
		// Promotion and coupon uses are taken in this same transaction: limits are
		// re-checked under row locks, so two checkouts cannot share the last use.
		if err := promostore.ReserveTx(ctx, tx, tenant, id, custHash, string(tot.Grand.Currency), redemptions); err != nil {
			return err
		}
		if wallet, err = s.redeemWalletTx(ctx, tx, tenant, actor, id, req.GiftCards, tot.Grand.Amount, string(tot.Grand.Currency)); err != nil {
			return err
		}
		if req.PaymentMethod == "credit" {
			credit = tot.Grand.Amount - wallet
			if credit > 0 {
				if err := creditChargeTx(ctx, tx, tenant, actor, id, req.Company, credit); err != nil {
					return err
				}
			}
		}
		if err := s.afterCreateTx(ctx, tx, tenant, id, req, risk, ipHash); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, id, actor, "order.created", nil, StatusPendingPayment, map[string]any{"promotions": sim.Promotions, "coupons": sim.Coupons, "shippingOption": method.ID, "acceptedTerms": req.AcceptedTerms})
	})
	if errors.Is(err, store.ErrInsufficient) {
		return problem(409, "insufficient_stock", err.Error(), nil), nil
	}
	var cre *CreditError
	if errors.As(err, &cre) {
		return problem(409, cre.Code, cre.Message, nil), nil
	}
	var gce *GiftCardError
	if errors.As(err, &gce) {
		return problem(409, gce.Code, gce.Message, nil), nil
	}
	var lim *promostore.LimitError
	if errors.As(err, &lim) {
		return problem(409, "promotion_unavailable", lim.Reason+" Revisa el total antes de pagar.",
			map[string]any{"promotionId": lim.PromotionID, "coupon": lim.Coupon, "reasonCode": lim.Code}), nil
	}
	if err != nil {
		return Result{}, err
	}

	// 3. Charge what gift cards did not cover. The idempotency key is the order
	// id, so a retry never charges twice.
	due := tot.Grand.Amount - wallet - credit
	ch := payments.Charge{Status: "captured", Ref: "no_card"}
	if due > 0 {
		var payErr error
		pmToken, cusToken := req.PaymentMethod, renewalCustomer(req.Renewal)
		if req.Saved != nil {
			pmToken, cusToken = req.Saved.PaymentMethod, req.Saved.Customer // a card the customer kept, used with them present
		}
		ch, payErr = acct.Provider.Charge(ctx, payments.ChargeRequest{
			Account: acct.Account, OrderID: id, Amount: due, Currency: string(tot.Grand.Currency),
			PaymentMethod: pmToken, FeeAmount: due * acct.FeeBps / 10000, IdempotencyKey: "charge-" + id, Email: req.Customer.Email,
			Installments: req.Installments, SaveCard: (subscribed || (req.SaveCard && req.SaveFor != "")) && req.Renewal == nil && req.Saved == nil, Name: req.Customer.Name,
			Customer: cusToken, OffSession: req.Renewal != nil,
		})
		if payErr != nil {
			// Unknown outcome (network, Stripe outage): leave the order pending; the
			// reconciler resolves it with Lookup. Reservations expire meanwhile.
			return problem(502, "payment_pending", "No se pudo confirmar el pago; el pedido quedó pendiente y se resolverá automáticamente", map[string]any{"orderId": id}), nil
		}
	}
	if err := s.settle(ctx, tenant, actor, id, acct, ch); err != nil {
		return Result{}, err
	}
	o, err := s.Get(ctx, tenant, id, false)
	if err != nil {
		return Result{}, err
	}
	if ch.Status == "captured" && req.SaveCard && req.SaveFor != "" && ch.Customer != "" && ch.PaymentMethod != "" {
		if err := s.keepCard(ctx, tenant, req.SaveFor, acct, ch); err != nil && s.Log != nil {
			s.Log.Error("save card", "tenant", tenant, "order", id, "err", err) // the order stands
		}
	}
	if ch.Status == "pending" && ch.Voucher != nil {
		b, _ := json.Marshal(o)
		return Result{Status: 201, Body: b}, nil // order placed, paid when the voucher is paid
	}
	if ch.Status != "captured" {
		return problem(402, "payment_declined", "El pago fue rechazado: "+ch.Reason, map[string]any{"order": o}), nil
	}
	if subscribed && req.Renewal == nil {
		if err := s.createSubscriptions(ctx, tenant, id, req, sim.Lines, t.Subscriptions().DiscountBps, method.ID, ch); err != nil && s.Log != nil {
			// The order is paid and stands; the store can see the failure and re-create the plan.
			s.Log.Error("create subscription", "tenant", tenant, "order", id, "err", err)
		}
	}
	b, _ := json.Marshal(o)
	return Result{Status: 201, Body: b}, nil
}

// settle finishes a pending order according to the charge outcome.
func (s *Service) settle(ctx context.Context, tenant, actor, id string, acct Account, ch payments.Charge) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var status, cur string
		var grand, tax int64
		if err := tx.QueryRow(ctx, `SELECT status, currency, grand_total, tax_included FROM orders WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).
			Scan(&status, &cur, &grand, &tax); err != nil {
			return err
		}
		if status != StatusPendingPayment {
			return nil // already settled (reconciler and request raced)
		}
		if ch.Status == "pending" && ch.Voucher != nil {
			// Cash or transfer: the order waits for the money with its stock
			// kept until the voucher expires (plus an hour of grace).
			raw, _ := json.Marshal(ch.Voucher)
			ct, err := tx.Exec(ctx, `UPDATE orders SET voucher=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND voucher IS NULL`, tenant, id, raw)
			if err != nil || ct.RowsAffected() == 0 {
				return err // already recorded: nothing new until it is paid
			}
			if _, err := tx.Exec(ctx, `UPDATE reservations SET expires_at=$3 WHERE tenant_id=$1 AND holder=$2`, tenant, id, ch.Voucher.ExpiresAt.Add(time.Hour)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO payments (tenant_id, id, order_id, provider, provider_ref, status, amount, currency)
				VALUES ($1,$2,$3,$4,$5,'pending',$6,$7)`, tenant, newID("pay_"), id, acct.Provider.Name(), ch.Ref, grand, cur); err != nil {
				return err
			}
			return s.event(ctx, tx, tenant, id, actor, "payment.voucher_issued", &status, StatusPendingPayment, map[string]any{"kind": ch.Voucher.Kind, "expiresAt": ch.Voucher.ExpiresAt})
		}
		if ch.Status == "pending" || ch.Status == "requires_action" {
			return nil // still waiting
		}
		// The voucher row (if any) is replaced by the final payment.
		if _, err := tx.Exec(ctx, `DELETE FROM payments WHERE tenant_id=$1 AND order_id=$2 AND status='pending'`, tenant, id); err != nil {
			return err
		}
		pstatus := "failed"
		if ch.Status == "captured" {
			pstatus = "captured"
		}
		// Gift cards already took their part when the order was created.
		cards, wallet, err := walletPartsTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		onCredit, company, err := creditPartTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		due := grand - wallet - onCredit
		if due > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO payments (tenant_id, id, order_id, provider, provider_ref, status, amount, currency)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, newID("pay_"), id, acct.Provider.Name(), ch.Ref, pstatus, due, cur); err != nil {
				return err
			}
		}
		if ch.Status != "captured" {
			if err := releaseWalletTx(ctx, tx, tenant, actor, id); err != nil {
				return err
			}
			if err := releaseCreditTx(ctx, tx, tenant, actor, id); err != nil {
				return err
			}
			if err := store.ReleaseTx(ctx, tx, tenant, id); err != nil {
				return err
			}
			if err := promostore.ReleaseTx(ctx, tx, tenant, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, StatusCancelled); err != nil {
				return err
			}
			return s.event(ctx, tx, tenant, id, actor, "payment.failed", &status, StatusCancelled, map[string]any{"reason": ch.Reason})
		}
		// Record where units came from, then consume the reservation.
		if _, err := tx.Exec(ctx, `INSERT INTO order_allocations (tenant_id, order_id, sku, location_id, qty)
			SELECT tenant_id, holder, sku, location_id, sum(qty) FROM reservations WHERE tenant_id=$1 AND holder=$2
			GROUP BY tenant_id, holder, sku, location_id`, tenant, id); err != nil {
			return err
		}
		if err := store.CommitTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if err := promostore.ConfirmTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, StatusPaid); err != nil {
			return err
		}
		// Double entry: cash at Stripe vs sales and tax payable.
		txID := "pay-" + id
		for _, c := range cards {
			if _, err := tx.Exec(ctx, `INSERT INTO payments (tenant_id, id, order_id, provider, provider_ref, status, amount, currency)
				VALUES ($1,$2,$3,'gift_card',$4,'captured',$5,$6)`, tenant, newID("pay_"), id, c.CardID, c.Amount, cur); err != nil {
				return err
			}
		}
		if onCredit > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO payments (tenant_id, id, order_id, provider, provider_ref, status, amount, currency)
				VALUES ($1,$2,$3,'company_credit',$4,'captured',$5,$6)`, tenant, newID("pay_"), id, company, onCredit, cur); err != nil {
				return err
			}
		}
		entries := map[string]int64{"sales": -(grand - tax), "tax_payable": -tax}
		entries[clearing(acct.Provider.Name())] += due
		entries[clearing("gift_card")] += wallet
		entries[clearing("company_credit")] += onCredit
		if err := ledger(ctx, tx, tenant, txID, id, cur, entries); err != nil {
			return err
		}
		if err := s.afterPaidTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO invoices (tenant_id, order_id, provider, status) VALUES ($1,$2,$3,'pending')`, tenant, id, acct.Provider.Name()); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, id, actor, "payment.captured", &status, StatusPaid, map[string]any{"ref": ch.Ref})
	})
}

func ledger(ctx context.Context, tx pgx.Tx, tenant, txID, orderID, cur string, entries map[string]int64) error {
	var sum int64
	for _, v := range entries {
		sum += v
	}
	if sum != 0 {
		return fmt.Errorf("ledger: entries of %s do not balance (%d)", txID, sum)
	}
	for acct, amt := range entries {
		if amt == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (tenant_id, tx_id, account, amount, currency, order_id) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenant, txID, acct, amt, cur, orderID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) event(ctx context.Context, tx pgx.Tx, tenant, id, actor, typ string, from *string, to string, data any) error {
	b, _ := json.Marshal(data)
	if _, err := tx.Exec(ctx, `INSERT INTO order_events (tenant_id, order_id, seq, actor, type, from_status, to_status, data)
		VALUES ($1,$2,(SELECT COALESCE(max(seq),0)+1 FROM order_events WHERE tenant_id=$1 AND order_id=$2),$3,$4,$5,$6,$7)`,
		tenant, id, actor, typ, from, to, b); err != nil {
		return err
	}
	if err := store.Emit(ctx, tx, tenant, store.TopicOrder, id, map[string]any{"type": typ, "status": to}); err != nil {
		return err
	}
	if err := store.Audit(ctx, tx, tenant, actor, typ, id, map[string]any{"to": to}); err != nil {
		return err
	}
	return deriveTx(ctx, tx, tenant, id)
}

// Fulfill ships every unit still pending in one shipment without tracking
// (kept for the simple "mark as shipped" action and API compatibility).
func (s *Service) Fulfill(ctx context.Context, tenant, actor, id string) error {
	_, err := s.CreateFulfillment(ctx, tenant, actor, id, FulfillmentInput{})
	return err
}

func (s *Service) transition(ctx context.Context, tenant, actor, id, from, to, typ string, data any) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != from {
			return fmt.Errorf("%w: %s → %s", ErrInvalidState, status, to)
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, to); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, id, actor, typ, &status, to, data)
	})
}

// List returns orders without personal data, newest first. before (order
// number, 0 = newest) pages backwards; status filters when not empty.
func (s *Service) List(ctx context.Context, tenant string, limit int, before int64, status string) ([]Order, error) {
	var out []Order
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, number, channel, status, currency, grand_total, created_at, COALESCE(NULLIF(display_number, ''), number::text) FROM orders
			WHERE tenant_id=$1 AND ($3 = 0 OR number < $3) AND ($4 = '' OR status = $4) ORDER BY number DESC LIMIT $2`, tenant, limit, before, status)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) {
			var o Order
			return o, r.Scan(&o.ID, &o.Number, &o.Channel, &o.Status, &o.Currency, &o.GrandTotal, &o.CreatedAt, &o.DisplayNumber)
		})
		return err
	})
	return out, err
}

// Reconcile resolves orders stuck in pending_payment for longer than after:
// it asks the provider whether the charge happened and settles accordingly.
func (s *Service) Reconcile(ctx context.Context, after time.Duration) (int, error) {
	// Orders waiting for a cash or transfer voucher are resolved by the
	// provider's webhook; they are checked here only once the voucher expired.
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id FROM orders WHERE status=$1 AND created_at < now() - $2::interval
		AND (voucher IS NULL OR (voucher->>'expiresAt')::timestamptz < now())`,
		StatusPendingPayment, fmt.Sprintf("%d seconds", int(after.Seconds())))
	if err != nil {
		return 0, err
	}
	type ti struct{ t, id string }
	var list []ti
	for rows.Next() {
		var x ti
		if err := rows.Scan(&x.t, &x.id); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	n := 0
	for _, x := range list {
		acct, err := s.Accounts(ctx, x.t)
		if err != nil {
			continue
		}
		ch, found, err := acct.Provider.Lookup(ctx, acct.Account, x.id)
		if err != nil {
			continue // provider unreachable; try again next round
		}
		if !found {
			ch = payments.Charge{Status: "failed", Reason: "no_charge_found"}
		}
		if err := s.settle(ctx, x.t, "reconciler", x.id, acct, ch); err == nil {
			n++
		}
	}
	return n, nil
}

// IssueInvoices sends pending invoices to the provider with exponential backoff.
func (s *Service) IssueInvoices(ctx context.Context) (int, error) {
	// Claim with a lease so several nodes never issue the same invoice at once.
	rows, err := s.DB.System.Query(ctx, `WITH due AS (
			SELECT tenant_id, order_id, provider FROM invoices
			WHERE status IN ('pending','failed') AND attempts < 10 AND next_attempt <= now() LIMIT 50 FOR UPDATE SKIP LOCKED)
		UPDATE invoices i SET next_attempt = now() + interval '5 minutes'
		FROM due WHERE i.tenant_id=due.tenant_id AND i.order_id=due.order_id AND i.provider=due.provider
		RETURNING i.tenant_id, i.order_id`)
	if err != nil {
		return 0, err
	}
	type ti struct{ t, id string }
	var list []ti
	for rows.Next() {
		var x ti
		if err := rows.Scan(&x.t, &x.id); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	n := 0
	for _, x := range list {
		o, err := s.Get(ctx, x.t, x.id, true)
		if err != nil {
			continue
		}
		acct, err := s.Accounts(ctx, x.t)
		if err != nil {
			continue
		}
		req := payments.InvoiceRequest{Account: acct.Account, OrderID: o.ID, Currency: o.Currency, Email: o.Customer.Email,
			Name: o.Customer.Name, Shipping: o.Shipping, Discount: o.Discounts, IdempotencyKey: "invoice-" + o.ID}
		for _, l := range o.Lines {
			req.Lines = append(req.Lines, payments.InvoiceLine{Description: l.Title, Qty: l.Qty, UnitAmount: l.Unit})
		}
		inv, ierr := acct.Provider.Invoice(ctx, req)
		_ = s.DB.Tx(ctx, x.t, func(tx pgx.Tx) error {
			if ierr != nil {
				_, e := tx.Exec(ctx, `UPDATE invoices SET status='failed', attempts=attempts+1, last_error=$3,
					next_attempt=now() + (interval '30 seconds' * power(2, attempts)), updated_at=now() WHERE tenant_id=$1 AND order_id=$2`, x.t, x.id, ierr.Error())
				return e
			}
			if _, e := tx.Exec(ctx, `UPDATE invoices SET status='issued', attempts=attempts+1, provider_ref=$3, url=$4, last_error='', updated_at=now() WHERE tenant_id=$1 AND order_id=$2`, x.t, x.id, inv.Ref, inv.URL); e != nil {
				return e
			}
			var status string
			if e := tx.QueryRow(ctx, `SELECT status FROM orders WHERE tenant_id=$1 AND id=$2`, x.t, x.id).Scan(&status); e != nil {
				return e
			}
			n++
			if e := s.notifyTx(ctx, tx, x.t, x.id, notify.TplInvoiceReady, map[string]string{"factura.url": inv.URL}, inv.URL, "invoice:"+x.id); e != nil {
				return e
			}
			return s.event(ctx, tx, x.t, x.id, "invoicer", "invoice.issued", &status, status, map[string]any{"ref": inv.Ref, "url": inv.URL})
		})
	}
	return n, nil
}

// ResolvePending settles one pending order from the provider's view (used by
// the Stripe webhook so a lost HTTP response never leaves an order hanging).
func (s *Service) ResolvePending(ctx context.Context, tenant, id string) error {
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return err
	}
	ch, found, err := acct.Provider.Lookup(ctx, acct.Account, id)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return s.settle(ctx, tenant, "stripe-webhook", id, acct, ch)
}

func renewalCustomer(r *Renewal) string {
	if r == nil {
		return ""
	}
	return r.PaymentCustomer
}

// feeJSON is the extra charges of an order as stored ([] when none).
func feeJSON(f []cart.Fee) []byte {
	if len(f) == 0 {
		return []byte("[]")
	}
	b, _ := json.Marshal(f)
	return b
}
