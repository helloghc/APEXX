package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// EditInput changes the items of a paid order before anything ships.
type EditInput struct {
	Add    []ItemInput  `json:"add"`
	Remove []RefundLine `json:"remove"` // line + qty to take out
	Reason string       `json:"reason"`
	DryRun bool         `json:"dryRun"` // compute the result without saving
}

// EditResult is the outcome (or preview) of an edit.
type EditResult struct {
	Before     Totals  `json:"before"`
	After      Totals  `json:"after"`
	Difference int64   `json:"difference"` // > 0: the customer owes; < 0: refunded to the customer
	Lines      []Line  `json:"lines"`
	Refund     *Refund `json:"refund,omitempty"`
	RefundErr  string  `json:"refundError,omitempty"`
	EditID     string  `json:"editId,omitempty"`
}

var errDryRun = errors.New("dry run")

func editsTx(ctx context.Context, tx pgx.Tx, tenant, id string) ([]Edit, error) {
	rows, err := tx.Query(ctx, `SELECT id, changes, before, after, difference, reason, created_by, created_at FROM order_edits WHERE tenant_id=$1 AND order_id=$2 ORDER BY created_at, id`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Edit, error) {
		var e Edit
		var ch, b, a []byte
		err := r.Scan(&e.ID, &ch, &b, &a, &e.Difference, &e.Reason, &e.CreatedBy, &e.CreatedAt)
		e.Changes = ch
		_ = json.Unmarshal(b, &e.Before)
		_ = json.Unmarshal(a, &e.After)
		return e, err
	})
}

// EditOrder adds and removes items of a paid, unshipped order. Removed units
// return to the exact locations they came from; added units are taken from
// stock. Shipping and the discount amount stay as they were (the discount is
// capped at the new items total). The price difference is handled explicitly:
// if the new total is lower, the overpayment is refunded at once; if it is
// higher, the order shows a balance that is collected with a payment link or a
// manual payment.
func (s *Service) EditOrder(ctx context.Context, tenant, actor, orderID string, in EditInput) (EditResult, error) {
	var res EditResult
	if len(in.Add) == 0 && len(in.Remove) == 0 {
		return res, FieldError{"add", "Agrega o quita al menos un producto"}
	}
	if len(in.Reason) > 300 {
		return res, FieldError{"reason", "Motivo de hasta 300 caracteres"}
	}
	editID := newID("edt_")
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		if o.Status != StatusPaid {
			return fmt.Errorf("%w: solo se edita un pedido pagado que no se ha enviado", ErrInvalidState)
		}
		lines, err := linesTx(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		var busy int64
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM fulfillments WHERE tenant_id=$1 AND order_id=$2 AND status <> 'cancelled')
			+ (SELECT count(*) FROM returns WHERE tenant_id=$1 AND order_id=$2 AND status <> 'cancelled')
			+ (SELECT count(*) FROM refunds WHERE tenant_id=$1 AND order_id=$2 AND status = 'pending')`, tenant, orderID).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 || o.RefundedTotal > 0 {
			return fmt.Errorf("%w: el pedido ya tiene envíos, devoluciones o reembolsos; usa un reembolso o un cambio", ErrInvalidState)
		}
		byLine := map[int]*Line{}
		maxLine := 0
		for i := range lines {
			byLine[lines[i].Line] = &lines[i]
			maxLine = max(maxLine, lines[i].Line)
		}
		type change struct {
			Op    string `json:"op"`
			Line  int    `json:"line"`
			SKU   string `json:"sku"`
			Title string `json:"title"`
			Qty   int64  `json:"qty"`
			Unit  int64  `json:"unit"`
		}
		var changes []change
		seen := map[int]bool{}
		for i, rm := range in.Remove {
			l, ok := byLine[rm.Line]
			if !ok || seen[rm.Line] {
				return FieldError{fmt.Sprintf("remove[%d].line", i), "Línea desconocida o repetida"}
			}
			seen[rm.Line] = true
			if rm.Qty < 1 || rm.Qty > l.Qty {
				return FieldError{fmt.Sprintf("remove[%d].qty", i), fmt.Sprintf("De %s puedes quitar hasta %d piezas", l.Title, l.Qty)}
			}
			l.Qty -= rm.Qty
			l.Total = l.Unit * l.Qty
			changes = append(changes, change{"remove", l.Line, l.SKU, l.Title, rm.Qty, l.Unit})
			if !in.DryRun {
				if _, err := restockFromAllocationsTx(ctx, tx, tenant, orderID, l.SKU, rm.Qty); err != nil {
					return err
				}
				if l.Qty == 0 {
					_, err = tx.Exec(ctx, `DELETE FROM order_lines WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, l.Line)
				} else {
					_, err = tx.Exec(ctx, `UPDATE order_lines SET qty=$4, total=$5 WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, l.Line, l.Qty, l.Total)
				}
				if err != nil {
					return err
				}
			}
		}
		added, err := s.priceItems(tenant, o.Channel, "add", in.Add)
		if err != nil {
			return err
		}
		for _, a := range added {
			// Same SKU at the same price joins its line; otherwise it is a new line.
			var into *Line
			for i := range lines {
				if lines[i].SKU == a.SKU && lines[i].Unit == a.Unit && lines[i].Qty > 0 {
					into = &lines[i]
				}
			}
			if !in.DryRun {
				if err := takeStockTx(ctx, tx, tenant, orderID, a.SKU, a.Qty); err != nil {
					return err
				}
			}
			if into != nil {
				into.Qty += a.Qty
				into.Total = into.Unit * into.Qty
				changes = append(changes, change{"add", into.Line, a.SKU, a.Title, a.Qty, a.Unit})
				if !in.DryRun {
					if _, err := tx.Exec(ctx, `UPDATE order_lines SET qty=$4, total=$5 WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, into.Line, into.Qty, into.Total); err != nil {
						return err
					}
				}
				continue
			}
			maxLine++
			lines = append(lines, Line{Line: maxLine, SKU: a.SKU, Title: a.Title, Qty: a.Qty, Unit: a.Unit, Total: a.Total, Origin: "edit"})
			changes = append(changes, change{"add", maxLine, a.SKU, a.Title, a.Qty, a.Unit})
			if !in.DryRun {
				if _, err := tx.Exec(ctx, `INSERT INTO order_lines (tenant_id, order_id, line, sku, title, qty, unit, total, origin) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'edit')`,
					tenant, orderID, maxLine, a.SKU, a.Title, a.Qty, a.Unit, a.Total); err != nil {
					return err
				}
			}
		}
		after := Totals{Shipping: o.Shipping}
		for _, l := range lines {
			if l.Qty > 0 {
				after.ItemsTotal += l.Total
				res.Lines = append(res.Lines, l)
			}
		}
		if len(res.Lines) == 0 {
			return FieldError{"remove", "El pedido no puede quedar vacío; cancélalo si ya no se va a surtir"}
		}
		after.Discounts = min(o.Discounts, after.ItemsTotal)
		after.GrandTotal = after.ItemsTotal - after.Discounts + after.Shipping
		after.TaxIncluded = scaleTax(o, after.GrandTotal)
		res.Before, res.After, res.Difference = o.Totals, after, after.GrandTotal-o.GrandTotal
		if in.DryRun {
			return errDryRun
		}
		if err := addValueTx(ctx, tx, tenant, orderID, "edit-"+editID, o, after); err != nil {
			return err
		}
		ch, _ := json.Marshal(changes)
		b, _ := json.Marshal(res.Before)
		a, _ := json.Marshal(res.After)
		if _, err := tx.Exec(ctx, `INSERT INTO order_edits (tenant_id, id, order_id, changes, before, after, difference, reason, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			tenant, editID, orderID, ch, b, a, res.Difference, strings.TrimSpace(in.Reason), actor); err != nil {
			return err
		}
		res.EditID = editID
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "order.edited", map[string]any{"edit": editID, "difference": res.Difference, "changes": len(changes), "reason": in.Reason})
	})
	if errors.Is(err, errDryRun) {
		return res, nil
	}
	if errors.Is(err, store.ErrInsufficient) {
		return res, FieldError{"add", "No hay existencias suficientes de un producto agregado"}
	}
	if err != nil {
		return res, err
	}
	if res.Difference < 0 {
		// The customer paid more than the order is now worth: give the difference back.
		r, rerr := s.refund(ctx, tenant, actor, orderID, refundSpec{Kind: "adjustment", Amount: -res.Difference, Reason: "Diferencia por edición del pedido", Notify: true, IdemKey: "edit:" + editID})
		if rerr != nil {
			res.RefundErr = "No se pudo reembolsar la diferencia; reintenta desde el pedido"
		} else {
			res.Refund = &r
		}
	}
	return res, nil
}
