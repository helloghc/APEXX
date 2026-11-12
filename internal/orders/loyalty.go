package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Loyalty: customers earn points on what they actually paid, at the rate of
// their tier; refunds and cancellations take back exactly the points the
// order gave; points are redeemed into store credit, which existing refunds
// already return. Every movement is an entry and is booked in the ledger at
// the program's point value.

// LoyaltyTier multiplies points for customers who spent at least MinSpend.
type LoyaltyTier struct {
	Name          string `json:"name"`
	MinSpend      int64  `json:"minSpend"`      // lifetime paid, cents
	MultiplierPct int    `json:"multiplierPct"` // 100 = 1x
}

// LoyaltyProgram is a store's points program.
type LoyaltyProgram struct {
	Active     bool          `json:"active"`
	EarnPer    int64         `json:"earnPer"`    // cents paid for one point (1000 = 1 point per $10)
	PointValue int64         `json:"pointValue"` // cents one point is worth when redeemed
	MinRedeem  int64         `json:"minRedeem"`  // fewest points per redemption
	Tiers      []LoyaltyTier `json:"tiers"`
}

// LoyaltyEntry is one movement of a customer's points.
type LoyaltyEntry struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"orderId,omitempty"`
	Kind      string    `json:"kind"`
	Points    int64     `json:"points"`
	Value     int64     `json:"value"`
	Note      string    `json:"note,omitempty"`
	CardID    string    `json:"cardId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// LoyaltyAccount is a customer's balance, tier and history.
type LoyaltyAccount struct {
	Points   int64          `json:"points"`
	Value    int64          `json:"value"` // what the balance is worth, cents
	Tier     string         `json:"tier,omitempty"`
	NextTier string         `json:"nextTier,omitempty"`
	ToNext   int64          `json:"toNext,omitempty"` // spend left for the next tier, cents
	Entries  []LoyaltyEntry `json:"entries"`
}

func loyaltyProgramTx(ctx context.Context, tx pgx.Tx, tenant string) (LoyaltyProgram, bool) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT data FROM tenant_docs WHERE tenant_id=$1 AND kind='loyalty' AND id='program'`, tenant).Scan(&raw); err != nil {
		return LoyaltyProgram{}, false
	}
	var p LoyaltyProgram
	if json.Unmarshal(raw, &p) != nil || !p.Active || p.EarnPer <= 0 {
		return p, false
	}
	return p, true
}

func (p LoyaltyProgram) tierOf(spent int64) (LoyaltyTier, *LoyaltyTier) {
	cur := LoyaltyTier{MultiplierPct: 100}
	var next *LoyaltyTier
	for i, t := range p.Tiers {
		if spent >= t.MinSpend && t.MinSpend >= cur.MinSpend {
			cur = t
		} else if spent < t.MinSpend && (next == nil || t.MinSpend < next.MinSpend) {
			next = &p.Tiers[i]
		}
	}
	if cur.MultiplierPct <= 0 {
		cur.MultiplierPct = 100
	}
	return cur, next
}

func loyaltyEntryTx(ctx context.Context, tx pgx.Tx, tenant, customer string, order *string, kind string, points, value int64, note, actor string, card *string, currency string) error {
	if points == 0 {
		return nil
	}
	id := newID("lyl_")
	if _, err := tx.Exec(ctx, `INSERT INTO loyalty_entries (tenant_id, id, customer_id, order_id, kind, points, value, note, card_id, actor) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		tenant, id, customer, order, kind, points, value, note, card, actor); err != nil {
		return err
	}
	// Points are a debt to the customer: earning books an expense, giving them back cancels it.
	return ledger(ctx, tx, tenant, "loyalty-"+id, derefStr(order), currency, map[string]int64{"loyalty_expense": value, "loyalty_liability": -value})
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loyaltySyncTx keeps an order's points equal to what it should give now:
// what was paid and not refunded, at the tier the customer had when it was
// first paid. A cancelled or refunded order ends at zero.
func loyaltySyncTx(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	var cust *string
	var status, cur string
	var grand, refunded, shipping int64
	if err := tx.QueryRow(ctx, `SELECT customer_id, status, grand_total, refunded_total, shipping, currency FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).
		Scan(&cust, &status, &grand, &refunded, &shipping, &cur); err != nil || cust == nil {
		return nil
	}
	var have, mult int64
	var first bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(points),0), COALESCE(max(CASE WHEN kind='earn' THEN (regexp_match(note, 'x([0-9]+)%'))[1]::bigint END), 0), count(*) = 0
		FROM loyalty_entries WHERE tenant_id=$1 AND order_id=$2 AND kind IN ('earn','adjust')`, tenant, id).Scan(&have, &mult, &first); err != nil {
		return err
	}
	p, active := loyaltyProgramTx(ctx, tx, tenant)
	if !active {
		return nil // a paused program neither gives nor takes points
	}
	paid := status == StatusPaid || status == StatusFulfilled
	base := max(grand-shipping-refunded, 0)
	if first {
		var spent int64
		_ = tx.QueryRow(ctx, `SELECT total_spent FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, *cust).Scan(&spent)
		t, _ := p.tierOf(spent) // the customer's stats do not include this order yet
		mult = int64(t.MultiplierPct)
	}
	want := int64(0)
	if paid && p.EarnPer > 0 {
		want = base / p.EarnPer * mult / 100
	}
	diff := want - have
	if diff == 0 {
		return nil
	}
	kind, note := "adjust", "Ajuste por reembolso o cancelación"
	if first {
		kind, note = "earn", fmt.Sprintf("Puntos por el pedido (x%d%%)", mult)
	}
	return loyaltyEntryTx(ctx, tx, tenant, *cust, &id, kind, diff, diff*p.PointValue, note, "system:loyalty", nil, cur)
}

// LoyaltyAccountOf returns a customer's points.
func (s *Service) LoyaltyAccountOf(ctx context.Context, tenant, customer string) (LoyaltyAccount, error) {
	var a LoyaltyAccount
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		p, _ := loyaltyProgramTx(ctx, tx, tenant)
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(points),0) FROM loyalty_entries WHERE tenant_id=$1 AND customer_id=$2`, tenant, customer).Scan(&a.Points); err != nil {
			return err
		}
		a.Value = a.Points * p.PointValue
		var spent int64
		_ = tx.QueryRow(ctx, `SELECT total_spent FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, customer).Scan(&spent)
		t, next := p.tierOf(spent)
		a.Tier = t.Name
		if next != nil {
			a.NextTier, a.ToNext = next.Name, next.MinSpend-spent
		}
		rows, err := tx.Query(ctx, `SELECT id, COALESCE(order_id,''), kind, points, value, note, COALESCE(card_id,''), created_at FROM loyalty_entries
			WHERE tenant_id=$1 AND customer_id=$2 ORDER BY created_at DESC LIMIT 50`, tenant, customer)
		if err != nil {
			return err
		}
		a.Entries, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (LoyaltyEntry, error) {
			var e LoyaltyEntry
			return e, r.Scan(&e.ID, &e.OrderID, &e.Kind, &e.Points, &e.Value, &e.Note, &e.CardID, &e.CreatedAt)
		})
		return err
	})
	if a.Entries == nil {
		a.Entries = []LoyaltyEntry{}
	}
	return a, err
}

// ErrLoyalty explains why points cannot be redeemed.
var ErrLoyalty = errors.New("puntos")

// RedeemPoints turns points into store credit for the customer and returns
// the card (its code is emailed and shown once).
func (s *Service) RedeemPoints(ctx context.Context, tenant, actor, customer, email, name string, points int64) (GiftCard, error) {
	var out GiftCard
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		p, active := loyaltyProgramTx(ctx, tx, tenant)
		if !active || p.PointValue <= 0 {
			return fmt.Errorf("%w: la tienda no tiene programa de puntos activo", ErrLoyalty)
		}
		// One redemption at a time per customer, so the balance cannot be spent twice.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || ':loyalty:' || $2))`, tenant, customer); err != nil {
			return err
		}
		var bal int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(points),0) FROM loyalty_entries WHERE tenant_id=$1 AND customer_id=$2`, tenant, customer).Scan(&bal); err != nil {
			return err
		}
		switch {
		case points <= 0 || points > bal:
			return fmt.Errorf("%w: tienes %d puntos", ErrLoyalty, bal)
		case points < max(p.MinRedeem, 1):
			return fmt.Errorf("%w: el mínimo para canjear es %d puntos", ErrLoyalty, p.MinRedeem)
		case points*p.PointValue < 100:
			return fmt.Errorf("%w: canjea al menos $1 en puntos", ErrLoyalty)
		}
		value := points * p.PointValue
		card, _, err := s.issueTx(ctx, tx, tenant, actor, newID("gc_"), IssueInput{Kind: "credit", Amount: value, Currency: "MXN", CustomerID: customer,
			RecipientEmail: strings.ToLower(email), RecipientName: name, Note: fmt.Sprintf("Canje de %d puntos", points)}, "")
		if err != nil {
			return err
		}
		out = card
		if _, err := tx.Exec(ctx, `INSERT INTO loyalty_entries (tenant_id, id, customer_id, kind, points, value, note, card_id, actor) VALUES ($1,$2,$3,'redeem',$4,$5,$6,$7,$8)`,
			tenant, newID("lyl_"), customer, -points, -value, "Canje por saldo a favor", card.ID, actor); err != nil {
			return err
		}
		// The credit replaces the points: the debt moves from points to store credit.
		return ledger(ctx, tx, tenant, "loyalty-redeem-"+card.ID, "", "MXN", map[string]int64{"loyalty_liability": value, "gift_card_expense": -value})
	})
	return out, err
}

// AdjustPoints adds or removes points by hand (with a reason).
func (s *Service) AdjustPoints(ctx context.Context, tenant, actor, customer string, points int64, note string) error {
	note = strings.TrimSpace(note)
	if points == 0 || points > 1_000_000 || points < -1_000_000 || note == "" || len(note) > 200 {
		return FieldError{"points", "Escribe los puntos (distintos de cero) y el motivo"}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		p, _ := loyaltyProgramTx(ctx, tx, tenant)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || ':loyalty:' || $2))`, tenant, customer); err != nil {
			return err
		}
		var bal int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(points),0) FROM loyalty_entries WHERE tenant_id=$1 AND customer_id=$2`, tenant, customer).Scan(&bal); err != nil {
			return err
		}
		if bal+points < 0 {
			return FieldError{"points", fmt.Sprintf("El cliente tiene %d puntos", bal)}
		}
		return loyaltyEntryTx(ctx, tx, tenant, customer, nil, "manual", points, points*p.PointValue, note, actor, nil, "MXN")
	})
}
