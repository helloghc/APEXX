package orders

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/helloghc/apexx/internal/promostore"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
)

// ---------- refund math ----------

// LineNet is what the customer really paid for a line: its total minus its
// share of the order discount (proportional to the line total, rounded down).
func LineNet(lineTotal, itemsTotal, discounts int64) int64 {
	if itemsTotal <= 0 || discounts <= 0 {
		return lineTotal
	}
	return lineTotal - discounts*lineTotal/itemsTotal
}

// UnitsValue is the refundable value of n units of a line of qty units.
func UnitsValue(lineNet, qty, n int64) int64 {
	if qty <= 0 || n <= 0 {
		return 0
	}
	if n >= qty {
		return lineNet
	}
	return lineNet * n / qty
}

// TaxShare is the tax inside a refund of amount. The last refund takes
// whatever tax is left so refunded tax never exceeds or falls short of the total.
func TaxShare(amount, grandTotal, taxIncluded, refundedTotal, refundedTax int64) int64 {
	left := taxIncluded - refundedTax
	if left <= 0 || grandTotal <= 0 {
		return 0
	}
	if refundedTotal+amount >= grandTotal {
		return min(left, amount)
	}
	return min(left, amount*taxIncluded/grandTotal)
}

// RefundLine asks to refund qty units of a line.
type RefundLine struct {
	Line int   `json:"line"`
	Qty  int64 `json:"qty"`
}

// RefundInput is a partial (or full) refund request.
type RefundInput struct {
	Lines    []RefundLine `json:"lines"`
	Shipping int64        `json:"shipping"` // part of the shipping cost to give back
	Amount   *int64       `json:"amount"`   // overrides the computed amount (<= refundable)
	Reason   string       `json:"reason"`
	Restock  bool         `json:"restock"`  // return unshipped units to stock and take them out of the order
	Notify   *bool        `json:"notify"`   // email the customer (default true)
	ToCredit bool         `json:"toCredit"` // refund as store credit (a code is emailed to the customer)
}

// Quote is the breakdown of a refund before it is made.
type Quote struct {
	Items         []RefundItem `json:"items"`
	ItemsAmount   int64        `json:"itemsAmount"`
	Shipping      int64        `json:"shipping"`
	Suggested     int64        `json:"suggested"`
	MaxRefundable int64        `json:"maxRefundable"` // money still held for this order
	ShippingLeft  int64        `json:"shippingLeft"`
	Tax           int64        `json:"tax"`
}

// quote computes the refund of lines + shipping against the order state.
func quote(o orderRow, lines []Line, netPaid, shippingRefunded int64, in RefundInput) (Quote, error) {
	q := Quote{Items: []RefundItem{}, MaxRefundable: max(netPaid, 0), ShippingLeft: max(o.Shipping-shippingRefunded, 0)}
	byLine := map[int]Line{}
	for _, l := range lines {
		byLine[l.Line] = l
	}
	seen := map[int]bool{}
	for i, rl := range in.Lines {
		l, ok := byLine[rl.Line]
		if !ok || seen[rl.Line] {
			return q, FieldError{fmt.Sprintf("lines[%d].line", i), "Línea desconocida o repetida"}
		}
		seen[rl.Line] = true
		if rl.Qty < 1 || rl.Qty > l.Qty-l.RefundedQty {
			return q, FieldError{fmt.Sprintf("lines[%d].qty", i), fmt.Sprintf("Puedes reembolsar hasta %d piezas de %s", l.Qty-l.RefundedQty, l.Title)}
		}
		net := LineNet(l.Total, o.ItemsTotal, o.Discounts)
		// Value of the units being refunded now = value up to (already + now) minus value already refunded.
		amt := UnitsValue(net, l.Qty, l.RefundedQty+rl.Qty) - UnitsValue(net, l.Qty, l.RefundedQty)
		q.Items = append(q.Items, RefundItem{Line: l.Line, SKU: l.SKU, Qty: rl.Qty, Amount: amt})
		q.ItemsAmount += amt
	}
	if in.Shipping < 0 || in.Shipping > q.ShippingLeft {
		return q, FieldError{"shipping", "El envío por reembolsar no puede pasar de " + notify.Money(q.ShippingLeft, o.Currency)}
	}
	q.Shipping = in.Shipping
	q.Suggested = min(q.ItemsAmount+q.Shipping, q.MaxRefundable)
	amount := q.Suggested
	if in.Amount != nil {
		amount = *in.Amount
	}
	q.Tax = TaxShare(amount, o.GrandTotal, o.TaxIncluded, o.RefundedTotal, o.RefundedTax)
	return q, nil
}

func shippingRefundedTx(ctx context.Context, tx pgx.Tx, tenant, id string) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(shipping_amount),0) FROM refunds WHERE tenant_id=$1 AND order_id=$2 AND status IN ('pending','succeeded')`, tenant, id).Scan(&n)
	return n, err
}

// RefundQuote previews a refund without changing anything.
func (s *Service) RefundQuote(ctx context.Context, tenant, id string, in RefundInput) (Quote, error) {
	var q Quote
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		lines, err := linesTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		net, err := netPaidTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		sr, err := shippingRefundedTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		q, err = quote(o, lines, net, sr, in)
		return err
	})
	return q, err
}

// ---------- refund engine ----------

type refundPart struct {
	PaymentID string `json:"paymentId"`
	Provider  string `json:"provider"`
	Ref       string `json:"ref"`
	Amount    int64  `json:"amount"`
}

type refundSpec struct {
	Kind     string // refund | adjustment | cancellation
	Amount   int64
	Shipping int64
	Items    []RefundItem
	Reason   string
	Restock  bool
	Notify   bool
	ReturnID string
	IdemKey  string
	ToCredit bool // give the money back as store credit instead of to the payment method
	// done runs in the completing transaction (order locked) after the money moved.
	done func(ctx context.Context, tx pgx.Tx, o orderRow, r *Refund) error
}

func refundsTx(ctx context.Context, tx pgx.Tx, tenant, id string) ([]Refund, error) {
	rows, err := tx.Query(ctx, `SELECT id, kind, status, amount, tax_amount, shipping_amount, reason, items, COALESCE(return_id,''), last_error, created_by, created_at, completed_at
		FROM refunds WHERE tenant_id=$1 AND order_id=$2 ORDER BY created_at, id`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanRefund)
}

func scanRefund(r pgx.CollectableRow) (Refund, error) {
	var x Refund
	var items []byte
	err := r.Scan(&x.ID, &x.Kind, &x.Status, &x.Amount, &x.TaxAmount, &x.ShippingAmount, &x.Reason, &items, &x.ReturnID, &x.LastError, &x.CreatedBy, &x.CreatedAt, &x.CompletedAt)
	_ = json.Unmarshal(items, &x.Items)
	if x.Items == nil {
		x.Items = []RefundItem{}
	}
	return x, err
}

// reserveTx splits amount across the payments that still hold money (newest
// first) and reserves it by raising payments.refunded, so two concurrent
// refunds can never give back more than was collected.
func reserveTx(ctx context.Context, tx pgx.Tx, tenant, orderID string, amount int64) ([]refundPart, error) {
	rows, err := tx.Query(ctx, `SELECT id, provider, provider_ref, amount - refunded FROM payments
		WHERE tenant_id=$1 AND order_id=$2 AND status='captured' AND amount > refunded ORDER BY created_at DESC, id DESC FOR UPDATE`, tenant, orderID)
	if err != nil {
		return nil, err
	}
	avail, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (refundPart, error) {
		var p refundPart
		return p, r.Scan(&p.PaymentID, &p.Provider, &p.Ref, &p.Amount)
	})
	if err != nil {
		return nil, err
	}
	left := amount
	var parts []refundPart
	for _, p := range avail {
		if left == 0 {
			break
		}
		take := min(p.Amount, left)
		p.Amount = take
		parts = append(parts, p)
		left -= take
		if _, err := tx.Exec(ctx, `UPDATE payments SET refunded=refunded+$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, p.PaymentID, take); err != nil {
			return nil, err
		}
	}
	if left > 0 {
		return nil, FieldError{"amount", "El monto supera lo que queda por reembolsar de este pedido"}
	}
	return parts, nil
}

// markCredit turns the parts of a refund into store credit: the money stays
// with the store and the customer gets a credit of the same amount. Parts paid
// with a gift card simply go back to that card.
func markCredit(parts []refundPart, toCredit bool) {
	if !toCredit {
		return
	}
	for i := range parts {
		if parts[i].Provider != "gift_card" && parts[i].Provider != "company_credit" {
			parts[i].Provider = "store_credit"
		}
	}
}

func releaseTx(ctx context.Context, tx pgx.Tx, tenant string, parts []refundPart) error {
	for _, p := range parts {
		if _, err := tx.Exec(ctx, `UPDATE payments SET refunded=refunded-$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, p.PaymentID, p.Amount); err != nil {
			return err
		}
	}
	return nil
}

// refund moves money back to the customer in three steps so a crash or a
// provider outage can never refund twice or lose a refund:
//
//  1. one transaction reserves the amount on the payments and writes the
//     refund as pending;
//  2. the provider is called with an idempotency key derived from the refund
//     id and attempt (a retry of the same attempt is a no-op at Stripe);
//  3. one transaction marks it succeeded and posts ledger, stock and events,
//     or releases the reservation when the provider said no.
//
// A refund left pending by a crash is finished by ReconcileRefunds.
func (s *Service) refund(ctx context.Context, tenant, actor, orderID string, spec refundSpec) (Refund, error) {
	var (
		id       string
		parts    []refundPart
		attempt  int
		existing *Refund
	)
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		if spec.IdemKey != "" {
			var raw []byte
			var status string
			err := tx.QueryRow(ctx, `SELECT id, status, parts, attempts FROM refunds WHERE tenant_id=$1 AND idem_key=$2`, tenant, spec.IdemKey).Scan(&id, &status, &raw, &attempt)
			if err == nil {
				switch status {
				case "succeeded":
					rs, err := refundsTx(ctx, tx, tenant, orderID)
					if err != nil {
						return err
					}
					for i := range rs {
						if rs[i].ID == id {
							existing = &rs[i]
						}
					}
					return nil
				case "pending":
					return json.Unmarshal(raw, &parts) // finish the interrupted attempt
				}
				// failed: try again as a new attempt of the same refund.
				if parts, err = reserveTx(ctx, tx, tenant, orderID, spec.Amount); err != nil {
					return err
				}
				markCredit(parts, spec.ToCredit)
				attempt++
				raw, _ = json.Marshal(parts)
				_, err = tx.Exec(ctx, `UPDATE refunds SET status='pending', parts=$3, attempts=$4, last_error='', amount=$5 WHERE tenant_id=$1 AND id=$2`, tenant, id, raw, attempt, spec.Amount)
				return err
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if o.Status != StatusPaid && o.Status != StatusFulfilled {
			return fmt.Errorf("%w: no se puede reembolsar un pedido %s", ErrInvalidState, o.Status)
		}
		if spec.Amount <= 0 {
			return FieldError{"amount", "El monto a reembolsar debe ser mayor a cero"}
		}
		if parts, err = reserveTx(ctx, tx, tenant, orderID, spec.Amount); err != nil {
			return err
		}
		markCredit(parts, spec.ToCredit)
		id = newID("rfd_")
		items, _ := json.Marshal(spec.Items)
		raw, _ := json.Marshal(parts)
		var idem, ret *string
		if spec.IdemKey != "" {
			idem = &spec.IdemKey
		}
		if spec.ReturnID != "" {
			ret = &spec.ReturnID
		}
		_, err = tx.Exec(ctx, `INSERT INTO refunds (tenant_id, id, order_id, kind, status, amount, shipping_amount, currency, reason, items, parts, restock, notify, return_id, idem_key, created_by)
			VALUES ($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			tenant, id, orderID, spec.Kind, spec.Amount, spec.Shipping, o.Currency, spec.Reason, items, raw, spec.Restock, spec.Notify, ret, idem, actor)
		return err
	})
	if err != nil {
		return Refund{}, err
	}
	if existing != nil {
		return *existing, nil
	}
	if perr := s.providerRefund(ctx, tenant, id, attempt, parts); perr != nil {
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			if _, err := lockOrder(ctx, tx, tenant, orderID); err != nil {
				return err
			}
			if err := releaseTx(ctx, tx, tenant, parts); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE refunds SET status='failed', last_error=$3 WHERE tenant_id=$1 AND id=$2 AND status='pending'`, tenant, id, truncate(perr.Error(), 300)); err != nil {
				return err
			}
			return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "refund.failed", map[string]any{"refund": id, "amount": spec.Amount})
		})
		return Refund{}, fmt.Errorf("%w: %v", ErrRefundFailed, perr)
	}
	return s.completeRefund(ctx, tenant, actor, orderID, id, spec.done)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (s *Service) providerRefund(ctx context.Context, tenant, refundID string, attempt int, parts []refundPart) error {
	var acct *Account
	var credit int64
	for _, p := range parts {
		switch p.Provider {
		case "manual":
			continue // money handed back outside the platform; only recorded
		case "gift_card":
			if err := s.walletRefund(ctx, tenant, refundID, p); err != nil {
				return err
			}
			continue
		case "company_credit":
			if err := s.creditRefundPart(ctx, tenant, refundID, p); err != nil {
				return err
			}
			continue
		case "store_credit":
			credit += p.Amount
			continue
		}
		if acct == nil {
			a, err := s.Accounts(ctx, tenant)
			if err != nil {
				return err
			}
			acct = &a
		}
		key := "refund-" + refundID + "-" + p.PaymentID + "-" + strconv.Itoa(attempt)
		if err := acct.Provider.Refund(ctx, acct.Account, p.Ref, p.Amount, key); err != nil {
			return err
		}
	}
	if credit > 0 {
		return s.creditRefund(ctx, tenant, refundID, credit)
	}
	return nil
}

// completeRefund posts a refund whose money already moved.
func (s *Service) completeRefund(ctx context.Context, tenant, actor, orderID, refundID string, done func(context.Context, pgx.Tx, orderRow, *Refund) error) (Refund, error) {
	var out Refund
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, kind, status, amount, tax_amount, shipping_amount, reason, items, COALESCE(return_id,''), last_error, created_by, created_at, completed_at
			FROM refunds WHERE tenant_id=$1 AND id=$2`, tenant, refundID)
		if err != nil {
			return err
		}
		r, err := pgx.CollectExactlyOneRow(rows, scanRefund)
		if err != nil {
			return err
		}
		if r.Status != "pending" {
			out = r
			return nil
		}
		var restock, doNotify bool
		var rawParts []byte
		if err := tx.QueryRow(ctx, `SELECT restock, notify, parts FROM refunds WHERE tenant_id=$1 AND id=$2`, tenant, refundID).Scan(&restock, &doNotify, &rawParts); err != nil {
			return err
		}
		var parts []refundPart
		_ = json.Unmarshal(rawParts, &parts)
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='refunded', updated_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status='captured' AND refunded >= amount AND amount > 0`, tenant, orderID); err != nil {
			return err
		}
		entries := map[string]int64{}
		for _, p := range parts {
			entries[clearing(p.Provider)] -= p.Amount
		}
		switch r.Kind {
		case "adjustment":
			// Overpayment after an edit: the edit already reversed the sale; this returns the cash.
			entries["customer_receivable"] += r.Amount
		case "cancellation":
			// Reverse everything still standing; what was never collected leaves the receivable.
			value := o.GrandTotal - o.RefundedTotal
			r.TaxAmount = max(o.TaxIncluded-o.RefundedTax, 0)
			entries["customer_receivable"] -= value - r.Amount
			entries["sales"] += value - r.TaxAmount
			entries["tax_payable"] += r.TaxAmount
			if _, err := tx.Exec(ctx, `UPDATE orders SET refunded_total=grand_total, refunded_tax=tax_included WHERE tenant_id=$1 AND id=$2`, tenant, orderID); err != nil {
				return err
			}
		default:
			r.TaxAmount = TaxShare(r.Amount, o.GrandTotal, o.TaxIncluded, o.RefundedTotal, o.RefundedTax)
			entries["sales"] += r.Amount - r.TaxAmount
			entries["tax_payable"] += r.TaxAmount
			if _, err := tx.Exec(ctx, `UPDATE orders SET refunded_total=refunded_total+$3, refunded_tax=refunded_tax+$4 WHERE tenant_id=$1 AND id=$2`, tenant, orderID, r.Amount, r.TaxAmount); err != nil {
				return err
			}
			for i, it := range r.Items {
				n, err := s.refundLineTx(ctx, tx, tenant, orderID, it, restock)
				if err != nil {
					return err
				}
				r.Items[i].Restocked = n
			}
		}
		if err := ledger(ctx, tx, tenant, "refund-"+refundID, orderID, o.Currency, entries); err != nil {
			return err
		}
		items, _ := json.Marshal(r.Items)
		if _, err := tx.Exec(ctx, `UPDATE refunds SET status='succeeded', tax_amount=$3, items=$4, completed_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, refundID, r.TaxAmount, items); err != nil {
			return err
		}
		r.Status = "succeeded"
		if done != nil {
			if err := done(ctx, tx, o, &r); err != nil {
				return err
			}
		}
		if doNotify && r.Kind != "cancellation" {
			if err := s.notifyTx(ctx, tx, tenant, orderID, notify.TplRefundIssued, map[string]string{"reembolso.monto": notify.Money(r.Amount, o.Currency), "reembolso.motivo": r.Reason}, "", "refund:"+refundID); err != nil {
				return err
			}
		}
		out = r
		if r.Kind == "cancellation" {
			return nil // the caller's done hook wrote the final order event
		}
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "refund.succeeded", map[string]any{"refund": refundID, "amount": r.Amount, "kind": r.Kind, "reason": r.Reason})
	})
	return out, err
}

// refundLineTx marks units of a line as refunded. With restock, units not yet
// shipped go back to the exact locations they were taken from and leave the order.
func (s *Service) refundLineTx(ctx context.Context, tx pgx.Tx, tenant, orderID string, it RefundItem, restock bool) (int64, error) {
	var qty, fulfilled, cancelled int64
	if err := tx.QueryRow(ctx, `UPDATE order_lines SET refunded_qty=LEAST(qty, refunded_qty+$4) WHERE tenant_id=$1 AND order_id=$2 AND line=$3
		RETURNING qty, fulfilled_qty, cancelled_qty`, tenant, orderID, it.Line, it.Qty).Scan(&qty, &fulfilled, &cancelled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if !restock {
		return 0, nil
	}
	n := min(it.Qty, qty-fulfilled-cancelled)
	if n <= 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE order_lines SET cancelled_qty=cancelled_qty+$4 WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, it.Line, n); err != nil {
		return 0, err
	}
	_, err := restockFromAllocationsTx(ctx, tx, tenant, orderID, it.SKU, n)
	return n, err
}

// restockFromAllocationsTx returns n units of sku to the locations recorded in
// order_allocations (the exact places they were sold from) and shrinks the
// allocations. It reports where each unit went.
func restockFromAllocationsTx(ctx context.Context, tx pgx.Tx, tenant, orderID, sku string, n int64) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT location_id, qty FROM order_allocations WHERE tenant_id=$1 AND order_id=$2 AND sku=$3 ORDER BY location_id FOR UPDATE`, tenant, orderID, sku)
	if err != nil {
		return nil, err
	}
	type alloc struct {
		loc string
		qty int64
	}
	as, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (alloc, error) {
		var a alloc
		return a, r.Scan(&a.loc, &a.qty)
	})
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	left := n
	for _, a := range as {
		if left == 0 {
			break
		}
		take := min(a.qty, left)
		if take == a.qty {
			_, err = tx.Exec(ctx, `DELETE FROM order_allocations WHERE tenant_id=$1 AND order_id=$2 AND sku=$3 AND location_id=$4`, tenant, orderID, sku, a.loc)
		} else {
			_, err = tx.Exec(ctx, `UPDATE order_allocations SET qty=qty-$5 WHERE tenant_id=$1 AND order_id=$2 AND sku=$3 AND location_id=$4`, tenant, orderID, sku, a.loc, take)
		}
		if err != nil {
			return nil, err
		}
		if err := store.RestockTx(ctx, tx, tenant, sku, a.loc, take); err != nil {
			return nil, err
		}
		out[a.loc] += take
		left -= take
	}
	return out, nil
}

// takeStockTx sells qty more units of sku into an existing order (edit,
// exchange): it reserves across locations, records the allocation and
// decrements stock, exactly like a checkout does.
func takeStockTx(ctx context.Context, tx pgx.Tx, tenant, orderID, sku string, qty int64) error {
	holder := newID("take_")
	if err := store.ReserveTx(ctx, tx, tenant, holder, sku, qty, time.Minute); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_allocations (tenant_id, order_id, sku, location_id, qty)
		SELECT tenant_id, $3, sku, location_id, sum(qty) FROM reservations WHERE tenant_id=$1 AND holder=$2 GROUP BY tenant_id, sku, location_id
		ON CONFLICT (tenant_id, order_id, sku, location_id) DO UPDATE SET qty = order_allocations.qty + EXCLUDED.qty`, tenant, holder, orderID); err != nil {
		return err
	}
	return store.CommitTx(ctx, tx, tenant, holder)
}

// Refund gives money back for lines and/or shipping, or an explicit amount.
// idemKey makes the request repeatable: the same key returns the same refund.
func (s *Service) Refund(ctx context.Context, tenant, actor, orderID, idemKey string, in RefundInput) (Refund, error) {
	if len(in.Reason) > 300 {
		return Refund{}, FieldError{"reason", "Motivo de hasta 300 caracteres"}
	}
	q, err := s.RefundQuote(ctx, tenant, orderID, in)
	if err != nil {
		return Refund{}, err
	}
	amount := q.Suggested
	if in.Amount != nil {
		amount = *in.Amount
	}
	if amount <= 0 {
		return Refund{}, FieldError{"amount", "Elige productos, envío o un monto a reembolsar"}
	}
	if amount > q.MaxRefundable {
		return Refund{}, FieldError{"amount", "Lo máximo que puedes reembolsar es " + cents(q.MaxRefundable)}
	}
	if idemKey != "" {
		idemKey = "api:" + idemKey
	}
	return s.refund(ctx, tenant, actor, orderID, refundSpec{Kind: "refund", Amount: amount, Shipping: q.Shipping, Items: q.Items,
		Reason: strings.TrimSpace(in.Reason), Restock: in.Restock, Notify: in.Notify == nil || *in.Notify, IdemKey: idemKey, ToCredit: in.ToCredit})
}

// ReconcileRefunds finishes refunds left pending by a crash: the provider call
// is repeated with the same idempotency key, then the refund is posted.
func (s *Service) ReconcileRefunds(ctx context.Context, after time.Duration) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id, order_id, parts, attempts FROM refunds WHERE status='pending' AND created_at < now() - $1::interval LIMIT 50`,
		fmt.Sprintf("%d seconds", int(after.Seconds())))
	if err != nil {
		return 0, err
	}
	type row struct {
		tenant, id, order string
		parts             []byte
		attempt           int
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.tenant, &x.id, &x.order, &x.parts, &x.attempt)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range list {
		var parts []refundPart
		_ = json.Unmarshal(x.parts, &parts)
		if err := s.providerRefund(ctx, x.tenant, x.id, x.attempt, parts); err != nil {
			continue // provider still unreachable; next round
		}
		if _, err := s.completeRefund(ctx, x.tenant, "reconciler", x.order, x.id, nil); err == nil {
			n++
		}
	}
	return n, nil
}

// Cancel reverts an order completely: every peso still held is refunded
// (idempotent at the provider), remaining units go back to the exact
// locations, the ledger is reversed and the invoice voided.
func (s *Service) Cancel(ctx context.Context, tenant, actor, id, reason string) error {
	var status string
	var held int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		status = o.Status
		if status == StatusPendingPayment {
			if err := store.ReleaseTx(ctx, tx, tenant, id); err != nil {
				return err
			}
			if err := releaseWalletTx(ctx, tx, tenant, actor, id); err != nil {
				return err
			}
			if err := releaseCreditTx(ctx, tx, tenant, actor, id); err != nil {
				return err
			}
			if err := promostore.ReleaseTx(ctx, tx, tenant, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, StatusCancelled); err != nil {
				return err
			}
			return s.event(ctx, tx, tenant, id, actor, "order.cancelled", &status, StatusCancelled, map[string]any{"reason": reason})
		}
		held, err = netPaidTx(ctx, tx, tenant, id)
		return err
	})
	if err != nil {
		return err
	}
	switch status {
	case StatusPendingPayment:
		return nil
	case StatusPaid, StatusFulfilled:
	default:
		return fmt.Errorf("%w: %s", ErrInvalidState, status)
	}
	finish := func(ctx context.Context, tx pgx.Tx, o orderRow, r *Refund) error {
		if o.Status == StatusRefunded {
			return nil // a concurrent cancel already finished
		}
		lines, err := linesTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		for _, l := range lines {
			// Everything still allocated to the order returns to its location.
			if _, err := restockFromAllocationsTx(ctx, tx, tenant, id, l.SKU, 1<<40); err != nil {
				return err
			}
		}
		// The cancellation gives back the promotion and coupon uses of the order.
		if err := promostore.ReleaseTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status='voided', updated_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status IN ('pending','failed')`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE returns SET status='cancelled', closed_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status='requested'`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE fulfillments SET status='cancelled', cancelled_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status='pending'`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE orders SET status=$3, refunded_total=grand_total, refunded_tax=tax_included, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, StatusRefunded); err != nil {
			return err
		}
		amount := int64(0)
		if r != nil {
			amount = r.Amount
			if err := s.notifyTx(ctx, tx, tenant, id, notify.TplRefundIssued, map[string]string{"reembolso.monto": notify.Money(amount, o.Currency), "reembolso.motivo": reason}, "", "refund:"+r.ID); err != nil {
				return err
			}
		}
		return s.event(ctx, tx, tenant, id, actor, "order.refunded", &o.Status, StatusRefunded, map[string]any{"reason": reason, "amount": amount})
	}
	if held > 0 {
		_, err := s.refund(ctx, tenant, actor, id, refundSpec{Kind: "cancellation", Amount: held, Reason: reason, Notify: true, IdemKey: "cancel:" + id, done: finish})
		if errors.Is(err, ErrRefundFailed) {
			return fmt.Errorf("orders: refund failed, order unchanged: %w", err)
		}
		return err
	}
	// Nothing left to refund (already refunded in parts): reverse what stands and close.
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if o.Status != StatusPaid && o.Status != StatusFulfilled {
			return fmt.Errorf("%w: %s", ErrInvalidState, o.Status)
		}
		value, tax := o.GrandTotal-o.RefundedTotal, max(o.TaxIncluded-o.RefundedTax, 0)
		if err := ledger(ctx, tx, tenant, "cancel-"+id, id, o.Currency, map[string]int64{"customer_receivable": -value, "sales": value - tax, "tax_payable": tax}); err != nil {
			return err
		}
		return finish(ctx, tx, o, nil)
	})
}

// ---------- balance: manual payments and payment links ----------

// RecordManualPayment registers money received outside the platform (cash,
// transfer) against the balance of an order.
func (s *Service) RecordManualPayment(ctx context.Context, tenant, actor, orderID string, amount int64, note string) error {
	if len(note) > 200 {
		return FieldError{"note", "Nota de hasta 200 caracteres"}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return s.payBalanceTx(ctx, tx, tenant, actor, orderID, "manual", "manual_"+newID(""), amount, "payment.manual", map[string]any{"note": note})
	})
}

func (s *Service) balanceTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) (orderRow, int64, error) {
	o, err := lockOrder(ctx, tx, tenant, orderID)
	if err != nil {
		return o, 0, err
	}
	if o.Status != StatusPaid && o.Status != StatusFulfilled {
		return o, 0, nil
	}
	net, err := netPaidTx(ctx, tx, tenant, orderID)
	return o, o.GrandTotal - o.RefundedTotal - net, err
}

func (s *Service) payBalanceTx(ctx context.Context, tx pgx.Tx, tenant, actor, orderID, provider, ref string, amount int64, typ string, data map[string]any) error {
	o, balance, err := s.balanceTx(ctx, tx, tenant, orderID)
	if err != nil {
		return err
	}
	if balance <= 0 {
		return fmt.Errorf("%w: el pedido no tiene saldo por cobrar", ErrInvalidState)
	}
	if amount <= 0 || amount > balance {
		return FieldError{"amount", "El pago debe ser mayor a cero y no pasar del saldo: " + notify.Money(balance, o.Currency)}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payments (tenant_id, id, order_id, provider, provider_ref, status, amount, currency) VALUES ($1,$2,$3,$4,$5,'captured',$6,$7)`,
		tenant, newID("pay_"), orderID, provider, ref, amount, o.Currency); err != nil {
		return err
	}
	if err := ledger(ctx, tx, tenant, "balance-"+ref, orderID, o.Currency, map[string]int64{clearing(provider): amount, "customer_receivable": -amount}); err != nil {
		return err
	}
	data["amount"] = amount
	return s.syncStatusTx(ctx, tx, tenant, actor, orderID, typ, data)
}

// SettleCredit refunds a negative balance (the store owes the customer after an edit).
func (s *Service) SettleCredit(ctx context.Context, tenant, actor, orderID string) (Refund, error) {
	var owed int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, b, err := s.balanceTx(ctx, tx, tenant, orderID)
		owed = -b
		return err
	})
	if err != nil {
		return Refund{}, err
	}
	if owed <= 0 {
		return Refund{}, fmt.Errorf("%w: el pedido no tiene saldo a favor del cliente", ErrInvalidState)
	}
	return s.refund(ctx, tenant, actor, orderID, refundSpec{Kind: "adjustment", Amount: owed, Reason: "Diferencia a favor del cliente", Notify: true})
}

// PayLink is what the hosted payment page shows.
type PayLink struct {
	Kind     string         `json:"kind"` // draft | balance
	Number   int64          `json:"number"`
	Amount   int64          `json:"amount"`
	Currency string         `json:"currency"`
	Lines    []ExchangeItem `json:"lines"`
	Paid     bool           `json:"paid"`
	OrderID  string         `json:"orderId,omitempty"`
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// linkTx creates a payment link (replacing earlier unused ones for the same target).
func (s *Service) linkTx(ctx context.Context, tx pgx.Tx, tenant, kind, refID string) (string, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM payment_links WHERE tenant_id=$1 AND kind=$2 AND ref_id=$3 AND used_at IS NULL`, tenant, kind, refID); err != nil {
		return "", err
	}
	token := "apl_" + newID("") + newID("")
	_, err := tx.Exec(ctx, `INSERT INTO payment_links (tenant_id, id, token_hash, kind, ref_id, expires_at) VALUES ($1,$2,$3,$4,$5, now() + interval '7 days')`,
		tenant, newID("lnk_"), tokenHash(token), kind, refID)
	return token, err
}

// LinkURL is the hosted payment page of a token.
func (s *Service) LinkURL(tenant, token string) string {
	return s.PublicURL + "/s/" + tenant + "/main/pagar?t=" + token
}

// SendBalanceLink emails the customer a link to pay the balance of an order.
func (s *Service) SendBalanceLink(ctx context.Context, tenant, actor, orderID string) (string, error) {
	var url string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, balance, err := s.balanceTx(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		if balance <= 0 {
			return fmt.Errorf("%w: el pedido no tiene saldo por cobrar", ErrInvalidState)
		}
		token, err := s.linkTx(ctx, tx, tenant, "balance", orderID)
		if err != nil {
			return err
		}
		url = s.LinkURL(tenant, token)
		if err := s.notifyTx(ctx, tx, tenant, orderID, notify.TplPaymentLink, map[string]string{"pago.monto": notify.Money(balance, o.Currency)}, url, ""); err != nil {
			return err
		}
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "payment_link.sent", map[string]any{"amount": balance})
	})
	return url, err
}

type linkRow struct {
	id, kind, ref string
	used          bool
}

func linkByTokenTx(ctx context.Context, tx pgx.Tx, tenant, token string, lock bool) (linkRow, error) {
	var l linkRow
	if !strings.HasPrefix(token, "apl_") || len(token) > 100 {
		return l, ErrNotFound
	}
	q := `SELECT id, kind, ref_id, used_at IS NOT NULL FROM payment_links WHERE tenant_id=$1 AND token_hash=$2 AND (expires_at > now() OR used_at IS NOT NULL)`
	if lock {
		q += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, q, tenant, tokenHash(token)).Scan(&l.id, &l.kind, &l.ref, &l.used)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// PayLinkInfo describes what a payment link charges.
func (s *Service) PayLinkInfo(ctx context.Context, tenant, token string) (PayLink, error) {
	var p PayLink
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		l, err := linkByTokenTx(ctx, tx, tenant, token, false)
		if err != nil {
			return err
		}
		p.Kind, p.Paid = l.kind, l.used
		if l.kind == "draft" {
			d, err := s.draftTx(ctx, tx, tenant, l.ref, false)
			if err != nil {
				return err
			}
			p.Number, p.Amount, p.Currency, p.OrderID = d.Number, d.GrandTotal, d.Currency, d.OrderID
			p.Paid = p.Paid || d.Status == "completed"
			for _, it := range d.Items {
				p.Lines = append(p.Lines, ExchangeItem{SKU: it.SKU, Title: it.Title, Qty: it.Qty, Unit: it.Unit, Total: it.Unit * it.Qty})
			}
			if d.Status == "cancelled" {
				return ErrNotFound
			}
			return nil
		}
		o, balance, err := s.balanceTx(ctx, tx, tenant, l.ref)
		if err != nil {
			return err
		}
		p.Number, p.Amount, p.Currency, p.OrderID = o.Number, max(balance, 0), o.Currency, l.ref
		p.Paid = p.Paid || balance <= 0
		return nil
	})
	return p, err
}

// PayWithLink charges the payment method for what the link is for. It is safe
// to repeat: the provider idempotency key is the link id.
func (s *Service) PayWithLink(ctx context.Context, tenant, token, paymentMethod string) (PayLink, error) {
	info, err := s.PayLinkInfo(ctx, tenant, token)
	if err != nil || info.Paid {
		return info, err
	}
	var l linkRow
	if err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var e error
		l, e = linkByTokenTx(ctx, tx, tenant, token, false)
		return e
	}); err != nil {
		return info, err
	}
	if l.kind == "draft" {
		o, err := s.CompleteDraft(ctx, tenant, "shopper:link", l.ref, paymentMethod)
		if err != nil {
			return info, err
		}
		info.Paid, info.OrderID = true, o.ID
	} else {
		acct, err := s.Accounts(ctx, tenant)
		if err != nil {
			return info, ErrNoPaymentAccount
		}
		var c string
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			if cu, err := s.customerTx(ctx, tx, tenant, l.ref); err == nil {
				c = cu.Email
			}
			return nil
		})
		ch, err := acct.Provider.Charge(ctx, payments.ChargeRequest{Account: acct.Account, OrderID: l.id, Amount: info.Amount, Currency: info.Currency,
			PaymentMethod: paymentMethod, FeeAmount: info.Amount * acct.FeeBps / 10000, IdempotencyKey: "charge-" + l.id, Email: c})
		if err != nil {
			return info, fmt.Errorf("%w: %v", ErrDeclined, err)
		}
		if ch.Status != "captured" {
			return info, fmt.Errorf("%w: %s", ErrDeclined, ch.Reason)
		}
		err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			return s.payBalanceTx(ctx, tx, tenant, "shopper:link", l.ref, acct.Provider.Name(), ch.Ref, info.Amount, "payment.balance_paid", map[string]any{"ref": ch.Ref})
		})
		if err != nil {
			return info, err
		}
		info.Paid = true
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE payment_links SET used_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, l.id)
		return e
	})
	return info, err
}
