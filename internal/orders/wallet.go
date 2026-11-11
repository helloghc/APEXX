package orders

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// Gift cards and store credit share one mechanism: a card with a balance that
// pays all or part of an order. It enters the order as one more payment
// (provider "gift_card"), so refunds, the ledger and the balance of the order
// treat it like any other money.

// GiftCard is a gift card or a store credit.
type GiftCard struct {
	ID            string     `json:"id"`
	Code          string     `json:"code,omitempty"` // only when it is created
	Last4         string     `json:"last4"`
	Kind          string     `json:"kind"` // gift | credit
	Initial       int64      `json:"initial"`
	Balance       int64      `json:"balance"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	CustomerID    string     `json:"customerId,omitempty"`
	RecipientName string     `json:"recipientName,omitempty"`
	Note          string     `json:"note,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	Expired       bool       `json:"expired"`
	CreatedBy     string     `json:"createdBy"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// GiftCardTx is one movement of a card.
type GiftCardTx struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Amount  int64     `json:"amount"`
	OrderID string    `json:"orderId,omitempty"`
	Actor   string    `json:"actor"`
	Note    string    `json:"note,omitempty"`
	At      time.Time `json:"at"`
}

// IssueInput describes a new card.
type IssueInput struct {
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	Kind           string     `json:"kind"`
	RecipientEmail string     `json:"recipientEmail"`
	RecipientName  string     `json:"recipientName"`
	Note           string     `json:"note"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	CustomerID     string     `json:"customerId"`
}

// GiftCardError is a problem with a code the shopper typed.
type GiftCardError struct{ Code, Message string }

func (e *GiftCardError) Error() string { return e.Message }

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I

func newCardCode() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for i, x := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[int(x)%len(codeAlphabet)])
	}
	return sb.String()
}

// NormalizeCode strips spaces and dashes and upper-cases what the shopper typed.
func NormalizeCode(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (s *Service) codeHash(tenant, code string) ([]byte, error) {
	return s.Keys.Blind(tenant, "giftcard", NormalizeCode(code))
}

const cardCols = `id, last4, kind, initial, balance, currency, status, COALESCE(customer_id,''), recipient_name, note, expires_at, created_by, created_at`

func scanCard(r pgx.Row) (GiftCard, error) {
	var c GiftCard
	err := r.Scan(&c.ID, &c.Last4, &c.Kind, &c.Initial, &c.Balance, &c.Currency, &c.Status, &c.CustomerID, &c.RecipientName, &c.Note, &c.ExpiresAt, &c.CreatedBy, &c.CreatedAt)
	c.Expired = c.ExpiresAt != nil && c.ExpiresAt.Before(time.Now())
	return c, err
}

// issueTx creates a card. A fixed id makes it idempotent: the second call
// returns created=false and changes nothing.
func (s *Service) issueTx(ctx context.Context, tx pgx.Tx, tenant, actor, id string, in IssueInput, orderID string) (GiftCard, bool, error) {
	code := newCardCode()
	hash, err := s.codeHash(tenant, code)
	if err != nil {
		return GiftCard{}, false, err
	}
	var cust *string
	if in.CustomerID != "" {
		cust = &in.CustomerID
	}
	ct, err := tx.Exec(ctx, `INSERT INTO gift_cards (tenant_id, id, code_hash, last4, kind, initial, balance, currency, customer_id, recipient_name, note, expires_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (tenant_id, id) DO NOTHING`,
		tenant, id, hash, code[len(code)-4:], in.Kind, in.Amount, in.Currency, cust, in.RecipientName, in.Note, in.ExpiresAt, actor)
	if err != nil {
		return GiftCard{}, false, err
	}
	if ct.RowsAffected() == 0 {
		c, err := scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND id=$2`, tenant, id))
		return c, false, err
	}
	var ord *string
	if orderID != "" {
		ord = &orderID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO gift_card_tx (tenant_id, id, card_id, kind, amount, order_id, actor, note) VALUES ($1,$2,$3,'issue',$4,$5,$6,$7)`,
		tenant, "gtx_"+id, id, in.Amount, ord, actor, in.Note); err != nil {
		return GiftCard{}, false, err
	}
	// Cards given away by the store are a cost; credit from a refund is booked by the refund itself.
	if orderID == "" {
		for acct, amt := range map[string]int64{"gift_card_expense": in.Amount, "gift_card_liability": -in.Amount} {
			if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (tenant_id, tx_id, account, amount, currency) VALUES ($1,$2,$3,$4,$5)`, tenant, "giftcard-"+id, acct, amt, in.Currency); err != nil {
				return GiftCard{}, false, err
			}
		}
	}
	if s.Notify != nil && in.RecipientEmail != "" {
		vars := map[string]string{"cliente.nombre": firstWord(in.RecipientName), "tarjeta.codigo": code, "tarjeta.saldo": notify.Money(in.Amount, in.Currency), "tarjeta.vence": "No vence"}
		if in.ExpiresAt != nil {
			vars["tarjeta.vence"] = in.ExpiresAt.In(time.FixedZone("CST", -6*3600)).Format("02/01/2006")
		}
		if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplGiftCard, To: in.RecipientEmail, Name: in.RecipientName,
			Data: notify.Data{Vars: vars, ActionURL: strings.TrimRight(s.PublicURL, "/") + "/s/" + tenant + "/main/index"}, CustomerID: in.CustomerID, OrderID: orderID, DedupeKey: "giftcard-" + id}); err != nil {
			return GiftCard{}, false, err
		}
	}
	if err := store.Audit(ctx, tx, tenant, actor, "giftcard.issue", id, map[string]any{"kind": in.Kind, "amount": in.Amount, "emailed": in.RecipientEmail != ""}); err != nil {
		return GiftCard{}, false, err
	}
	c, err := scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND id=$2`, tenant, id))
	c.Code = code
	return c, true, err
}

// IssueGiftCard creates a card and, when there is a recipient, emails the code.
func (s *Service) IssueGiftCard(ctx context.Context, tenant, actor string, in IssueInput) (GiftCard, error) {
	in.RecipientEmail, in.RecipientName, in.Note = strings.ToLower(strings.TrimSpace(in.RecipientEmail)), strings.TrimSpace(in.RecipientName), strings.TrimSpace(in.Note)
	if in.Kind == "" {
		in.Kind = "gift"
	}
	if in.Currency == "" {
		in.Currency = "MXN"
	}
	switch {
	case in.Kind != "gift" && in.Kind != "credit":
		return GiftCard{}, FieldError{"kind", "Tipo desconocido"}
	case in.Amount < 100 || in.Amount > 100_000_00:
		return GiftCard{}, FieldError{"amount", "El saldo debe estar entre $1 y $100,000"}
	case in.RecipientEmail != "" && (!strings.Contains(in.RecipientEmail, "@") || len(in.RecipientEmail) > 200):
		return GiftCard{}, FieldError{"recipientEmail", "Escribe un correo válido"}
	case len(in.RecipientName) > 80 || len(in.Note) > 300:
		return GiftCard{}, FieldError{"note", "Nombre de hasta 80 caracteres y nota de hasta 300"}
	case in.ExpiresAt != nil && in.ExpiresAt.Before(time.Now()):
		return GiftCard{}, FieldError{"expiresAt", "La fecha de vencimiento ya pasó"}
	}
	var out GiftCard
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, _, err = s.issueTx(ctx, tx, tenant, actor, newID("gc_"), in, "")
		return err
	})
	return out, err
}

// GiftCards lists cards, newest first.
func (s *Service) GiftCards(ctx context.Context, tenant, kind, q string, before time.Time, limit int) ([]GiftCard, map[string]int64, error) {
	var out []GiftCard
	stats := map[string]int64{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var b *time.Time
		if !before.IsZero() {
			b = &before
		}
		rows, err := tx.Query(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND ($2='' OR kind=$2)
			AND ($3='' OR last4 = upper($3) OR recipient_name ILIKE '%'||$3||'%' OR id=$3) AND ($4::timestamptz IS NULL OR created_at < $4)
			ORDER BY created_at DESC LIMIT $5`, tenant, kind, strings.TrimSpace(q), b, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCard(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		var n, outstanding, issued int64
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(balance) FILTER (WHERE status='active' AND (expires_at IS NULL OR expires_at > now())),0), COALESCE(sum(initial),0)
			FROM gift_cards WHERE tenant_id=$1`, tenant).Scan(&n, &outstanding, &issued); err != nil {
			return err
		}
		stats["cards"], stats["outstanding"], stats["issued"] = n, outstanding, issued
		return nil
	})
	return out, stats, err
}

// GiftCard returns a card with its movements.
func (s *Service) GiftCard(ctx context.Context, tenant, id string) (GiftCard, []GiftCardTx, error) {
	var c GiftCard
	var txs []GiftCardTx
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, err = scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND id=$2`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, kind, amount, COALESCE(order_id,''), actor, note, created_at FROM gift_card_tx WHERE tenant_id=$1 AND card_id=$2 ORDER BY created_at, id`, tenant, id)
		if err != nil {
			return err
		}
		txs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (GiftCardTx, error) {
			var t GiftCardTx
			return t, r.Scan(&t.ID, &t.Kind, &t.Amount, &t.OrderID, &t.Actor, &t.Note, &t.At)
		})
		return err
	})
	return c, txs, err
}

// UpdateGiftCard enables or disables a card and/or adjusts its balance.
func (s *Service) UpdateGiftCard(ctx context.Context, tenant, actor, id, status string, delta int64, note string) (GiftCard, error) {
	if status != "" && status != "active" && status != "disabled" {
		return GiftCard{}, FieldError{"status", "Estado desconocido"}
	}
	if delta != 0 && strings.TrimSpace(note) == "" {
		return GiftCard{}, FieldError{"note", "Escribe el motivo del ajuste"}
	}
	var c GiftCard
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, err = scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.Balance+delta < 0 {
			return FieldError{"amount", "El ajuste deja el saldo en negativo"}
		}
		if status == "" {
			status = c.Status
		}
		if _, err := tx.Exec(ctx, `UPDATE gift_cards SET status=$3, balance=balance+$4, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, status, delta); err != nil {
			return err
		}
		if delta != 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO gift_card_tx (tenant_id, id, card_id, kind, amount, actor, note) VALUES ($1,$2,$3,'adjust',$4,$5,$6)`,
				tenant, newID("gtx_"), id, delta, actor, strings.TrimSpace(note)); err != nil {
				return err
			}
			for acct, amt := range map[string]int64{"gift_card_expense": delta, "gift_card_liability": -delta} {
				if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (tenant_id, tx_id, account, amount, currency) VALUES ($1,$2,$3,$4,$5)`, tenant, "giftcard-adjust-"+newID(""), acct, amt, c.Currency); err != nil {
					return err
				}
			}
		}
		if err := store.Audit(ctx, tx, tenant, actor, "giftcard.update", id, map[string]any{"status": status, "delta": delta, "note": note}); err != nil {
			return err
		}
		c, err = scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND id=$2`, tenant, id))
		return err
	})
	return c, err
}

// usable explains why a card cannot be used, or returns nil.
func usable(c GiftCard, currency string) *GiftCardError {
	switch {
	case c.Status != "active":
		return &GiftCardError{"gift_card_disabled", "Esta tarjeta está desactivada"}
	case c.Expired:
		return &GiftCardError{"gift_card_expired", "Esta tarjeta ya venció"}
	case c.Balance == 0:
		return &GiftCardError{"gift_card_empty", "Esta tarjeta ya no tiene saldo"}
	case currency != "" && c.Currency != currency:
		return &GiftCardError{"gift_card_currency", "Esta tarjeta es de otra moneda"}
	}
	return nil
}

var errUnknownCard = &GiftCardError{"gift_card_unknown", "No encontramos esa tarjeta. Revisa el código"}

// CheckGiftCard tells the shopper the balance of a code.
func (s *Service) CheckGiftCard(ctx context.Context, tenant, code, currency string) (GiftCard, error) {
	if len(NormalizeCode(code)) != 16 {
		return GiftCard{}, errUnknownCard
	}
	hash, err := s.codeHash(tenant, code)
	if err != nil {
		return GiftCard{}, err
	}
	var c GiftCard
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, err = scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND code_hash=$2`, tenant, hash))
		if errors.Is(err, pgx.ErrNoRows) {
			return errUnknownCard
		}
		return err
	})
	if err != nil {
		return GiftCard{}, err
	}
	if e := usable(c, currency); e != nil {
		return GiftCard{}, e
	}
	return c, nil
}

// redeemWalletTx spends the cards on an order, in the order typed, until the
// total is covered. Cards are locked, so two checkouts cannot spend the same balance.
func (s *Service) redeemWalletTx(ctx context.Context, tx pgx.Tx, tenant, actor, orderID string, codes []string, total int64, currency string) (int64, error) {
	if len(codes) > 5 {
		return 0, &GiftCardError{"gift_card_limit", "Puedes usar hasta 5 tarjetas por pedido"}
	}
	seen := map[string]bool{}
	var applied int64
	for _, code := range codes {
		n := NormalizeCode(code)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if len(n) != 16 {
			return 0, errUnknownCard
		}
		hash, err := s.codeHash(tenant, n)
		if err != nil {
			return 0, err
		}
		c, err := scanCard(tx.QueryRow(ctx, `SELECT `+cardCols+` FROM gift_cards WHERE tenant_id=$1 AND code_hash=$2 FOR UPDATE`, tenant, hash))
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errUnknownCard
		}
		if err != nil {
			return 0, err
		}
		if e := usable(c, currency); e != nil {
			return 0, e
		}
		take := min(c.Balance, total-applied)
		if take <= 0 {
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE gift_cards SET balance=balance-$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, c.ID, take); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gift_card_tx (tenant_id, id, card_id, kind, amount, order_id, actor) VALUES ($1,$2,$3,'redeem',$4,$5,$6)`,
			tenant, newID("gtx_"), c.ID, -take, orderID, actor); err != nil {
			return 0, err
		}
		applied += take
	}
	return applied, nil
}

type walletPart struct {
	CardID string
	Amount int64
}

// walletPartsTx returns what each card is paying of an order that is still pending.
func walletPartsTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) ([]walletPart, int64, error) {
	rows, err := tx.Query(ctx, `SELECT card_id, -sum(amount) FROM gift_card_tx WHERE tenant_id=$1 AND order_id=$2 AND kind IN ('redeem','release')
		GROUP BY card_id HAVING sum(amount) < 0 ORDER BY card_id`, tenant, orderID)
	if err != nil {
		return nil, 0, err
	}
	parts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (walletPart, error) {
		var p walletPart
		return p, r.Scan(&p.CardID, &p.Amount)
	})
	var total int64
	for _, p := range parts {
		total += p.Amount
	}
	return parts, total, err
}

// releaseWalletTx gives back to the cards what a failed or cancelled pending order had taken.
func releaseWalletTx(ctx context.Context, tx pgx.Tx, tenant, actor, orderID string) error {
	parts, _, err := walletPartsTx(ctx, tx, tenant, orderID)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if _, err := tx.Exec(ctx, `UPDATE gift_cards SET balance=balance+$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, p.CardID, p.Amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gift_card_tx (tenant_id, id, card_id, kind, amount, order_id, actor, note) VALUES ($1,$2,$3,'release',$4,$5,$6,'El pedido no se completó')`,
			tenant, newID("gtx_"), p.CardID, p.Amount, orderID, actor); err != nil {
			return err
		}
	}
	return nil
}

// walletRefund returns to a card the part of a refund that was paid with it.
// The movement id comes from the refund, so a retry never credits twice.
func (s *Service) walletRefund(ctx context.Context, tenant, refundID string, p refundPart) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var orderID string
		if err := tx.QueryRow(ctx, `SELECT order_id FROM payments WHERE tenant_id=$1 AND id=$2`, tenant, p.PaymentID).Scan(&orderID); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `INSERT INTO gift_card_tx (tenant_id, id, card_id, kind, amount, order_id, actor, note) VALUES ($1,$2,$3,'refund',$4,$5,'system','Reembolso')
			ON CONFLICT (tenant_id, id) DO NOTHING`, tenant, "gtx_"+refundID+"_"+p.PaymentID, p.Ref, p.Amount, orderID)
		if err != nil || ct.RowsAffected() == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE gift_cards SET balance=balance+$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, p.Ref, p.Amount)
		return err
	})
}

// creditRefund turns the part of a refund marked as store credit into a credit
// card for the customer of the order, and emails the code.
func (s *Service) creditRefund(ctx context.Context, tenant, refundID string, amount int64) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var orderID, cur string
		var cid *string
		var number int64
		if err := tx.QueryRow(ctx, `SELECT r.order_id, o.currency, o.customer_id, o.number FROM refunds r JOIN orders o ON o.tenant_id=r.tenant_id AND o.id=r.order_id
			WHERE r.tenant_id=$1 AND r.id=$2`, tenant, refundID).Scan(&orderID, &cur, &cid, &number); err != nil {
			return err
		}
		c, err := s.customerTx(ctx, tx, tenant, orderID)
		if err != nil {
			return fmt.Errorf("orders: store credit needs the customer of the order: %w", err)
		}
		in := IssueInput{Amount: amount, Currency: cur, Kind: "credit", RecipientEmail: c.Email, RecipientName: c.Name, Note: fmt.Sprintf("Saldo a favor por reembolso del pedido #%d", number)}
		if cid != nil {
			in.CustomerID = *cid
		}
		_, _, err = s.issueTx(ctx, tx, tenant, "system", "gc_"+strings.TrimPrefix(refundID, "rfd_"), in, orderID)
		return err
	})
}
