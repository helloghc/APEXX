package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// Marketplace: a seller owns products, the store charges the shopper and
// keeps a commission. Every paid line of a seller's product adds to the
// seller's statement; refunds take it back in the same proportion.

// Seller is a third party that sells through the store.
type Seller struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	CommissionBps int64     `json:"commissionBps"`
	Status        string    `json:"status"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"createdAt"`
	Products      int       `json:"products"`
	Balance       int64     `json:"balance"`    // what the store owes the seller now
	Sales         int64     `json:"sales"`      // gross sold, net of refunds
	Commission    int64     `json:"commission"` // commission earned by the store
	// Automatic payouts.
	PayoutAccount  string `json:"payoutAccount"`  // the seller's connected account at the payment provider
	PayoutAuto     bool   `json:"payoutAuto"`     // pay by itself when there is enough to pay
	PayoutMin      int64  `json:"payoutMin"`      // do not send less than this
	PayoutHoldDays int    `json:"payoutHoldDays"` // days a sale waits, so returns come out of it first
	Payable        int64  `json:"payable"`        // what can be sent today
}

// SellerEntry is one movement of a seller's statement.
type SellerEntry struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	OrderID    string    `json:"orderId,omitempty"`
	Number     int64     `json:"orderNumber,omitempty"`
	Line       int       `json:"line,omitempty"`
	Gross      int64     `json:"gross"`
	Commission int64     `json:"commission"`
	Net        int64     `json:"net"`
	Note       string    `json:"note,omitempty"`
	At         time.Time `json:"at"`
}

func (in *Seller) clean() error {
	in.Name, in.Email, in.Note = strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.Note)
	if in.Status == "" {
		in.Status = "active"
	}
	switch {
	case in.Name == "" || len(in.Name) > 120:
		return FieldError{"name", "Escribe el nombre del vendedor"}
	case in.Email != "" && (!strings.Contains(in.Email, "@") || len(in.Email) > 200):
		return FieldError{"email", "Escribe un correo válido"}
	case in.CommissionBps < 0 || in.CommissionBps > 10000:
		return FieldError{"commissionBps", "La comisión debe estar entre 0% y 100%"}
	case in.Status != "active" && in.Status != "paused":
		return FieldError{"status", "Estado desconocido"}
	case len(in.Note) > 500:
		return FieldError{"note", "Nota de hasta 500 caracteres"}
	}
	in.PayoutAccount = strings.TrimSpace(in.PayoutAccount)
	switch {
	case len(in.PayoutAccount) > 100 || strings.ContainsAny(in.PayoutAccount, " \t\n"):
		return FieldError{"payoutAccount", "Escribe el identificador de la cuenta tal como lo da Stripe, por ejemplo acct_…"}
	case in.PayoutAuto && in.PayoutAccount == "":
		return FieldError{"payoutAccount", "Para pagar en automático hace falta la cuenta de Stripe del vendedor"}
	case in.PayoutMin < 0:
		return FieldError{"payoutMin", "El mínimo no puede ser negativo"}
	case in.PayoutHoldDays < 0 || in.PayoutHoldDays > 90:
		return FieldError{"payoutHoldDays", "La espera debe estar entre 0 y 90 días"}
	}
	return nil
}

const sellerSelect = `SELECT s.id, s.name, s.email, s.commission_bps, s.status, s.note, s.created_at,
	(SELECT count(*) FROM seller_products p WHERE p.tenant_id=s.tenant_id AND p.seller_id=s.id)::int,
	COALESCE(e.net,0), COALESCE(e.gross,0), COALESCE(e.commission,0),
	s.payout_account, s.payout_auto, s.payout_min, s.payout_hold_days,
	GREATEST(0, COALESCE(e.net,0) - COALESCE((SELECT sum(y.net) FROM seller_entries y WHERE y.tenant_id=s.tenant_id AND y.seller_id=s.id AND y.kind='sale' AND y.net > 0 AND y.created_at > now() - make_interval(days => s.payout_hold_days)),0))
	FROM sellers s LEFT JOIN (SELECT seller_id, sum(net) AS net, sum(gross) AS gross, sum(commission) AS commission FROM seller_entries WHERE tenant_id=$1 GROUP BY seller_id) e ON e.seller_id=s.id
	WHERE s.tenant_id=$1`

func scanSeller(r pgx.Row) (Seller, error) {
	var x Seller
	return x, r.Scan(&x.ID, &x.Name, &x.Email, &x.CommissionBps, &x.Status, &x.Note, &x.CreatedAt, &x.Products, &x.Balance, &x.Sales, &x.Commission, &x.PayoutAccount, &x.PayoutAuto, &x.PayoutMin, &x.PayoutHoldDays, &x.Payable)
}

// Sellers lists the sellers of a store with their balances.
func (s *Service) Sellers(ctx context.Context, tenant string) ([]Seller, error) {
	var out []Seller
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sellerSelect+` ORDER BY s.name`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			x, err := scanSeller(rows)
			if err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// Seller returns one seller with its product ids.
func (s *Service) Seller(ctx context.Context, tenant, id string) (Seller, []string, error) {
	var x Seller
	var products []string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, err = scanSeller(tx.QueryRow(ctx, sellerSelect+` AND s.id=$2`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT product_id FROM seller_products WHERE tenant_id=$1 AND seller_id=$2 ORDER BY product_id`, tenant, id)
		if err != nil {
			return err
		}
		products, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return x, products, err
}

// SaveSeller creates (empty id) or updates a seller. A new commission applies
// to future sales; lines already sold keep the rate they were sold with.
func (s *Service) SaveSeller(ctx context.Context, tenant, actor string, in Seller) (Seller, error) {
	if err := in.clean(); err != nil {
		return Seller{}, err
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if in.ID == "" {
			in.ID = newID("sel_")
			if !in.PayoutAuto && in.PayoutHoldDays == 0 {
				in.PayoutHoldDays = 7
			}
			if _, err := tx.Exec(ctx, `INSERT INTO sellers (tenant_id, id, name, email, commission_bps, status, note, payout_account, payout_auto, payout_min, payout_hold_days) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				tenant, in.ID, in.Name, in.Email, in.CommissionBps, in.Status, in.Note, in.PayoutAccount, in.PayoutAuto, in.PayoutMin, in.PayoutHoldDays); err != nil {
				return err
			}
		} else {
			ct, err := tx.Exec(ctx, `UPDATE sellers SET name=$3, email=$4, commission_bps=$5, status=$6, note=$7, payout_account=$8, payout_auto=$9, payout_min=$10, payout_hold_days=$11, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenant, in.ID, in.Name, in.Email, in.CommissionBps, in.Status, in.Note, in.PayoutAccount, in.PayoutAuto, in.PayoutMin, in.PayoutHoldDays)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicSettings, store.KeySellers, map[string]any{"id": in.ID}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "seller.save", in.ID, map[string]any{"name": in.Name, "commissionBps": in.CommissionBps, "status": in.Status})
	})
	if err != nil {
		return Seller{}, err
	}
	out, _, err := s.Seller(ctx, tenant, in.ID)
	return out, err
}

// SetSellerProducts replaces the products of a seller. A product moves away
// from another seller when it is given to this one.
func (s *Service) SetSellerProducts(ctx context.Context, tenant, actor, sellerID string, productIDs []string) error {
	if len(productIDs) > 5000 {
		return FieldError{"productIds", "Máximo 5000 productos por vendedor"}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sellers WHERE tenant_id=$1 AND id=$2)`, tenant, sellerID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM seller_products WHERE tenant_id=$1 AND seller_id=$2 AND NOT (product_id = ANY($3))`, tenant, sellerID, productIDs); err != nil {
			return err
		}
		for _, pid := range productIDs {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE tenant_id=$1 AND id=$2)`, tenant, pid).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return FieldError{"productIds", "El producto " + pid + " no existe"}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO seller_products (tenant_id, product_id, seller_id) VALUES ($1,$2,$3)
				ON CONFLICT (tenant_id, product_id) DO UPDATE SET seller_id=EXCLUDED.seller_id`, tenant, pid, sellerID); err != nil {
				return err
			}
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicSettings, store.KeySellers, map[string]any{"id": sellerID}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "seller.products", sellerID, map[string]any{"count": len(productIDs)})
	})
}

// SellerStatement returns the movements of a seller, newest first.
func (s *Service) SellerStatement(ctx context.Context, tenant, sellerID string, limit int) ([]SellerEntry, error) {
	var out []SellerEntry
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.id, e.kind, COALESCE(e.order_id,''), COALESCE(o.number,0), COALESCE(e.line,0), e.gross, e.commission, e.net, e.note, e.created_at
			FROM seller_entries e LEFT JOIN orders o ON o.tenant_id=e.tenant_id AND o.id=e.order_id
			WHERE e.tenant_id=$1 AND e.seller_id=$2 ORDER BY e.created_at DESC, e.id DESC LIMIT $3`, tenant, sellerID, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (SellerEntry, error) {
			var e SellerEntry
			return e, r.Scan(&e.ID, &e.Kind, &e.OrderID, &e.Number, &e.Line, &e.Gross, &e.Commission, &e.Net, &e.Note, &e.At)
		})
		return err
	})
	return out, err
}

// SellerPayout records money sent to the seller (kind payout, amount > 0) or
// a manual correction (kind adjust, positive or negative).
func (s *Service) SellerPayout(ctx context.Context, tenant, actor, sellerID, kind string, amount int64, note string) error {
	note = strings.TrimSpace(note)
	switch {
	case kind != "payout" && kind != "adjust":
		return FieldError{"kind", "Movimiento desconocido"}
	case amount == 0 || (kind == "payout" && amount < 0):
		return FieldError{"amount", "Escribe un monto mayor a cero"}
	case note == "" || len(note) > 300:
		return FieldError{"note", "Escribe la referencia del pago o el motivo del ajuste"}
	}
	net := amount
	if kind == "payout" {
		net = -amount
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var balance int64
		err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT sum(net) FROM seller_entries WHERE tenant_id=$1 AND seller_id=$2),0) FROM sellers WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, sellerID).Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if kind == "payout" && amount > balance {
			return FieldError{"amount", "El pago supera el saldo a favor del vendedor (" + cents(balance) + ")"}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO seller_entries (tenant_id, id, seller_id, kind, net, note, actor) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, newID("sen_"), sellerID, kind, net, note, actor); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "seller."+kind, sellerID, map[string]any{"amount": amount, "note": note})
	})
}

// sellerSyncTx brings the statement of every seller in line with the order:
// each line of a seller's product earns its net value (after discounts),
// scaled by what is left of the order after refunds. It only writes the
// difference with what is already on the statement, so it is safe to run
// after any event, any number of times.
func sellerSyncTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) error {
	var status string
	var items, discounts, grand, refunded int64
	if err := tx.QueryRow(ctx, `SELECT status, items_total, discounts, grand_total, refunded_total FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, orderID).
		Scan(&status, &items, &discounts, &grand, &refunded); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT l.line, l.total, sp.seller_id, s.commission_bps,
			COALESCE((SELECT e.commission_bps FROM seller_entries e WHERE e.tenant_id=l.tenant_id AND e.order_id=l.order_id AND e.line=l.line AND e.seller_id=sp.seller_id ORDER BY e.created_at LIMIT 1), -1),
			COALESCE((SELECT sum(e.gross) FROM seller_entries e WHERE e.tenant_id=l.tenant_id AND e.order_id=l.order_id AND e.line=l.line AND e.seller_id=sp.seller_id), 0),
			COALESCE((SELECT sum(e.commission) FROM seller_entries e WHERE e.tenant_id=l.tenant_id AND e.order_id=l.order_id AND e.line=l.line AND e.seller_id=sp.seller_id), 0)
		FROM order_lines l JOIN variants v ON v.tenant_id=l.tenant_id AND v.sku=l.sku
			JOIN seller_products sp ON sp.tenant_id=v.tenant_id AND sp.product_id=v.product_id
			JOIN sellers s ON s.tenant_id=sp.tenant_id AND s.id=sp.seller_id
		WHERE l.tenant_id=$1 AND l.order_id=$2
		UNION ALL
		-- lines already on a statement whose product no longer belongs to that seller keep following the order
		SELECT e.line, l.total, e.seller_id, 0, min(e.commission_bps), sum(e.gross), sum(e.commission)
		FROM seller_entries e JOIN order_lines l ON l.tenant_id=e.tenant_id AND l.order_id=e.order_id AND l.line=e.line
		WHERE e.tenant_id=$1 AND e.order_id=$2 AND NOT EXISTS (SELECT 1 FROM variants v JOIN seller_products sp ON sp.tenant_id=v.tenant_id AND sp.product_id=v.product_id
			WHERE v.tenant_id=l.tenant_id AND v.sku=l.sku AND sp.seller_id=e.seller_id)
		GROUP BY e.line, l.total, e.seller_id`, tenant, orderID)
	if err != nil {
		return err
	}
	type row struct {
		line                               int
		total, bps, soldBps, gross, commis int64
		seller                             string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.line, &x.total, &x.seller, &x.bps, &x.soldBps, &x.gross, &x.commis)
	})
	if err != nil {
		return err
	}
	earning := status == StatusPaid || status == StatusFulfilled || status == StatusRefunded
	for _, x := range list {
		var target int64
		if earning && grand > 0 {
			target = LineNet(x.total, items, discounts)
			target -= target * min(refunded, grand) / grand
		}
		bps := x.soldBps // the rate the line was sold with
		if bps < 0 {
			bps = x.bps
		}
		wantCommission := target * bps / 10000
		dGross, dCommission := target-x.gross, wantCommission-x.commis
		if dGross == 0 && dCommission == 0 {
			continue
		}
		kind := "sale"
		if x.soldBps >= 0 {
			kind = "refund"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO seller_entries (tenant_id, id, seller_id, kind, order_id, line, commission_bps, gross, commission, net) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			tenant, newID("sen_"), x.seller, kind, orderID, x.line, bps, dGross, dCommission, dGross-dCommission); err != nil {
			return fmt.Errorf("orders: seller statement: %w", err)
		}
	}
	return nil
}

// SellerOrderLine is a sold unit the seller must ship.
type SellerOrderLine struct {
	OrderID      string    `json:"orderId"`
	Number       string    `json:"number"`
	At           time.Time `json:"at"`
	Line         int       `json:"line"`
	SKU          string    `json:"sku"`
	Title        string    `json:"title"`
	Qty          int64     `json:"qty"`
	Pending      int64     `json:"pending"` // units still to ship
	Total        int64     `json:"total"`
	Status       string    `json:"status"`
	Customer     *Customer `json:"customer,omitempty"` // who to ship to (only while there is something to ship)
	PostalCode   string    `json:"postalCode,omitempty"`
	ShippingName string    `json:"shippingName,omitempty"`
}

// SellerOrders lists the order lines of a seller's products. pending limits
// the list to lines with units still to ship. The shipping address is only
// revealed for those lines.
func (s *Service) SellerOrders(ctx context.Context, tenant, sellerID string, pending bool, limit int) ([]SellerOrderLine, error) {
	var out []SellerOrderLine
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT o.id, COALESCE(NULLIF(o.display_number,''), o.number::text), o.created_at, l.line, l.sku, l.title, l.qty,
				GREATEST(l.qty - l.fulfilled_qty - l.cancelled_qty, 0), l.total, o.status, o.customer, o.postal_code, COALESCE(o.shipping_method->>'name','')
			FROM order_lines l JOIN orders o ON o.tenant_id=l.tenant_id AND o.id=l.order_id
				JOIN variants v ON v.tenant_id=l.tenant_id AND v.sku=l.sku
				JOIN seller_products sp ON sp.tenant_id=v.tenant_id AND sp.product_id=v.product_id
			WHERE l.tenant_id=$1 AND sp.seller_id=$2 AND o.status IN ('paid','fulfilled','refunded')
				AND (NOT $3 OR (o.status='paid' AND l.qty - l.fulfilled_qty - l.cancelled_qty > 0))
			ORDER BY o.number DESC, l.line LIMIT $4`, tenant, sellerID, pending, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x SellerOrderLine
			var enc []byte
			if err := rows.Scan(&x.OrderID, &x.Number, &x.At, &x.Line, &x.SKU, &x.Title, &x.Qty, &x.Pending, &x.Total, &x.Status, &enc, &x.PostalCode, &x.ShippingName); err != nil {
				return err
			}
			if x.Pending > 0 && x.Status == StatusPaid {
				if plain, err := s.Keys.Decrypt(tenant, enc, x.OrderID); err == nil {
					var c Customer
					if json.Unmarshal(plain, &c) == nil {
						c.Email = "" // the seller ships; the store keeps the relationship with the shopper
						x.Customer = &c
					}
				}
			} else {
				x.PostalCode = ""
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// SellerShip ships the pending units of the seller's lines of an order.
func (s *Service) SellerShip(ctx context.Context, tenant, actor, sellerID, orderID, carrier, tracking string) (Fulfillment, error) {
	lines, err := s.SellerOrders(ctx, tenant, sellerID, true, 1000)
	if err != nil {
		return Fulfillment{}, err
	}
	var items []RefundLine
	for _, l := range lines {
		if l.OrderID == orderID {
			items = append(items, RefundLine{Line: l.Line, Qty: l.Pending})
		}
	}
	if len(items) == 0 {
		return Fulfillment{}, fmt.Errorf("%w: no hay productos tuyos por enviar en este pedido", ErrInvalidState)
	}
	return s.CreateFulfillment(ctx, tenant, actor, orderID, FulfillmentInput{Items: items, Carrier: carrier, TrackingNumber: tracking})
}
