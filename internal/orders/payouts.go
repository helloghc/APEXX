package orders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
)

// Automatic payouts to marketplace sellers.
//
// The money leaves the seller's statement first and is sent second. If the
// process dies between the two, the payout stays "pending" and the next round
// sends it again with the same idempotency key, so the provider pays once.
// A transfer the provider rejects is marked failed and the amount goes back
// to the statement with its own movement, so the books always add up.
//
// A sale waits a few days before it can be paid, so a return comes out of the
// seller's balance instead of out of the store's pocket. A return that arrives
// after the payout leaves the balance negative and the next payout absorbs it.

// SellerPayout is one transfer to a seller.
type SellerPayoutRow struct {
	ID          string     `json:"id"`
	SellerID    string     `json:"sellerId"`
	Amount      int64      `json:"amount"`
	Currency    string     `json:"currency"`
	Status      string     `json:"status"` // pending, paid, failed
	Destination string     `json:"destination"`
	Provider    string     `json:"provider,omitempty"`
	Ref         string     `json:"ref,omitempty"`
	Error       string     `json:"error,omitempty"`
	Actor       string     `json:"actor"`
	CreatedAt   time.Time  `json:"createdAt"`
	SettledAt   *time.Time `json:"settledAt,omitempty"`
}

// ErrNothingToPay means the seller has no money that can be sent today.
var ErrNothingToPay = errors.New("orders: nothing to pay")

// payableTx returns what can be sent to the seller now, with the row locked.
func payableTx(ctx context.Context, tx pgx.Tx, tenant, sellerID string) (amount int64, account string, min int64, err error) {
	err = tx.QueryRow(ctx, `SELECT s.payout_account, s.payout_min,
		COALESCE((SELECT sum(net) FROM seller_entries e WHERE e.tenant_id=s.tenant_id AND e.seller_id=s.id),0)
		- COALESCE((SELECT sum(net) FROM seller_entries y WHERE y.tenant_id=s.tenant_id AND y.seller_id=s.id AND y.kind='sale' AND y.net > 0 AND y.created_at > now() - make_interval(days => s.payout_hold_days)),0)
		FROM sellers s WHERE s.tenant_id=$1 AND s.id=$2 FOR UPDATE OF s`, tenant, sellerID).Scan(&account, &min, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", 0, ErrNotFound
	}
	return amount, account, min, err
}

// PaySeller sends the seller everything that can be paid today. amount 0
// means "all of it"; a smaller amount pays part.
func (s *Service) PaySeller(ctx context.Context, tenant, actor, sellerID string, amount int64) (SellerPayoutRow, error) {
	var id string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		payable, account, _, err := payableTx(ctx, tx, tenant, sellerID)
		if err != nil {
			return err
		}
		switch {
		case account == "":
			return FieldError{"payoutAccount", "Primero guarda la cuenta de Stripe del vendedor"}
		case amount < 0:
			return FieldError{"amount", "Escribe un monto mayor a cero"}
		case payable <= 0:
			return ErrNothingToPay
		case amount > payable:
			return FieldError{"amount", "Hoy se le pueden pagar hasta " + cents(payable)}
		}
		if amount == 0 {
			amount = payable
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM seller_payouts WHERE tenant_id=$1 AND seller_id=$2 AND status='pending')`, tenant, sellerID).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return FieldError{"amount", "Ya hay un pago en camino para este vendedor. Espera a que termine"}
		}
		id = newID("spo_")
		if _, err := tx.Exec(ctx, `INSERT INTO seller_payouts (tenant_id, id, seller_id, amount, destination, actor) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenant, id, sellerID, amount, account, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO seller_entries (tenant_id, id, seller_id, kind, net, note, actor, payout_id) VALUES ($1,$2,$3,'payout',$4,$5,$6,$7)`,
			tenant, newID("sen_"), sellerID, -amount, "Pago automático "+id, actor, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "seller.payout.start", sellerID, map[string]any{"payout": id, "amount": amount})
	})
	if err != nil {
		return SellerPayoutRow{}, err
	}
	// The transfer happens outside the transaction; settle records the outcome.
	_ = s.settlePayout(ctx, tenant, id)
	return s.payout(ctx, tenant, id)
}

func (s *Service) payout(ctx context.Context, tenant, id string) (SellerPayoutRow, error) {
	var p SellerPayoutRow
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, payoutSelect+` AND id=$2`, tenant, id).Scan(&p.ID, &p.SellerID, &p.Amount, &p.Currency, &p.Status, &p.Destination, &p.Provider, &p.Ref, &p.Error, &p.Actor, &p.CreatedAt, &p.SettledAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

const payoutSelect = `SELECT id, seller_id, amount, currency, status, destination, provider, provider_ref, error, actor, created_at, settled_at FROM seller_payouts WHERE tenant_id=$1`

// SellerPayouts lists the transfers of a seller, newest first.
func (s *Service) SellerPayouts(ctx context.Context, tenant, sellerID string, limit int) ([]SellerPayoutRow, error) {
	var out []SellerPayoutRow
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, payoutSelect+` AND seller_id=$2 ORDER BY created_at DESC, id DESC LIMIT $3`, tenant, sellerID, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (SellerPayoutRow, error) {
			var p SellerPayoutRow
			return p, r.Scan(&p.ID, &p.SellerID, &p.Amount, &p.Currency, &p.Status, &p.Destination, &p.Provider, &p.Ref, &p.Error, &p.Actor, &p.CreatedAt, &p.SettledAt)
		})
		return err
	})
	return out, err
}

// settlePayout sends a pending payout and records what happened. It is safe
// to call any number of times: the provider sees one idempotency key.
func (s *Service) settlePayout(ctx context.Context, tenant, id string) error {
	p, err := s.payout(ctx, tenant, id)
	if err != nil || p.Status != "pending" {
		return err
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return err
	}
	tr, ok := acct.Provider.(payments.Transferer)
	var ref string
	if !ok {
		err = fmt.Errorf("%w: el proveedor de pagos no envía dinero a terceros", payments.ErrDeclined)
	} else {
		ref, err = tr.Transfer(ctx, acct.Account, p.Destination, p.Amount, p.Currency, "payout-"+p.ID)
	}
	if err != nil && !errors.Is(err, payments.ErrDeclined) {
		// The provider did not answer: leave it pending for the next round.
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `UPDATE seller_payouts SET attempts=attempts+1, error=$3 WHERE tenant_id=$1 AND id=$2 AND status='pending'`, tenant, id, truncate(err.Error(), 300))
			return e
		})
		return err
	}
	failed := err != nil
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		status, msg := "paid", ""
		if failed {
			status, msg = "failed", truncate(err.Error(), 300)
		}
		ct, e := tx.Exec(ctx, `UPDATE seller_payouts SET status=$3, provider=$4, provider_ref=$5, error=$6, attempts=attempts+1, settled_at=now() WHERE tenant_id=$1 AND id=$2 AND status='pending'`,
			tenant, id, status, acct.Provider.Name(), ref, msg)
		if e != nil || ct.RowsAffected() == 0 {
			return e // someone else settled it first
		}
		if failed {
			// Put the money back on the statement.
			if _, e := tx.Exec(ctx, `INSERT INTO seller_entries (tenant_id, id, seller_id, kind, net, note, actor, payout_id) VALUES ($1,$2,$3,'payout_failed',$4,$5,'system',$6)`,
				tenant, newID("sen_"), p.SellerID, p.Amount, "El pago "+id+" no se pudo enviar; el saldo regresó", id); e != nil {
				return e
			}
		}
		return store.Audit(ctx, tx, tenant, "system", "seller.payout."+status, p.SellerID, map[string]any{"payout": id, "amount": p.Amount, "ref": ref})
	})
}

// RunPayouts is the background round: it finishes payouts left pending and
// pays every seller set to automatic that has enough to pay.
func (s *Service) RunPayouts(ctx context.Context) (int, error) {
	type ref struct{ tenant, id string }
	collect := func(q string) ([]ref, error) {
		rows, err := s.DB.System.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ref, error) {
			var x ref
			return x, r.Scan(&x.tenant, &x.id)
		})
	}
	pending, err := collect(`SELECT tenant_id, id FROM seller_payouts WHERE status='pending' AND created_at < now() - interval '30 seconds' ORDER BY created_at LIMIT 100`)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range pending {
		if s.settlePayout(ctx, x.tenant, x.id) == nil {
			n++
		}
	}
	due, err := collect(`SELECT s.tenant_id, s.id FROM sellers s WHERE s.payout_auto AND s.status='active' AND s.payout_account <> ''
		AND NOT EXISTS (SELECT 1 FROM seller_payouts p WHERE p.tenant_id=s.tenant_id AND p.seller_id=s.id AND (p.status='pending' OR (p.status='failed' AND p.settled_at > now() - interval '1 day')))
		AND COALESCE((SELECT sum(net) FROM seller_entries e WHERE e.tenant_id=s.tenant_id AND e.seller_id=s.id),0)
		  - COALESCE((SELECT sum(net) FROM seller_entries y WHERE y.tenant_id=s.tenant_id AND y.seller_id=s.id AND y.kind='sale' AND y.net > 0 AND y.created_at > now() - make_interval(days => s.payout_hold_days)),0)
		  >= GREATEST(s.payout_min, 1) LIMIT 200`)
	if err != nil {
		return n, err
	}
	for _, x := range due {
		if _, err := s.PaySeller(ctx, x.tenant, "system", x.id, 0); err == nil {
			n++
		}
	}
	return n, nil
}
