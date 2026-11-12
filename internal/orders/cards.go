package orders

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
)

// Saved cards: a signed-in customer keeps a card for the next purchase. Only
// provider tokens are stored, plus brand and last four digits to recognise it.

// SavedToken is what the provider needs to charge a kept card.
type SavedToken struct{ Customer, PaymentMethod string }

// SavedCard is a kept card as the customer sees it.
type SavedCard struct {
	ID        string    `json:"id"`
	Brand     string    `json:"brand"`
	Last4     string    `json:"last4"`
	ExpMonth  int       `json:"expMonth"`
	ExpYear   int       `json:"expYear"`
	CreatedAt time.Time `json:"createdAt"`
}

const maxCards = 5

// keepCard stores the card of a captured charge for a customer.
func (s *Service) keepCard(ctx context.Context, tenant, customerID string, acct Account, ch payments.Charge) error {
	var info payments.CardInfo
	if d, ok := acct.Provider.(payments.CardDescriber); ok {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, _ = d.DescribeCard(c, acct.Account, ch.PaymentMethod)
		cancel()
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM customer_cards WHERE tenant_id=$1 AND customer_id=$2`, tenant, customerID).Scan(&n); err != nil {
			return err
		}
		if n >= maxCards {
			return nil // the customer keeps at most five; the purchase still went through
		}
		if _, err := tx.Exec(ctx, `INSERT INTO customer_cards (tenant_id, id, customer_id, provider, provider_customer, payment_method, brand, last4, exp_month, exp_year)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (tenant_id, customer_id, payment_method) DO NOTHING`,
			tenant, newID("card_"), customerID, acct.Provider.Name(), ch.Customer, ch.PaymentMethod, info.Brand, info.Last4, info.ExpMonth, info.ExpYear); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "customer:"+customerID, "customer.card_saved", customerID, map[string]any{"brand": info.Brand, "last4": info.Last4})
	})
}

// Cards lists the kept cards of a customer.
func (s *Service) Cards(ctx context.Context, tenant, customerID string) ([]SavedCard, error) {
	var out []SavedCard
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, brand, last4, exp_month, exp_year, created_at FROM customer_cards WHERE tenant_id=$1 AND customer_id=$2 ORDER BY created_at DESC`, tenant, customerID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (SavedCard, error) {
			var c SavedCard
			return c, r.Scan(&c.ID, &c.Brand, &c.Last4, &c.ExpMonth, &c.ExpYear, &c.CreatedAt)
		})
		return err
	})
	return out, err
}

// CardToken resolves a kept card of this customer (ErrNotFound otherwise).
func (s *Service) CardToken(ctx context.Context, tenant, customerID, cardID string) (*SavedToken, error) {
	var t SavedToken
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT provider_customer, payment_method FROM customer_cards WHERE tenant_id=$1 AND customer_id=$2 AND id=$3`, tenant, customerID, cardID).Scan(&t.Customer, &t.PaymentMethod)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ForgetCard removes a kept card of a customer.
func (s *Service) ForgetCard(ctx context.Context, tenant, customerID, cardID string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM customer_cards WHERE tenant_id=$1 AND customer_id=$2 AND id=$3`, tenant, customerID, cardID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, "customer:"+customerID, "customer.card_removed", customerID, map[string]any{"card": cardID})
	})
}
