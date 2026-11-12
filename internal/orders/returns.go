package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// ItemInput asks for qty units of a SKU; Unit overrides the channel price.
type ItemInput struct {
	SKU  string `json:"sku"`
	Qty  int64  `json:"qty"`
	Unit *int64 `json:"unit"`
}

// priceItems resolves title and unit price of items from the store's read models.
func (s *Service) priceItems(tenant, channel, field string, items []ItemInput) ([]ExchangeItem, error) {
	t, ok := s.Projector.Tenant(tenant)
	if !ok {
		return nil, fmt.Errorf("orders: tenant %s not loaded", tenant)
	}
	if len(items) > 100 {
		return nil, FieldError{field, "Máximo 100 productos"}
	}
	out := make([]ExchangeItem, 0, len(items))
	seen := map[string]bool{}
	for i, it := range items {
		f := fmt.Sprintf("%s[%d]", field, i)
		if it.Qty < 1 || it.Qty > 10000 {
			return nil, FieldError{f + ".qty", "La cantidad debe estar entre 1 y 10,000"}
		}
		if seen[it.SKU] {
			return nil, FieldError{f + ".sku", "SKU repetido: " + it.SKU}
		}
		seen[it.SKU] = true
		v, p, err := t.Catalog.VariantBySKU(it.SKU)
		if err != nil {
			return nil, FieldError{f + ".sku", "No existe el SKU " + it.SKU}
		}
		e := ExchangeItem{SKU: it.SKU, Title: strings.TrimSpace(p.Title + " " + v.Title), Qty: it.Qty}
		if it.Unit != nil {
			if *it.Unit < 0 || *it.Unit > 1e12 {
				return nil, FieldError{f + ".unit", "Precio inválido"}
			}
			e.Unit = *it.Unit
		} else {
			q, ok := t.Pricing.Resolve(channel, it.SKU, it.Qty)
			if !ok {
				return nil, FieldError{f + ".unit", "El SKU " + it.SKU + " no tiene precio en este canal; escribe uno"}
			}
			e.Unit = q.Unit.Amount
		}
		e.Total = e.Unit * e.Qty
		out = append(out, e)
	}
	return out, nil
}

// ReturnItemInput asks to return qty units of a line.
type ReturnItemInput struct {
	Line   int    `json:"line"`
	Qty    int64  `json:"qty"`
	Reason string `json:"reason"`
}

// ReturnInput creates a return (RMA). With Exchange items it is an exchange:
// the value of the returned items pays for the new ones.
type ReturnInput struct {
	Items          []ReturnItemInput `json:"items"`
	Note           string            `json:"note"`
	RefundShipping int64             `json:"refundShipping"`
	Exchange       []ItemInput       `json:"exchange"`
}

const returnCols = `r.id, r.order_id, r.number, r.kind, r.status, r.note, r.refund_shipping, r.credit, r.credit_applied, r.refund_amount, COALESCE(r.refund_id,''),
	r.new_items, r.created_by, r.created_at, r.received_at, r.closed_at, o.number, o.currency`

func returnsTx(ctx context.Context, tx pgx.Tx, tenant, cond string, args ...any) ([]Return, error) {
	rows, err := tx.Query(ctx, `SELECT `+returnCols+` FROM returns r JOIN orders o ON o.tenant_id=r.tenant_id AND o.id=r.order_id
		WHERE r.tenant_id=$1 AND `+cond, append([]any{tenant}, args...)...)
	if err != nil {
		return nil, err
	}
	rs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Return, error) {
		var x Return
		var items []byte
		err := r.Scan(&x.ID, &x.OrderID, &x.Number, &x.Kind, &x.Status, &x.Note, &x.RefundShipping, &x.Credit, &x.CreditApplied, &x.RefundAmount, &x.RefundID,
			&items, &x.CreatedBy, &x.CreatedAt, &x.ReceivedAt, &x.ClosedAt, &x.OrderNumber, &x.Currency)
		_ = json.Unmarshal(items, &x.NewItems)
		if x.NewItems == nil {
			x.NewItems = []ExchangeItem{}
		}
		return x, err
	})
	if err != nil {
		return nil, err
	}
	for i := range rs {
		rows, err := tx.Query(ctx, `SELECT line, sku, title, qty, reason_code, received_qty, restock, restock_location FROM return_items WHERE tenant_id=$1 AND return_id=$2 ORDER BY line`, tenant, rs[i].ID)
		if err != nil {
			return nil, err
		}
		rs[i].Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReturnItem, error) {
			var it ReturnItem
			return it, r.Scan(&it.Line, &it.SKU, &it.Title, &it.Qty, &it.Reason, &it.ReceivedQty, &it.Restock, &it.RestockLocation)
		})
		if err != nil {
			return nil, err
		}
	}
	return rs, nil
}

// RMA is the human number of a return.
func RMA(n int64) string { return "RMA-" + strconv.FormatInt(n, 10) }

// Returns lists returns of the store, newest first; status filters when not empty.
func (s *Service) Returns(ctx context.Context, tenant, status string, before int64, limit int) ([]Return, int64, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var out []Return
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = returnsTx(ctx, tx, tenant, `($2 = '' OR r.status = $2) AND ($3 = 0 OR r.number < $3) ORDER BY r.number DESC LIMIT $4`, status, before, limit+1)
		return err
	})
	next := int64(0)
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].Number
	}
	if out == nil {
		out = []Return{}
	}
	return out, next, err
}

// GetReturn loads one return.
func (s *Service) GetReturn(ctx context.Context, tenant, id string) (Return, error) {
	var out Return
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rs, err := returnsTx(ctx, tx, tenant, `r.id=$2`, id)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			return ErrNotFound
		}
		out = rs[0]
		return nil
	})
	return out, err
}

// addValueTx raises the worth of an order by d (new lines of an edit or
// exchange) and posts it against customer_receivable. tax grows in proportion.
func addValueTx(ctx context.Context, tx pgx.Tx, tenant, orderID, txID string, o orderRow, after Totals) error {
	d := after.GrandTotal - o.GrandTotal
	taxD := after.TaxIncluded - o.TaxIncluded
	if _, err := tx.Exec(ctx, `UPDATE orders SET items_total=$3, discounts=$4, shipping=$5, tax_included=$6, grand_total=$7, tax_adjust=$7::bigint - $3::bigint + $4::bigint - $5::bigint - fees WHERE tenant_id=$1 AND id=$2`,
		tenant, orderID, after.ItemsTotal, after.Discounts, after.Shipping, after.TaxIncluded, after.GrandTotal); err != nil {
		return err
	}
	if d == 0 && taxD == 0 {
		return nil
	}
	return ledger(ctx, tx, tenant, txID, orderID, o.Currency, map[string]int64{"customer_receivable": d, "sales": -(d - taxD), "tax_payable": -taxD})
}

// scaleTax keeps the tax rate of the order when its total changes.
func scaleTax(o orderRow, newGrand int64) int64 {
	if o.GrandTotal <= 0 || newGrand <= 0 {
		return 0
	}
	return newGrand * o.TaxIncluded / o.GrandTotal
}

// CreateReturn opens an RMA for shipped units. For an exchange, the new items
// are added to the order right away (stock is taken) and the value of the
// returned items is applied as credit; whatever credit is left over is
// refunded when the return is received.
func (s *Service) CreateReturn(ctx context.Context, tenant, actor, orderID string, in ReturnInput) (Return, error) {
	if len(in.Items) == 0 {
		return Return{}, FieldError{"items", "Elige al menos un producto a devolver"}
	}
	if len(in.Note) > 500 {
		return Return{}, FieldError{"note", "Nota de hasta 500 caracteres"}
	}
	id := newID("ret_")
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		if o.Status != StatusPaid && o.Status != StatusFulfilled {
			return fmt.Errorf("%w: no se puede devolver un pedido %s", ErrInvalidState, o.Status)
		}
		lines, err := linesTx(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		sr, err := shippingRefundedTx(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		if in.RefundShipping < 0 || in.RefundShipping > o.Shipping-sr {
			return FieldError{"refundShipping", "El envío por reembolsar no puede pasar de " + notify.Money(max(o.Shipping-sr, 0), o.Currency)}
		}
		byLine := map[int]Line{}
		for _, l := range lines {
			byLine[l.Line] = l
		}
		var credit int64
		var items []ReturnItem
		seen := map[int]bool{}
		for i, it := range in.Items {
			f := fmt.Sprintf("items[%d]", i)
			l, ok := byLine[it.Line]
			if !ok || seen[it.Line] {
				return FieldError{f + ".line", "Línea desconocida o repetida"}
			}
			seen[it.Line] = true
			if !slices.Contains(ReasonCodes, it.Reason) {
				return FieldError{f + ".reason", "Elige un motivo de devolución"}
			}
			if left := l.FulfilledQty - l.ReturnedQty; it.Qty < 1 || it.Qty > left {
				return FieldError{f + ".qty", fmt.Sprintf("De %s se pueden devolver hasta %d piezas (solo las ya enviadas)", l.Title, left)}
			}
			net := LineNet(l.Total, o.ItemsTotal, o.Discounts)
			credit += UnitsValue(net, l.Qty, l.RefundedQty+it.Qty) - UnitsValue(net, l.Qty, l.RefundedQty)
			items = append(items, ReturnItem{Line: l.Line, SKU: l.SKU, Title: l.Title, Qty: it.Qty, Reason: it.Reason})
			if _, err := tx.Exec(ctx, `UPDATE order_lines SET returned_qty=returned_qty+$4 WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, l.Line, it.Qty); err != nil {
				return err
			}
		}
		kind, typ := "return", "return.requested"
		var newItems []ExchangeItem
		var applied int64
		if len(in.Exchange) > 0 {
			kind, typ = "exchange", "exchange.requested"
			if newItems, err = s.priceItems(tenant, o.Channel, "exchange", in.Exchange); err != nil {
				return err
			}
			var maxLine int
			for _, l := range lines {
				maxLine = max(maxLine, l.Line)
			}
			var added int64
			for i := range newItems {
				maxLine++
				newItems[i].Line = maxLine
				if _, err := tx.Exec(ctx, `INSERT INTO order_lines (tenant_id, order_id, line, sku, title, qty, unit, total, origin) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'exchange')`,
					tenant, orderID, maxLine, newItems[i].SKU, newItems[i].Title, newItems[i].Qty, newItems[i].Unit, newItems[i].Total); err != nil {
					return err
				}
				if err := takeStockTx(ctx, tx, tenant, orderID, newItems[i].SKU, newItems[i].Qty); err != nil {
					return err
				}
				added += newItems[i].Total
			}
			after := o.Totals
			after.ItemsTotal += added
			after.GrandTotal += added
			after.TaxIncluded = scaleTax(o, after.GrandTotal)
			if o.GrandTotal == 0 {
				after.TaxIncluded = o.TaxIncluded
			}
			if err := addValueTx(ctx, tx, tenant, orderID, "exchange-"+id, o, after); err != nil {
				return err
			}
			// The credit of the returned items pays for the new ones; no money moves for that part.
			if applied = min(credit, added); applied > 0 {
				tax := TaxShare(applied, after.GrandTotal, after.TaxIncluded, o.RefundedTotal, o.RefundedTax)
				rid := newID("rfd_")
				if _, err := tx.Exec(ctx, `INSERT INTO refunds (tenant_id, id, order_id, kind, status, amount, tax_amount, currency, reason, return_id, created_by, completed_at)
					VALUES ($1,$2,$3,'credit','succeeded',$4,$5,$6,'Crédito aplicado al cambio',$7,$8, now())`, tenant, rid, orderID, applied, tax, o.Currency, id, actor); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE orders SET refunded_total=refunded_total+$3, refunded_tax=refunded_tax+$4 WHERE tenant_id=$1 AND id=$2`, tenant, orderID, applied, tax); err != nil {
					return err
				}
				if err := ledger(ctx, tx, tenant, "credit-"+rid, orderID, o.Currency, map[string]int64{"customer_receivable": -applied, "sales": applied - tax, "tax_payable": tax}); err != nil {
					return err
				}
			}
		}
		var number int64
		if err := tx.QueryRow(ctx, `SELECT nextval('return_numbers')`).Scan(&number); err != nil {
			return err
		}
		raw, _ := json.Marshal(newItems)
		if newItems == nil {
			raw = []byte("[]")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO returns (tenant_id, id, order_id, number, kind, status, note, refund_shipping, credit, credit_applied, new_items, created_by)
			VALUES ($1,$2,$3,$4,$5,'requested',$6,$7,$8,$9,$10,$11)`, tenant, id, orderID, number, kind, strings.TrimSpace(in.Note), in.RefundShipping, credit, applied, raw, actor); err != nil {
			return err
		}
		for _, it := range items {
			if _, err := tx.Exec(ctx, `INSERT INTO return_items (tenant_id, return_id, line, sku, title, qty, reason_code) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				tenant, id, it.Line, it.SKU, it.Title, it.Qty, it.Reason); err != nil {
				return err
			}
		}
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, typ, map[string]any{"return": id, "number": RMA(number), "credit": credit, "creditApplied": applied})
	})
	if errors.Is(err, store.ErrInsufficient) {
		return Return{}, FieldError{"exchange", "No hay existencias suficientes de un producto del cambio"}
	}
	if err != nil {
		return Return{}, err
	}
	return s.GetReturn(ctx, tenant, id)
}

// ReceiveItem is the warehouse decision for one returned line.
type ReceiveItem struct {
	Line    int  `json:"line"`
	Restock bool `json:"restock"`
}

// ReceiveInput records the arrival of a return. Lines not listed are not restocked.
type ReceiveInput struct {
	Items      []ReceiveItem `json:"items"`
	SkipRefund bool          `json:"skipRefund"` // receive now, refund later
}

// ReceiveReturn records that the returned items arrived. Each item goes back
// to the exact location it was sold from when restock is chosen, the customer
// is told, and the money still owed for the return is refunded (refund on receipt).
func (s *Service) ReceiveReturn(ctx context.Context, tenant, actor, returnID string, in ReceiveInput) (Return, error) {
	restock := map[int]bool{}
	for _, it := range in.Items {
		restock[it.Line] = it.Restock
	}
	var orderID string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT order_id FROM returns WHERE tenant_id=$1 AND id=$2`, tenant, returnID).Scan(&orderID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		o, err := lockOrder(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		rs, err := returnsTx(ctx, tx, tenant, `r.id=$2`, returnID)
		if err != nil || len(rs) == 0 {
			return err
		}
		r := rs[0]
		if r.Status != "requested" {
			return fmt.Errorf("%w: la devolución ya fue %s", ErrInvalidState, map[string]string{"received": "recibida", "completed": "cerrada", "cancelled": "cancelada"}[r.Status])
		}
		restocked := map[string]any{}
		for _, it := range r.Items {
			loc := ""
			if restock[it.Line] {
				where, err := restockFromAllocationsTx(ctx, tx, tenant, orderID, it.SKU, it.Qty)
				if err != nil {
					return err
				}
				var locs []string
				for l := range where {
					locs = append(locs, l)
				}
				slices.Sort(locs)
				loc = strings.Join(locs, ",")
				restocked[it.SKU] = where
			}
			if _, err := tx.Exec(ctx, `UPDATE return_items SET received_qty=qty, restock=$4, restock_location=$5 WHERE tenant_id=$1 AND return_id=$2 AND line=$3`,
				tenant, returnID, it.Line, restock[it.Line], loc); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE returns SET status='received', received_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, returnID); err != nil {
			return err
		}
		due := r.Credit - r.CreditApplied + r.RefundShipping
		resolution := "Revisaremos los productos y te contactaremos."
		switch {
		case due > 0 && !in.SkipRefund:
			resolution = "Reembolsamos " + notify.Money(due, o.Currency) + " a tu método de pago; tu banco puede tardar de 5 a 10 días hábiles en reflejarlo."
		case r.Kind == "exchange":
			resolution = "Tu cambio queda completo con los productos nuevos de tu pedido."
		}
		if err := s.notifyTx(ctx, tx, tenant, orderID, notify.TplReturnReceived, map[string]string{"devolucion.numero": RMA(r.Number), "devolucion.resolucion": resolution}, "", "return:"+returnID); err != nil {
			return err
		}
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "return.received", map[string]any{"return": returnID, "number": RMA(r.Number), "restocked": restocked})
	})
	if err != nil {
		return Return{}, err
	}
	if !in.SkipRefund {
		if err := s.RefundReturn(ctx, tenant, actor, returnID); err != nil {
			// The items are in; the refund can be retried from the return.
			r, gerr := s.GetReturn(ctx, tenant, returnID)
			if gerr != nil {
				return r, err
			}
			return r, err
		}
	}
	return s.GetReturn(ctx, tenant, returnID)
}

// RefundReturn pays what a received return still owes the customer and closes
// it. It is idempotent per return, so it can be retried after a provider error.
func (s *Service) RefundReturn(ctx context.Context, tenant, actor, returnID string) error {
	var r Return
	var held int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rs, err := returnsTx(ctx, tx, tenant, `r.id=$2`, returnID)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			return ErrNotFound
		}
		r = rs[0]
		held, err = netPaidTx(ctx, tx, tenant, r.OrderID)
		return err
	})
	if err != nil {
		return err
	}
	if r.Status != "received" {
		return fmt.Errorf("%w: primero registra la recepción de la devolución", ErrInvalidState)
	}
	closeTx := func(ctx context.Context, tx pgx.Tx, refund *Refund) error {
		for _, it := range r.Items {
			if _, err := tx.Exec(ctx, `UPDATE order_lines SET refunded_qty=LEAST(qty, refunded_qty+$4) WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, r.OrderID, it.Line, it.Qty); err != nil {
				return err
			}
		}
		var rid *string
		var amount int64
		if refund != nil {
			rid, amount = &refund.ID, refund.Amount
		}
		_, err := tx.Exec(ctx, `UPDATE returns SET status='completed', closed_at=now(), refund_id=$3, refund_amount=$4 WHERE tenant_id=$1 AND id=$2`, tenant, returnID, rid, amount)
		return err
	}
	// Never refund more than the money actually held (e.g. an exchange with a balance still unpaid).
	due := min(r.Credit-r.CreditApplied+r.RefundShipping, held)
	if due <= 0 {
		return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			if _, err := lockOrder(ctx, tx, tenant, r.OrderID); err != nil {
				return err
			}
			if err := closeTx(ctx, tx, nil); err != nil {
				return err
			}
			return s.syncStatusTx(ctx, tx, tenant, actor, r.OrderID, "return.completed", map[string]any{"return": returnID, "number": RMA(r.Number), "refund": 0})
		})
	}
	var items []RefundItem
	_, err = s.refund(ctx, tenant, actor, r.OrderID, refundSpec{Kind: "refund", Amount: due, Shipping: min(r.RefundShipping, due), Items: items,
		Reason: "Devolución " + RMA(r.Number), Notify: false, ReturnID: returnID, IdemKey: "return:" + returnID,
		done: func(ctx context.Context, tx pgx.Tx, _ orderRow, refund *Refund) error {
			return closeTx(ctx, tx, refund)
		}})
	return err
}

// CancelReturn withdraws a return that has not been received. Exchanges
// cannot be cancelled: their new items are already part of the order.
func (s *Service) CancelReturn(ctx context.Context, tenant, actor, returnID string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var orderID string
		if err := tx.QueryRow(ctx, `SELECT order_id FROM returns WHERE tenant_id=$1 AND id=$2`, tenant, returnID).Scan(&orderID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := lockOrder(ctx, tx, tenant, orderID); err != nil {
			return err
		}
		rs, err := returnsTx(ctx, tx, tenant, `r.id=$2`, returnID)
		if err != nil || len(rs) == 0 {
			return err
		}
		r := rs[0]
		if r.Status != "requested" {
			return fmt.Errorf("%w: solo se cancela una devolución que no se ha recibido", ErrInvalidState)
		}
		if r.Kind == "exchange" {
			return fmt.Errorf("%w: un cambio no se cancela porque los productos nuevos ya son parte del pedido; recíbelo o reembolsa", ErrInvalidState)
		}
		for _, it := range r.Items {
			if _, err := tx.Exec(ctx, `UPDATE order_lines SET returned_qty=GREATEST(returned_qty-$4, 0) WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, it.Line, it.Qty); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE returns SET status='cancelled', closed_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, returnID); err != nil {
			return err
		}
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "return.cancelled", map[string]any{"return": returnID, "number": RMA(r.Number)})
	})
}
