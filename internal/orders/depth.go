package orders

// After-sale depth: what happens to an order once it is paid.
//
//	fulfilments  partial shipments with carrier, tracking number and URL
//	refunds      by line and/or amount, idempotent at the provider
//	returns      RMA with reason codes, restock decision per item, refund on receipt
//	exchanges    a return plus new items; the credit pays for them
//	edits        add/remove items before shipping; the difference is charged or refunded
//	drafts       orders created by staff, paid manually or through a payment link
//
// Money model. orders.grand_total is what the order is worth. refunded_total
// is the value given back (money refunds and exchange credit). The money
// actually held is the sum over payments of amount - refunded. So:
//
//	balance = grand_total - refunded_total - sum(payments.amount - payments.refunded)
//
// balance > 0 means the customer owes (payment link or manual payment);
// balance < 0 means the store owes (adjustment refund). Every movement posts
// balanced ledger entries; customer_receivable carries what is owed in between.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/promostore"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// More errors.
var (
	ErrInvalid      = errors.New("orders: invalid request")
	ErrRefundFailed = errors.New("orders: the payment provider rejected the refund")
)

// FieldError is a validation error on one field.
type FieldError struct{ Field, Message string }

func (e FieldError) Error() string { return e.Message }

// Fulfillment is one shipment of an order.
type Fulfillment struct {
	ID             string            `json:"id"`
	Number         int               `json:"number"`
	Status         string            `json:"status"` // pending | shipped | delivered | cancelled
	Carrier        string            `json:"carrier"`
	TrackingNumber string            `json:"trackingNumber"`
	TrackingURL    string            `json:"trackingUrl"`
	Note           string            `json:"note,omitempty"`
	Items          []FulfillmentItem `json:"items"`
	CreatedAt      time.Time         `json:"createdAt"`
	ShippedAt      *time.Time        `json:"shippedAt,omitempty"`
	DeliveredAt    *time.Time        `json:"deliveredAt,omitempty"`
	CancelledAt    *time.Time        `json:"cancelledAt,omitempty"`
	// Bought from a connected carrier: its id, the label and what it cost the
	// store; TrackingStatus is the last word of the carrier.
	CarrierID      string `json:"carrierId,omitempty"`
	LabelURL       string `json:"labelUrl,omitempty"`
	LabelCost      int64  `json:"labelCost,omitempty"`
	TrackingStatus string `json:"trackingStatus,omitempty"`
}

// FulfillmentItem is a line inside a shipment.
type FulfillmentItem struct {
	Line  int    `json:"line"`
	SKU   string `json:"sku"`
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
}

// RefundItem is a refunded line.
type RefundItem struct {
	Line      int    `json:"line"`
	SKU       string `json:"sku"`
	Qty       int64  `json:"qty"`
	Amount    int64  `json:"amount"`
	Restocked int64  `json:"restocked,omitempty"`
}

// Refund is money (or exchange credit) given back.
type Refund struct {
	ID             string       `json:"id"`
	Kind           string       `json:"kind"`   // refund | credit | adjustment | cancellation
	Status         string       `json:"status"` // pending | succeeded | failed
	Amount         int64        `json:"amount"`
	TaxAmount      int64        `json:"taxAmount"`
	ShippingAmount int64        `json:"shippingAmount"`
	Reason         string       `json:"reason,omitempty"`
	Items          []RefundItem `json:"items"`
	ReturnID       string       `json:"returnId,omitempty"`
	LastError      string       `json:"lastError,omitempty"`
	CreatedBy      string       `json:"createdBy"`
	CreatedAt      time.Time    `json:"createdAt"`
	CompletedAt    *time.Time   `json:"completedAt,omitempty"`
}

// ReturnItem is a returned line.
type ReturnItem struct {
	Line            int    `json:"line"`
	SKU             string `json:"sku"`
	Title           string `json:"title"`
	Qty             int64  `json:"qty"`
	Reason          string `json:"reason"`
	ReceivedQty     int64  `json:"receivedQty"`
	Restock         *bool  `json:"restock,omitempty"`
	RestockLocation string `json:"restockLocation,omitempty"`
}

// ExchangeItem is a new item sent to the customer in an exchange.
type ExchangeItem struct {
	Line  int    `json:"line"`
	SKU   string `json:"sku"`
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
	Unit  int64  `json:"unit"`
	Total int64  `json:"total"`
}

// Return is an RMA (kind "return") or an exchange.
type Return struct {
	ID             string         `json:"id"`
	OrderID        string         `json:"orderId"`
	OrderNumber    int64          `json:"orderNumber,omitempty"`
	Currency       string         `json:"currency,omitempty"`
	Number         int64          `json:"number"`
	Kind           string         `json:"kind"`
	Status         string         `json:"status"` // requested | received | completed | cancelled
	Note           string         `json:"note,omitempty"`
	RefundShipping int64          `json:"refundShipping"`
	Credit         int64          `json:"credit"`
	CreditApplied  int64          `json:"creditApplied"`
	RefundAmount   int64          `json:"refundAmount"`
	RefundID       string         `json:"refundId,omitempty"`
	Items          []ReturnItem   `json:"items"`
	NewItems       []ExchangeItem `json:"newItems"`
	CreatedBy      string         `json:"createdBy"`
	CreatedAt      time.Time      `json:"createdAt"`
	ReceivedAt     *time.Time     `json:"receivedAt,omitempty"`
	ClosedAt       *time.Time     `json:"closedAt,omitempty"`
	// Prepaid return label, if one was bought, and who asked for the return.
	CarrierID     string `json:"carrierId,omitempty"`
	LabelTracking string `json:"labelTracking,omitempty"`
	LabelURL      string `json:"labelUrl,omitempty"`
	LabelCost     int64  `json:"labelCost,omitempty"`
	RequestedBy   string `json:"requestedBy"`
}

// ReasonCodes are the accepted return reasons.
var ReasonCodes = []string{"size", "defective", "not_as_described", "wrong_item", "changed_mind", "arrived_late", "other"}

// Note is a comment on an order. Internal notes never leave the admin;
// customer notes are shown to the shopper in their account.
type Note struct {
	ID         string    `json:"id"`
	Visibility string    `json:"visibility"` // internal | customer
	Body       string    `json:"body"`
	Author     string    `json:"author"`
	AuthorName string    `json:"authorName"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Totals is the money summary of an order.
type Totals struct {
	ItemsTotal  int64 `json:"itemsTotal"`
	Discounts   int64 `json:"discounts"`
	Shipping    int64 `json:"shipping"`
	TaxIncluded int64 `json:"taxIncluded"`
	GrandTotal  int64 `json:"grandTotal"`
}

// Edit is one applied order edit.
type Edit struct {
	ID         string          `json:"id"`
	Changes    json.RawMessage `json:"changes"`
	Before     Totals          `json:"before"`
	After      Totals          `json:"after"`
	Difference int64           `json:"difference"`
	Reason     string          `json:"reason,omitempty"`
	CreatedBy  string          `json:"createdBy"`
	CreatedAt  time.Time       `json:"createdAt"`
}

// clearing is the ledger account where money of a provider lands.
func clearing(provider string) string {
	switch provider {
	case "manual":
		return "manual_clearing"
	case "gift_card", "store_credit":
		return "gift_card_liability" // what the store owes to card holders
	case "company_credit":
		return "accounts_receivable" // what companies owe the store
	}
	return "psp_clearing"
}

// deriveTx recomputes payment_status and fulfillment_status from the facts and
// refreshes the lifetime stats of the customer. It runs after every order event.
func deriveTx(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	if err := digitalGrantsTx(ctx, tx, tenant, id); err != nil {
		return err
	}
	var cid *string
	err := tx.QueryRow(ctx, `UPDATE orders o SET
			fulfillment_status = CASE
				WHEN l.need = 0 AND l.digital > 0 AND o.status <> 'pending_payment' THEN 'fulfilled'
				WHEN l.need > 0 AND l.fulfilled >= l.need AND f.open = 0 AND f.delivered > 0 THEN 'delivered'
				WHEN l.need > 0 AND l.fulfilled >= l.need THEN 'fulfilled'
				WHEN l.fulfilled > 0 THEN 'partial' ELSE 'unfulfilled' END,
			payment_status = CASE
				WHEN o.status = 'pending_payment' THEN 'pending'
				WHEN o.status = 'cancelled' THEN 'voided'
				WHEN o.status = 'refunded' OR (o.grand_total > 0 AND o.refunded_total >= o.grand_total) THEN 'refunded'
				WHEN o.grand_total - o.refunded_total - p.net > 0 THEN 'balance_due'
				WHEN o.refunded_total > 0 THEN 'partially_refunded'
				ELSE 'paid' END
		FROM (SELECT COALESCE(sum(ol.qty - ol.cancelled_qty) FILTER (WHERE NOT d.only),0) AS need, COALESCE(sum(ol.fulfilled_qty),0) AS fulfilled,
				count(*) FILTER (WHERE d.only) AS digital
			FROM order_lines ol
			CROSS JOIN LATERAL (SELECT EXISTS (SELECT 1 FROM digital_files f WHERE f.tenant_id=$1 AND f.sku=ol.sku)
				AND COALESCE((SELECT v.weight_grams FROM variants v WHERE v.tenant_id=$1 AND v.sku=ol.sku), 0) = 0 AS only) d
			WHERE ol.tenant_id=$1 AND ol.order_id=$2) l,
		     (SELECT count(*) FILTER (WHERE status IN ('pending','shipped')) AS open, count(*) FILTER (WHERE status='delivered') AS delivered FROM fulfillments WHERE tenant_id=$1 AND order_id=$2) f,
		     (SELECT COALESCE(sum(amount - refunded),0) AS net FROM payments WHERE tenant_id=$1 AND order_id=$2 AND status IN ('captured','refunded')) p
		WHERE o.tenant_id=$1 AND o.id=$2 RETURNING o.customer_id`, tenant, id).Scan(&cid)
	if err != nil {
		return err
	}
	if err := sellerSyncTx(ctx, tx, tenant, id); err != nil {
		return err
	}
	if err := loyaltySyncTx(ctx, tx, tenant, id); err != nil {
		return err
	}
	if cid != nil {
		return customers.RecalcStatsTx(ctx, tx, tenant, *cid)
	}
	return nil
}

// digitalGrantsTx gives the buyer access to the files of the digital SKUs of
// a paid order, and takes it away when the order is cancelled or refunded or
// the line is fully cancelled or refunded.
func digitalGrantsTx(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO download_grants (tenant_id, id, order_id, file_id, max_downloads, expires_at)
		SELECT $1, 'dlg_' || replace(gen_random_uuid()::text, '-', ''), $2, f.id, f.max_downloads, now() + make_interval(days => f.link_days)
		FROM orders o
		JOIN order_lines l ON l.tenant_id=o.tenant_id AND l.order_id=o.id AND l.qty - l.cancelled_qty - l.refunded_qty > 0
		JOIN digital_files f ON f.tenant_id=o.tenant_id AND f.sku=l.sku
		WHERE o.tenant_id=$1 AND o.id=$2 AND o.status IN ('paid','fulfilled')
		ON CONFLICT (tenant_id, order_id, file_id) DO NOTHING`, tenant, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE download_grants g SET revoked_at=now()
		FROM digital_files f, orders o
		WHERE g.tenant_id=$1 AND g.order_id=$2 AND g.revoked_at IS NULL
		  AND f.tenant_id=$1 AND f.id=g.file_id AND o.tenant_id=$1 AND o.id=$2
		  AND (o.status IN ('cancelled','refunded') OR NOT EXISTS (
			SELECT 1 FROM order_lines l WHERE l.tenant_id=$1 AND l.order_id=$2 AND l.sku=f.sku AND l.qty - l.cancelled_qty - l.refunded_qty > 0))`, tenant, id)
	return err
}

// linkCustomerTx finds or creates the customer of a new order by email.
func (s *Service) linkCustomerTx(ctx context.Context, tx pgx.Tx, tenant, orderID string, c Customer, postal string) error {
	cid, _, err := customers.UpsertByEmailTx(ctx, tx, s.Keys, tenant, c.Email, c.Name, c.Phone)
	var fe customers.FieldError
	if errors.As(err, &fe) {
		return nil // an order without a usable email simply has no customer
	}
	if err != nil {
		return err
	}
	h, err := customers.EmailHash(s.Keys, tenant, c.Email)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET customer_id=$3, email_hash=$4 WHERE tenant_id=$1 AND id=$2`, tenant, orderID, cid, h); err != nil {
		return err
	}
	return customers.SaveAddressIfNoneTx(ctx, tx, s.Keys, tenant, cid, c.Name, c.Phone, c.Address, postal)
}

// customerTx decrypts the customer of an order.
func (s *Service) customerTx(ctx context.Context, tx pgx.Tx, tenant, id string) (Customer, error) {
	var enc []byte
	var c Customer
	if err := tx.QueryRow(ctx, `SELECT customer FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&enc); err != nil {
		return c, err
	}
	plain, err := s.Keys.Decrypt(tenant, enc, id)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(plain, &c)
}

// afterPaidTx runs when an order becomes paid: confirmation email and the
// shopper's abandoned cart is closed as recovered.
func (s *Service) afterPaidTx(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	c, err := s.customerTx(ctx, tx, tenant, id)
	if err != nil {
		return nil // unreadable customer: the sale still stands
	}
	if _, ok := customers.NormalizeEmail(c.Email); ok {
		if err := customers.MarkRecoveredTx(ctx, tx, s.Keys, tenant, c.Email, id); err != nil {
			return err
		}
	}
	// Ad platforms the store connected hear about the purchase, if the shopper agreed.
	if _, err := tx.Exec(ctx, `INSERT INTO conversion_jobs (tenant_id, id, order_id, platform)
		SELECT $1, 'cvj_' || replace(gen_random_uuid()::text, '-', ''), $2, p.key
		FROM tenant_docs d, jsonb_each(d.data) p, orders o
		WHERE d.tenant_id=$1 AND d.kind='ads_conversions' AND d.id='config' AND (p.value->>'active')::boolean
		  AND p.key IN ('meta','ga4') AND o.tenant_id=$1 AND o.id=$2 AND o.ads_consent
		ON CONFLICT DO NOTHING`, tenant, id); err != nil {
		return err
	}
	return s.notifyTx(ctx, tx, tenant, id, notify.TplOrderConfirmed, nil, "", "confirmed:"+id)
}

// OrderURL is the shopper-facing page of an order.
func (s *Service) OrderURL(tenant, id string) string {
	return s.PublicURL + "/s/" + tenant + "/main/cuenta?pedido=" + id
}

// notifyTx queues an email about an order inside the caller's transaction.
func (s *Service) notifyTx(ctx context.Context, tx pgx.Tx, tenant, id, tpl string, vars map[string]string, action, dedupe string) error {
	if s.Notify == nil {
		return nil
	}
	c, err := s.customerTx(ctx, tx, tenant, id)
	if err != nil {
		return nil
	}
	var number, items, disc, ship, grand int64
	var cur string
	var cid *string
	if err := tx.QueryRow(ctx, `SELECT number, currency, items_total, discounts, shipping, grand_total, customer_id FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).
		Scan(&number, &cur, &items, &disc, &ship, &grand, &cid); err != nil {
		return err
	}
	d := notify.Data{Vars: map[string]string{"cliente.nombre": firstWord(c.Name), "pedido.numero": strconv.FormatInt(number, 10), "pedido.total": notify.Money(grand, cur)}, ActionURL: action}
	// The store's own fields of the order ({{campo.mensaje_regalo}}) and of
	// the customer ({{cliente.campo.talla}}) can be used in the copy.
	s.customVars(ctx, tx, tenant, "order", id, "campo.", d.Vars)
	if cid != nil {
		s.customVars(ctx, tx, tenant, "customer", *cid, "cliente.campo.", d.Vars)
	}
	for k, v := range vars {
		d.Vars[k] = v
	}
	if d.ActionURL == "" {
		d.ActionURL = s.OrderURL(tenant, id)
	}
	rows, err := tx.Query(ctx, `SELECT title, qty, total FROM order_lines WHERE tenant_id=$1 AND order_id=$2 ORDER BY line`, tenant, id)
	if err != nil {
		return err
	}
	d.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (notify.Line, error) {
		var l notify.Line
		var total int64
		err := r.Scan(&l.Title, &l.Qty, &total)
		l.Total = notify.Money(total, cur)
		return l, err
	})
	if err != nil {
		return err
	}
	d.Totals = []notify.Row{{Label: "Productos", Value: notify.Money(items, cur)}}
	if disc > 0 {
		d.Totals = append(d.Totals, notify.Row{Label: "Descuentos", Value: "-" + notify.Money(disc, cur)})
	}
	d.Totals = append(d.Totals, notify.Row{Label: "Envío", Value: notify.Money(ship, cur)}, notify.Row{Label: "Total", Value: notify.Money(grand, cur), Bold: true})
	if tpl == notify.TplOrderConfirmed {
		// Paid: the links to the digital files go in the confirmation.
		if err := digitalGrantsTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		dls, err := s.downloadsTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		for _, dl := range dls {
			if dl.URL != "" {
				d.Downloads = append(d.Downloads, notify.Row{Label: dl.Filename, Value: s.PublicURL + dl.URL})
			}
		}
	}
	m := notify.Message{Template: tpl, To: c.Email, Name: c.Name, Data: d, OrderID: id, DedupeKey: dedupe}
	if cid != nil {
		m.CustomerID = *cid
	}
	_, err = s.Notify.EnqueueTx(ctx, tx, tenant, m)
	return err
}

func firstWord(n string) string {
	if f := strings.Fields(n); len(f) > 0 {
		return f[0]
	}
	return ""
}

// lockOrder locks the order row and returns its core fields.
type orderRow struct {
	Status, Currency, Channel, PaymentStatus, FulfillmentStatus string
	Number                                                      int64
	Totals
	RefundedTotal, RefundedTax int64
}

func lockOrder(ctx context.Context, tx pgx.Tx, tenant, id string) (orderRow, error) {
	var o orderRow
	err := tx.QueryRow(ctx, `SELECT status, currency, channel, payment_status, fulfillment_status, number, items_total, discounts, shipping, tax_included, grand_total, refunded_total, refunded_tax
		FROM orders WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).
		Scan(&o.Status, &o.Currency, &o.Channel, &o.PaymentStatus, &o.FulfillmentStatus, &o.Number, &o.ItemsTotal, &o.Discounts, &o.Shipping, &o.TaxIncluded, &o.GrandTotal, &o.RefundedTotal, &o.RefundedTax)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

func linesTx(ctx context.Context, tx pgx.Tx, tenant, id string) ([]Line, error) {
	rows, err := tx.Query(ctx, `SELECT line, sku, title, qty, unit, total, fulfilled_qty, returned_qty, refunded_qty, cancelled_qty, origin,
			COALESCE(to_char(preorder_ships_on,'YYYY-MM-DD'),''), custom
		FROM order_lines WHERE tenant_id=$1 AND order_id=$2 ORDER BY line`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Line, error) {
		var l Line
		return l, r.Scan(&l.Line, &l.SKU, &l.Title, &l.Qty, &l.Unit, &l.Total, &l.FulfilledQty, &l.ReturnedQty, &l.RefundedQty, &l.CancelledQty, &l.Origin, &l.PreorderShipsOn, &l.Custom)
	})
}

// netPaidTx is the money held for an order: collected minus refunded.
func netPaidTx(ctx context.Context, tx pgx.Tx, tenant, id string) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount - refunded),0) FROM payments WHERE tenant_id=$1 AND order_id=$2 AND status IN ('captured','refunded')`, tenant, id).Scan(&n)
	return n, err
}

// syncStatusTx moves the order between paid and fulfilled as units ship.
func (s *Service) syncStatusTx(ctx context.Context, tx pgx.Tx, tenant, actor, id, typ string, data any) error {
	o, err := lockOrder(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	to := o.Status
	if o.Status == StatusPaid || o.Status == StatusFulfilled {
		var need, done int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(qty - cancelled_qty),0), COALESCE(sum(fulfilled_qty),0) FROM order_lines WHERE tenant_id=$1 AND order_id=$2`, tenant, id).Scan(&need, &done); err != nil {
			return err
		}
		to = StatusPaid
		if need > 0 && done >= need {
			to = StatusFulfilled
		}
		if to != o.Status {
			if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, to); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE orders SET version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
			return err
		}
	}
	return s.event(ctx, tx, tenant, id, actor, typ, &o.Status, to, data)
}

// Get loads an order with its whole after-sale state. withPII decrypts the customer and notes.
func (s *Service) Get(ctx context.Context, tenant, id string, withPII bool) (Order, error) {
	var o Order
	var enc []byte
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var methodJSON, taxJSON, riskJSON []byte
		err := tx.QueryRow(ctx, `SELECT id, number, channel, status, currency, items_total, discounts, shipping, tax_included, grand_total, customer, created_at,
				COALESCE(customer_id,''), payment_status, fulfillment_status, refunded_total, source,
				shipping_method, tax_detail, display_number, risk, installments, po_number, fees, fee_lines, COALESCE(postal_code,''), voucher, deferred, checkout_fields
			FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).
			Scan(&o.ID, &o.Number, &o.Channel, &o.Status, &o.Currency, &o.ItemsTotal, &o.Discounts, &o.Shipping, &o.TaxIncluded, &o.GrandTotal, &enc, &o.CreatedAt,
				&o.CustomerID, &o.PaymentStatus, &o.FulfillmentStatus, &o.RefundedTotal, &o.Source, &methodJSON, &taxJSON, &o.DisplayNumber, &riskJSON, &o.Installments, &o.PONumber, &o.Fees, &o.FeeLines, &o.PostalCode, &o.Voucher, &o.Deferred, &o.CheckoutFields)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if o.Status != StatusPendingPayment {
			o.Voucher = nil // paid or cancelled: nothing left to pay
		}
		var rk Risk
		if json.Unmarshal(riskJSON, &rk) == nil && rk.Level != "" {
			o.Risk = &rk
		}
		var m ShippingMethod
		if json.Unmarshal(methodJSON, &m) == nil && m.Name != "" {
			o.ShippingMethod = &m
		}
		var td logistics.TaxResult
		if json.Unmarshal(taxJSON, &td) == nil && td.Lines != nil {
			o.TaxDetail = &td
		}
		if o.DisplayNumber == "" {
			o.DisplayNumber = fmt.Sprint(o.Number)
		}
		if o.Lines, err = linesTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if o.Downloads, err = s.downloadsTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT seq, at, actor, type, from_status, to_status, data FROM order_events WHERE tenant_id=$1 AND order_id=$2 ORDER BY seq`, tenant, id)
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
		rows, err = tx.Query(ctx, `SELECT id, provider, provider_ref, status, amount, refunded FROM payments WHERE tenant_id=$1 AND order_id=$2 ORDER BY created_at, id`, tenant, id)
		if err != nil {
			return err
		}
		o.Payments, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payment, error) {
			var p Payment
			return p, r.Scan(&p.ID, &p.Provider, &p.Ref, &p.Status, &p.Amount, &p.Refunded)
		})
		if err != nil {
			return err
		}
		if n := len(o.Payments); n > 0 {
			// "payment" keeps its old meaning: one summary of what was collected and refunded.
			sum := o.Payments[n-1]
			var amt, ref int64
			found := false
			for _, p := range o.Payments {
				if p.Status == "captured" || p.Status == "refunded" {
					if !found {
						sum, found = p, true
					}
					amt += p.Amount
					ref += p.Refunded
				}
			}
			if found {
				sum.Amount, sum.Refunded, sum.Status = amt, ref, "captured"
				if ref >= amt {
					sum.Status = "refunded"
				}
				o.PaidTotal = amt - ref
			}
			o.Payment = &sum
		}
		if o.Status == StatusPaid || o.Status == StatusFulfilled {
			o.Balance = o.GrandTotal - o.RefundedTotal - o.PaidTotal
		}
		var inv Invoice
		err = tx.QueryRow(ctx, `SELECT status, url, attempts, last_error FROM invoices WHERE tenant_id=$1 AND order_id=$2 LIMIT 1`, tenant, id).
			Scan(&inv.Status, &inv.URL, &inv.Attempts, &inv.Error)
		if err == nil {
			o.Invoice = &inv
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		rrows, err := tx.Query(ctx, `SELECT promotion_id, promotion_name, coupon_code, amount, currency, status, created_at
			FROM promotion_redemptions WHERE tenant_id=$1 AND order_id=$2 ORDER BY promotion_id`, tenant, id)
		if err != nil {
			return err
		}
		o.Promotions, err = pgx.CollectRows(rrows, func(r pgx.CollectableRow) (promostore.Redemption, error) {
			x := promostore.Redemption{OrderID: id, OrderNumber: o.Number}
			return x, r.Scan(&x.PromotionID, &x.PromotionName, &x.Coupon, &x.Amount, &x.Currency, &x.Status, &x.CreatedAt)
		})
		if err != nil {
			return err
		}
		if o.Fulfillments, err = fulfillmentsTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if o.Refunds, err = refundsTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if o.Returns, err = returnsTx(ctx, tx, tenant, `r.order_id=$2`, id); err != nil {
			return err
		}
		if o.Edits, err = editsTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if withPII {
			if o.Notes, err = s.notesTx(ctx, tx, tenant, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return o, err
	}
	for _, l := range o.Lines {
		o.ItemsCount += l.Qty - l.CancelledQty
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

// ---------- list, search, filters, export ----------

// ListFilter filters the order list. Multi-valued fields accept several values.
type ListFilter struct {
	Q           string // order number, order id or the customer's email
	Status      []string
	Payment     []string
	Fulfillment []string
	Review      bool // only orders flagged by the fraud rules and not yet reviewed
	Channel     string
	CustomerID  string
	From, To    *time.Time
	Sort        string // number_desc (default) | number_asc | total_desc | total_asc
	Cursor      string
	Limit       int
}

var (
	validStatus      = map[string]bool{StatusPendingPayment: true, StatusPaid: true, StatusFulfilled: true, StatusCancelled: true, StatusRefunded: true}
	validPayment     = map[string]bool{"pending": true, "paid": true, "balance_due": true, "partially_refunded": true, "refunded": true, "voided": true}
	validFulfillment = map[string]bool{"unfulfilled": true, "partial": true, "fulfilled": true, "delivered": true}
)

// Validate checks filter values.
func (f *ListFilter) Validate() error {
	for _, c := range []struct {
		name  string
		vals  []string
		valid map[string]bool
	}{{"status", f.Status, validStatus}, {"payment", f.Payment, validPayment}, {"fulfillment", f.Fulfillment, validFulfillment}} {
		for _, v := range c.vals {
			if !c.valid[v] {
				return FieldError{c.name, c.name + " no válido: " + v}
			}
		}
	}
	switch f.Sort {
	case "":
		f.Sort = "number_desc"
	case "number_desc", "number_asc", "total_desc", "total_asc":
	default:
		return FieldError{"sort", "sort: number_desc, number_asc, total_desc o total_asc"}
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return FieldError{"to", "La fecha final es anterior a la inicial"}
	}
	if len(f.Q) > 200 {
		return FieldError{"q", "Búsqueda demasiado larga"}
	}
	return nil
}

func (s *Service) where(tenant string, f ListFilter) (string, []any, error) {
	w := ` WHERE o.tenant_id=$1`
	args := []any{tenant}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(f.Q), "#")); q != "" {
		switch {
		case strings.Contains(q, "@"):
			h, err := customers.EmailHash(s.Keys, tenant, q)
			if err != nil {
				return "", nil, err
			}
			w += ` AND o.email_hash=` + arg(h)
		case strings.HasPrefix(q, "ord_"):
			w += ` AND o.id=` + arg(q)
		default:
			n, err := strconv.ParseInt(q, 10, 64)
			if err != nil {
				return "", nil, FieldError{"q", "Busca por número de pedido o por el correo completo del cliente"}
			}
			w += ` AND o.number=` + arg(n)
		}
	}
	if len(f.Status) > 0 {
		w += ` AND o.status = ANY(` + arg(f.Status) + `)`
	}
	if f.Review {
		w += ` AND o.risk->>'level' = 'review'`
	}
	if len(f.Payment) > 0 {
		w += ` AND o.payment_status = ANY(` + arg(f.Payment) + `)`
	}
	if len(f.Fulfillment) > 0 {
		w += ` AND o.fulfillment_status = ANY(` + arg(f.Fulfillment) + `)`
	}
	if f.Channel != "" {
		w += ` AND o.channel=` + arg(f.Channel)
	}
	if f.CustomerID != "" {
		w += ` AND o.customer_id=` + arg(f.CustomerID)
	}
	if f.From != nil {
		w += ` AND o.created_at >= ` + arg(*f.From)
	}
	if f.To != nil {
		w += ` AND o.created_at < ` + arg(*f.To)
	}
	return w, args, nil
}

// Page is one page of the order list.
type Page struct {
	Items    []Order  `json:"items"`
	Next     string   `json:"nextCursor"`
	Total    int      `json:"total"`
	Channels []string `json:"channels,omitempty"`
}

// Query lists orders with search, filters, sorting and cursor pagination.
// withPII adds the customer's name and email to each row.
func (s *Service) Query(ctx context.Context, tenant string, f ListFilter, withPII bool) (Page, error) {
	p := Page{Items: []Order{}}
	if err := f.Validate(); err != nil {
		return p, err
	}
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	w, args, err := s.where(tenant, f)
	if err != nil {
		return p, err
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders o`+w, args...).Scan(&p.Total); err != nil {
			return err
		}
		byTotal := strings.HasPrefix(f.Sort, "total")
		dir, cmp := "DESC", "<"
		if strings.HasSuffix(f.Sort, "asc") {
			dir, cmp = "ASC", ">"
		}
		q := `SELECT o.id, o.number, o.channel, o.status, o.currency, o.grand_total, o.created_at, COALESCE(o.customer_id,''), o.payment_status, o.fulfillment_status,
			o.refunded_total, o.source, o.customer, (SELECT COALESCE(sum(qty - cancelled_qty),0) FROM order_lines l WHERE l.tenant_id=o.tenant_id AND l.order_id=o.id), o.po_number
			FROM orders o` + w
		if f.Cursor != "" {
			a, b, _ := strings.Cut(f.Cursor, "|")
			x, err1 := strconv.ParseInt(a, 10, 64)
			y, err2 := strconv.ParseInt(b, 10, 64)
			if err1 != nil || (byTotal && err2 != nil) {
				return FieldError{"cursor", "Cursor inválido"}
			}
			if byTotal {
				args = append(args, x, y)
				q += fmt.Sprintf(` AND (o.grand_total, o.number) %s ($%d, $%d)`, cmp, len(args)-1, len(args))
			} else {
				args = append(args, x)
				q += fmt.Sprintf(` AND o.number %s $%d`, cmp, len(args))
			}
		}
		if byTotal {
			q += ` ORDER BY o.grand_total ` + dir + `, o.number ` + dir
		} else {
			q += ` ORDER BY o.number ` + dir
		}
		args = append(args, f.Limit+1)
		q += fmt.Sprintf(` LIMIT $%d`, len(args))
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o Order
			var enc []byte
			if err := rows.Scan(&o.ID, &o.Number, &o.Channel, &o.Status, &o.Currency, &o.GrandTotal, &o.CreatedAt, &o.CustomerID, &o.PaymentStatus, &o.FulfillmentStatus,
				&o.RefundedTotal, &o.Source, &enc, &o.ItemsCount, &o.PONumber); err != nil {
				return err
			}
			if withPII {
				if plain, err := s.Keys.Decrypt(tenant, enc, o.ID); err == nil {
					var c Customer
					_ = json.Unmarshal(plain, &c)
					o.CustomerName, o.CustomerEmail = c.Name, c.Email
				}
			}
			p.Items = append(p.Items, o)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		ch, err := tx.Query(ctx, `SELECT DISTINCT channel FROM orders WHERE tenant_id=$1 ORDER BY 1 LIMIT 50`, tenant)
		if err != nil {
			return err
		}
		p.Channels, err = pgx.CollectRows(ch, pgx.RowTo[string])
		return err
	})
	if err != nil {
		return p, err
	}
	if len(p.Items) > f.Limit {
		p.Items = p.Items[:f.Limit]
		l := p.Items[f.Limit-1]
		if strings.HasPrefix(f.Sort, "total") {
			p.Next = fmt.Sprintf("%d|%d", l.GrandTotal, l.Number)
		} else {
			p.Next = strconv.FormatInt(l.Number, 10)
		}
	}
	return p, nil
}

// ExportMax is the most rows one CSV export returns.
const ExportMax = 10000

// csvSafe stops spreadsheet formula injection in exported text.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func cents(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

// Export writes the filtered orders as CSV. withPII adds name and email columns.
func (s *Service) Export(ctx context.Context, tenant string, f ListFilter, withPII bool, w io.Writer) (int, error) {
	cw := csv.NewWriter(w)
	head := []string{"pedido", "fecha", "canal", "estado", "pago", "envio", "moneda", "total", "reembolsado", "piezas", "orden_de_compra"}
	if withPII {
		head = append(head, "cliente", "correo")
	}
	if err := cw.Write(head); err != nil {
		return 0, err
	}
	f.Limit, f.Cursor = 200, ""
	n := 0
	for n < ExportMax {
		p, err := s.Query(ctx, tenant, f, withPII)
		if err != nil {
			return n, err
		}
		for _, o := range p.Items {
			rec := []string{strconv.FormatInt(o.Number, 10), o.CreatedAt.UTC().Format(time.RFC3339), csvSafe(o.Channel), o.Status, o.PaymentStatus, o.FulfillmentStatus,
				o.Currency, cents(o.GrandTotal), cents(o.RefundedTotal), strconv.FormatInt(o.ItemsCount, 10), csvSafe(o.PONumber)}
			if withPII {
				rec = append(rec, csvSafe(o.CustomerName), csvSafe(o.CustomerEmail))
			}
			if err := cw.Write(rec); err != nil {
				return n, err
			}
			n++
		}
		if p.Next == "" {
			break
		}
		f.Cursor = p.Next
	}
	cw.Flush()
	return n, cw.Error()
}

// ---------- notes ----------

func (s *Service) notesTx(ctx context.Context, tx pgx.Tx, tenant, id string) ([]Note, error) {
	rows, err := tx.Query(ctx, `SELECT id, visibility, body, author, author_name, created_at FROM order_notes WHERE tenant_id=$1 AND order_id=$2 ORDER BY created_at, id`, tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		var enc []byte
		if err := rows.Scan(&n.ID, &n.Visibility, &enc, &n.Author, &n.AuthorName, &n.CreatedAt); err != nil {
			return nil, err
		}
		if plain, err := s.Keys.Decrypt(tenant, enc, n.ID); err == nil {
			n.Body = string(plain)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddNote adds an internal or customer-visible note to an order.
func (s *Service) AddNote(ctx context.Context, tenant, actor, actorName, orderID, visibility, body string) (Note, error) {
	body = strings.TrimSpace(body)
	if body == "" || len(body) > 2000 {
		return Note{}, FieldError{"body", "Escribe una nota de hasta 2,000 caracteres"}
	}
	if visibility != "internal" && visibility != "customer" {
		return Note{}, FieldError{"visibility", "visibility: internal o customer"}
	}
	n := Note{ID: newID("ont_"), Visibility: visibility, Body: body, Author: actor, AuthorName: actorName, CreatedAt: time.Now()}
	enc, err := s.Keys.Encrypt(tenant, []byte(body), n.ID)
	if err != nil {
		return n, err
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := lockOrder(ctx, tx, tenant, orderID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO order_notes (tenant_id, id, order_id, visibility, body, author, author_name, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tenant, n.ID, orderID, visibility, enc, actor, actorName, n.CreatedAt); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, orderID, map[string]any{"type": "order.note_added", "visibility": visibility}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "order.note_added", orderID, map[string]any{"note": n.ID, "visibility": visibility})
	})
	return n, err
}

// DeleteNote removes a note.
func (s *Service) DeleteNote(ctx context.Context, tenant, actor, orderID, noteID string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM order_notes WHERE tenant_id=$1 AND id=$2 AND order_id=$3`, tenant, noteID, orderID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, orderID, map[string]any{"type": "order.note_deleted"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "order.note_deleted", orderID, map[string]any{"note": noteID})
	})
}

// ---------- saved views ----------

// View is a saved set of list filters.
type View struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Filters   json.RawMessage `json:"filters"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Views lists the saved views of a store.
func (s *Service) Views(ctx context.Context, tenant string) ([]View, error) {
	out := []View{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, filters, created_at FROM order_views WHERE tenant_id=$1 ORDER BY name`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (View, error) {
			var v View
			var f []byte
			err := r.Scan(&v.ID, &v.Name, &f, &v.CreatedAt)
			v.Filters = f
			return v, err
		})
		return err
	})
	return out, err
}

// SaveView stores a view; saving an existing name replaces its filters.
func (s *Service) SaveView(ctx context.Context, tenant, actor, name string, filters json.RawMessage) (View, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return View{}, FieldError{"name", "Escribe un nombre de hasta 60 caracteres"}
	}
	var m map[string]any
	if len(filters) > 4000 || json.Unmarshal(filters, &m) != nil {
		return View{}, FieldError{"filters", "Filtros inválidos"}
	}
	v := View{ID: newID("vw_"), Name: name, Filters: filters}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM order_views WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
			return err
		}
		if n >= 30 {
			return FieldError{"name", "Máximo 30 vistas guardadas; borra alguna"}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO order_views (tenant_id, id, name, filters, created_by) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, name) DO UPDATE SET filters=EXCLUDED.filters RETURNING id, created_at`, tenant, v.ID, name, []byte(filters), actor).Scan(&v.ID, &v.CreatedAt); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, v.ID, map[string]any{"type": "order.view_saved"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "order.view_saved", v.ID, map[string]any{"name": name})
	})
	return v, err
}

// DeleteView removes a saved view.
func (s *Service) DeleteView(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM order_views WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, id, map[string]any{"type": "order.view_deleted"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "order.view_deleted", id, map[string]any{})
	})
}

// customVars adds the custom field values of a record to email variables.
// The email goes to the same person the record is about, so personal values
// are included too.
func (s *Service) customVars(ctx context.Context, tx pgx.Tx, tenant, resource, id, prefix string, vars map[string]string) {
	var data, secret []byte
	if err := tx.QueryRow(ctx, `SELECT data, secret FROM custom_values WHERE tenant_id=$1 AND resource=$2 AND owner_id=$3`, tenant, resource, id).Scan(&data, &secret); err != nil {
		return
	}
	vals := map[string]string{}
	_ = json.Unmarshal(data, &vals)
	if len(secret) > 0 && s.Keys != nil {
		if plain, err := s.Keys.Decrypt(tenant, secret, "fields:"+resource+":"+id); err == nil {
			var p map[string]string
			_ = json.Unmarshal(plain, &p)
			for k, v := range p {
				vals[k] = v
			}
		}
	}
	for k, v := range vals {
		vars[prefix+k] = v
	}
}
