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
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/projection"
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
}

// CheckoutRequest places an order.
type CheckoutRequest struct {
	Channel       string      `json:"channel"`
	PostalCode    string      `json:"postalCode"`
	Items         []cart.Item `json:"items"`
	Customer      Customer    `json:"customer"`
	PaymentMethod string      `json:"paymentMethod"` // pm_... from Stripe.js
}

// Line is an order line.
type Line struct {
	SKU   string `json:"sku"`
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
	Unit  int64  `json:"unit"`
	Total int64  `json:"total"`
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
}

// Payment is the API view of a payment.
type Payment struct {
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
	ReserveTTL time.Duration
	Now        func() time.Time
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

// Checkout runs the whole purchase. The same idempotency key always returns
// the same result and never charges twice.
func (s *Service) Checkout(ctx context.Context, tenant, actor, idemKey string, req CheckoutRequest) (Result, error) {
	reqBytes, _ := json.Marshal(req)
	h := sha256.Sum256(reqBytes)

	// 1. Idempotency gate.
	var prior *Result
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
			return ErrInProgress
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

	res, err := s.checkout(ctx, tenant, actor, req)
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

func (s *Service) checkout(ctx context.Context, tenant, actor string, req CheckoutRequest) (Result, error) {
	t, ok := s.Projector.Tenant(tenant)
	if !ok {
		return Result{}, fmt.Errorf("orders: tenant %s not loaded", tenant)
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return problem(409, "payment_account_missing", ErrNoPaymentAccount.Error(), nil), nil
	}
	sim, err := t.Simulate(cart.Request{Channel: req.Channel, PostalCode: req.PostalCode, Items: req.Items})
	if err != nil {
		return problem(400, "invalid_request", err.Error(), nil), nil
	}
	// Every requested unit must be allocated; otherwise show the simulation back.
	for i, it := range req.Items {
		if i >= len(sim.Lines) || sim.Lines[i].SKU != it.SKU || sim.Lines[i].Qty != it.Qty {
			return problem(409, "cart_changed", ErrCartChanged.Error(), map[string]any{"simulation": sim}), nil
		}
	}
	if sim.Shipping == nil {
		return problem(400, "postal_code_required", "Se necesita un código postal válido para calcular el envío", nil), nil
	}
	cust, _ := json.Marshal(req.Customer)
	id := newID("ord_")
	enc, err := s.Keys.Encrypt(tenant, cust, id)
	if err != nil {
		return Result{}, err
	}
	tot := sim.Totals
	var number int64

	// 2. Reserve stock and create the order atomically.
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT nextval('order_numbers')`).Scan(&number); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO orders (tenant_id, id, number, channel, status, currency, items_total, discounts, shipping, tax_included, grand_total, customer, postal_code)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			tenant, id, number, req.Channel, StatusPendingPayment, string(tot.Grand.Currency), tot.Items.Amount, tot.Discounts.Amount,
			tot.Shipping.Amount, tot.Tax.Amount, tot.Grand.Amount, enc, req.PostalCode); err != nil {
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
		return s.event(ctx, tx, tenant, id, actor, "order.created", nil, StatusPendingPayment, map[string]any{"promotions": sim.Promotions})
	})
	if errors.Is(err, store.ErrInsufficient) {
		return problem(409, "insufficient_stock", err.Error(), nil), nil
	}
	if err != nil {
		return Result{}, err
	}

	// 3. Charge. The idempotency key is the order id, so a retry never charges twice.
	fee := tot.Grand.Amount * acct.FeeBps / 10000
	ch, payErr := acct.Provider.Charge(ctx, payments.ChargeRequest{
		Account: acct.Account, OrderID: id, Amount: tot.Grand.Amount, Currency: string(tot.Grand.Currency),
		PaymentMethod: req.PaymentMethod, FeeAmount: fee, IdempotencyKey: "charge-" + id, Email: req.Customer.Email,
	})
	if payErr != nil {
		// Unknown outcome (network, Stripe outage): leave the order pending; the
		// reconciler resolves it with Lookup. Reservations expire meanwhile.
		return problem(502, "payment_pending", "No se pudo confirmar el pago; el pedido quedó pendiente y se resolverá automáticamente", map[string]any{"orderId": id}), nil
	}
	if err := s.settle(ctx, tenant, actor, id, acct, ch); err != nil {
		return Result{}, err
	}
	o, err := s.Get(ctx, tenant, id, false)
	if err != nil {
		return Result{}, err
	}
	if ch.Status != "captured" {
		return problem(402, "payment_declined", "El pago fue rechazado: "+ch.Reason, map[string]any{"order": o}), nil
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
		pstatus := "failed"
		if ch.Status == "captured" {
			pstatus = "captured"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO payments (tenant_id, id, order_id, provider, provider_ref, status, amount, currency)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, newID("pay_"), id, acct.Provider.Name(), ch.Ref, pstatus, grand, cur); err != nil {
			return err
		}
		if ch.Status != "captured" {
			if err := store.ReleaseTx(ctx, tx, tenant, id); err != nil {
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
		if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, StatusPaid); err != nil {
			return err
		}
		// Double entry: cash at Stripe vs sales and tax payable.
		txID := "pay-" + id
		if err := ledger(ctx, tx, tenant, txID, id, cur, map[string]int64{
			"psp_clearing": grand, "sales": -(grand - tax), "tax_payable": -tax,
		}); err != nil {
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
	return store.Audit(ctx, tx, tenant, actor, typ, id, map[string]any{"to": to})
}

// Fulfill marks a paid order as shipped.
func (s *Service) Fulfill(ctx context.Context, tenant, actor, id string) error {
	return s.transition(ctx, tenant, actor, id, StatusPaid, StatusFulfilled, "order.fulfilled", nil)
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

// Cancel reverts an order completely: refund at Stripe (if paid), restock the
// exact locations, reverse the ledger and void the invoice.
func (s *Service) Cancel(ctx context.Context, tenant, actor, id, reason string) error {
	var status, ref, cur string
	var amount, refunded, grand, tax int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT o.status, o.currency, o.grand_total, o.tax_included, COALESCE(p.provider_ref,''), COALESCE(p.amount,0), COALESCE(p.refunded,0)
			FROM orders o LEFT JOIN payments p ON p.tenant_id=o.tenant_id AND p.order_id=o.id AND p.status IN ('captured','refunded')
			WHERE o.tenant_id=$1 AND o.id=$2`, tenant, id).Scan(&status, &cur, &grand, &tax, &ref, &amount, &refunded)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	switch status {
	case StatusPendingPayment:
		return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			if err := store.ReleaseTx(ctx, tx, tenant, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status=$4`, tenant, id, StatusCancelled, StatusPendingPayment); err != nil {
				return err
			}
			return s.event(ctx, tx, tenant, id, actor, "order.cancelled", &status, StatusCancelled, map[string]any{"reason": reason})
		})
	case StatusPaid, StatusFulfilled:
	default:
		return fmt.Errorf("%w: %s", ErrInvalidState, status)
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return err
	}
	toRefund := amount - refunded
	if toRefund > 0 {
		if err := acct.Provider.Refund(ctx, acct.Account, ref, toRefund, "refund-"+id); err != nil {
			return fmt.Errorf("orders: refund failed, order unchanged: %w", err)
		}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var cur2 string
		if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&cur2); err != nil {
			return err
		}
		if cur2 == StatusRefunded {
			return nil // a concurrent cancel already finished
		}
		rows, err := tx.Query(ctx, `SELECT sku, location_id, qty FROM order_allocations WHERE tenant_id=$1 AND order_id=$2`, tenant, id)
		if err != nil {
			return err
		}
		type alloc struct {
			sku, loc string
			qty      int64
		}
		var as []alloc
		for rows.Next() {
			var a alloc
			if err := rows.Scan(&a.sku, &a.loc, &a.qty); err != nil {
				rows.Close()
				return err
			}
			as = append(as, a)
		}
		rows.Close()
		for _, a := range as {
			if err := store.RestockTx(ctx, tx, tenant, a.sku, a.loc, a.qty); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET refunded=amount, status='refunded', updated_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status='captured'`, tenant, id); err != nil {
			return err
		}
		if err := ledger(ctx, tx, tenant, "refund-"+id, id, cur, map[string]int64{
			"psp_clearing": -toRefund, "sales": toRefund - tax, "tax_payable": tax,
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status='voided', updated_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status IN ('pending','failed')`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, StatusRefunded); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, id, actor, "order.refunded", &cur2, StatusRefunded, map[string]any{"reason": reason, "amount": toRefund})
	})
}

// Get loads an order. withPII decrypts the customer.
func (s *Service) Get(ctx context.Context, tenant, id string, withPII bool) (Order, error) {
	var o Order
	var enc []byte
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, number, channel, status, currency, items_total, discounts, shipping, tax_included, grand_total, customer, created_at
			FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).
			Scan(&o.ID, &o.Number, &o.Channel, &o.Status, &o.Currency, &o.ItemsTotal, &o.Discounts, &o.Shipping, &o.TaxIncluded, &o.GrandTotal, &enc, &o.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT sku, title, qty, unit, total FROM order_lines WHERE tenant_id=$1 AND order_id=$2 ORDER BY line`, tenant, id)
		if err != nil {
			return err
		}
		o.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Line, error) {
			var l Line
			return l, r.Scan(&l.SKU, &l.Title, &l.Qty, &l.Unit, &l.Total)
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT seq, at, actor, type, from_status, to_status, data FROM order_events WHERE tenant_id=$1 AND order_id=$2 ORDER BY seq`, tenant, id)
		if err != nil {
			return err
		}
		o.Events, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
			var e Event
			var d []byte
			err := r.Scan(&e.Seq, &e.At, &e.Actor, &e.Type, &e.From, &e.To, &d)
			e.Data = d
			return e, err
		})
		if err != nil {
			return err
		}
		var p Payment
		err = tx.QueryRow(ctx, `SELECT provider, provider_ref, status, amount, refunded FROM payments WHERE tenant_id=$1 AND order_id=$2 ORDER BY created_at DESC LIMIT 1`, tenant, id).
			Scan(&p.Provider, &p.Ref, &p.Status, &p.Amount, &p.Refunded)
		if err == nil {
			o.Payment = &p
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var inv Invoice
		err = tx.QueryRow(ctx, `SELECT status, url, attempts, last_error FROM invoices WHERE tenant_id=$1 AND order_id=$2 LIMIT 1`, tenant, id).
			Scan(&inv.Status, &inv.URL, &inv.Attempts, &inv.Error)
		if err == nil {
			o.Invoice = &inv
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return nil
	})
	if err != nil {
		return o, err
	}
	if withPII {
		plain, err := s.Keys.Decrypt(tenant, enc, id)
		if err != nil {
			return o, err
		}
		var c Customer
		_ = json.Unmarshal(plain, &c)
		o.Customer = &c
	}
	return o, nil
}

// List returns recent orders without personal data.
func (s *Service) List(ctx context.Context, tenant string, limit int) ([]Order, error) {
	var out []Order
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, number, channel, status, currency, grand_total, created_at FROM orders WHERE tenant_id=$1 ORDER BY number DESC LIMIT $2`, tenant, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) {
			var o Order
			return o, r.Scan(&o.ID, &o.Number, &o.Channel, &o.Status, &o.Currency, &o.GrandTotal, &o.CreatedAt)
		})
		return err
	})
	return out, err
}

// Reconcile resolves orders stuck in pending_payment for longer than after:
// it asks the provider whether the charge happened and settles accordingly.
func (s *Service) Reconcile(ctx context.Context, after time.Duration) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id FROM orders WHERE status=$1 AND created_at < now() - $2::interval`,
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
