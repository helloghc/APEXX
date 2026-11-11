package customers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// CartItem is one requested line of a tracked cart.
type CartItem struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// CartLine is a priced line of a tracked cart.
type CartLine struct {
	SKU   string `json:"sku"`
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
	Unit  int64  `json:"unit"`
	Total int64  `json:"total"`
}

// Cart is an abandoned (or recovered) cart of an identified shopper.
type Cart struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	Name             string     `json:"name,omitempty"`
	CustomerID       string     `json:"customerId,omitempty"`
	Lines            []CartLine `json:"lines"`
	ItemsCount       int        `json:"itemsCount"`
	Total            int64      `json:"total"`
	Currency         string     `json:"currency"`
	Channel          string     `json:"channel"`
	Status           string     `json:"status"`
	RemindedAt       *time.Time `json:"remindedAt,omitempty"`
	RecoveredOrderID string     `json:"recoveredOrderId,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type cartData struct {
	Email string     `json:"email"`
	Name  string     `json:"name"`
	Lines []CartLine `json:"lines"`
}

// ErrTemplateOff means the reminder template is switched off.
var ErrTemplateOff = errors.New("customers: reminder template is off")

// Track saves the cart of a shopper who typed an email at checkout. One live
// cart per email: tracking again replaces its contents.
func (s *Service) Track(ctx context.Context, tenant, site, channel, email, name string, items []CartItem) error {
	e, ok := NormalizeEmail(email)
	if !ok {
		return FieldError{"email", "Correo inválido"}
	}
	if len(items) == 0 || len(items) > 100 || s.Simulate == nil {
		return FieldError{"items", "El carrito debe tener entre 1 y 100 productos"}
	}
	lines, total, cur, ok := s.Simulate(tenant, channel, items)
	if !ok || len(lines) == 0 {
		return FieldError{"items", "El carrito no tiene productos disponibles"}
	}
	if len(name) > 120 {
		name = name[:120]
	}
	h, err := EmailHash(s.Keys, tenant, e)
	if err != nil {
		return err
	}
	count := 0
	for _, l := range lines {
		count += int(l.Qty)
	}
	raw, _ := json.Marshal(cartData{Email: e, Name: strings.TrimSpace(name), Lines: lines})
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('cart:' || $1 || ':' || encode($2,'hex')))`, tenant, h); err != nil {
			return err
		}
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM abandoned_carts WHERE tenant_id=$1 AND email_hash=$2 AND status IN ('open','reminded') FOR UPDATE`, tenant, h).Scan(&id)
		create := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !create {
			return err
		}
		if create {
			id = newID("crt_")
		}
		enc, err := s.Keys.Encrypt(tenant, raw, id)
		if err != nil {
			return err
		}
		var cid *string
		var found string
		if err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE tenant_id=$1 AND email_hash=$2`, tenant, h).Scan(&found); err == nil {
			cid = &found
		}
		if create {
			_, tokenHash := newToken() // replaced by a real token when a reminder is sent
			_, err = tx.Exec(ctx, `INSERT INTO abandoned_carts (tenant_id, id, email_hash, customer_id, data, items_count, total, currency, channel, site, token_hash)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, tenant, id, h, cid, enc, count, total, cur, channel, site, tokenHash)
		} else {
			_, err = tx.Exec(ctx, `UPDATE abandoned_carts SET data=$3, items_count=$4, total=$5, currency=$6, channel=$7, site=$8, customer_id=$9, updated_at=now()
				WHERE tenant_id=$1 AND id=$2`, tenant, id, enc, count, total, cur, channel, site, cid)
		}
		if err != nil {
			return err
		}
		return store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "cart.tracked", "total": total})
	})
}

// MarkRecoveredTx closes the live cart of an email when it places an order.
func MarkRecoveredTx(ctx context.Context, tx pgx.Tx, k *crypto.Keyring, tenant, email, orderID string) error {
	h, err := EmailHash(k, tenant, email)
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRow(ctx, `UPDATE abandoned_carts SET status='recovered', recovered_order_id=$3, updated_at=now()
		WHERE tenant_id=$1 AND email_hash=$2 AND status IN ('open','reminded') RETURNING id`, tenant, h, orderID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "cart.recovered", "orderId": orderID})
}

const cartCols = `id, COALESCE(customer_id,''), data, items_count, total, currency, channel, status, reminded_at, COALESCE(recovered_order_id,''), created_at, updated_at`

func (s *Service) scanCart(tenant string, r pgx.Row, pii bool) (Cart, error) {
	var c Cart
	var enc []byte
	if err := r.Scan(&c.ID, &c.CustomerID, &enc, &c.ItemsCount, &c.Total, &c.Currency, &c.Channel, &c.Status, &c.RemindedAt, &c.RecoveredOrderID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	c.Lines = []CartLine{}
	if plain, err := s.Keys.Decrypt(tenant, enc, c.ID); err == nil {
		var d cartData
		_ = json.Unmarshal(plain, &d)
		c.Email, c.Name, c.Lines = d.Email, d.Name, d.Lines
		if !pii {
			c.Email, c.Name = notify.MaskEmail(d.Email), maskName(d.Name)
		}
	}
	return c, nil
}

// CartStats summarizes abandoned carts of the last 30 days.
type CartStats struct {
	Open           int   `json:"open"`
	Reminded       int   `json:"reminded"`
	Recovered      int   `json:"recovered"`
	OpenValue      int64 `json:"openValue"`
	RecoveredValue int64 `json:"recoveredValue"`
}

// Carts lists tracked carts, most recent activity first.
func (s *Service) Carts(ctx context.Context, tenant, status, cursor string, limit int, pii bool) ([]Cart, string, CartStats, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	out := []Cart{}
	next := ""
	var st CartStats
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='open'), count(*) FILTER (WHERE status='reminded'), count(*) FILTER (WHERE status='recovered'),
			COALESCE(sum(total) FILTER (WHERE status IN ('open','reminded')),0), COALESCE(sum(total) FILTER (WHERE status='recovered'),0)
			FROM abandoned_carts WHERE tenant_id=$1 AND updated_at > now() - interval '30 days'`, tenant).Scan(&st.Open, &st.Reminded, &st.Recovered, &st.OpenValue, &st.RecoveredValue); err != nil {
			return err
		}
		q := `SELECT ` + cartCols + ` FROM abandoned_carts WHERE tenant_id=$1`
		args := []any{tenant}
		if status != "" {
			args = append(args, status)
			q += fmt.Sprintf(" AND status=$%d", len(args))
		}
		if cursor != "" {
			at, id, ok := strings.Cut(cursor, "|")
			ts, err := time.Parse(time.RFC3339Nano, at)
			if !ok || err != nil {
				return FieldError{"cursor", "Cursor inválido"}
			}
			args = append(args, ts, id)
			q += fmt.Sprintf(" AND (updated_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
		args = append(args, limit+1)
		q += fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT $%d", len(args))
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := s.scanCart(tenant, rows, pii)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if len(out) > limit {
		out = out[:limit]
		l := out[limit-1]
		next = l.UpdatedAt.Format(time.RFC3339Nano) + "|" + l.ID
	}
	return out, next, st, err
}

// Remind emails the reminder for a cart with a link that restores it. force
// sends even when the template is switched off (manual send from the admin).
func (s *Service) Remind(ctx context.Context, tenant, actor, id string, force bool) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var site string
		row := tx.QueryRow(ctx, `SELECT `+cartCols+`, site FROM abandoned_carts WHERE tenant_id=$1 AND id=$2 AND status IN ('open','reminded') FOR UPDATE`, tenant, id)
		c, err := s.scanCart(tenant, scanSite{row, &site}, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.Email == "" {
			return ErrNotFound
		}
		token, hash := newToken()
		if _, err := tx.Exec(ctx, `UPDATE abandoned_carts SET status='reminded', reminded_at=now(), token_hash=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, hash); err != nil {
			return err
		}
		d := notify.Data{Vars: map[string]string{"cliente.nombre": firstName(c.Name), "carrito.total": notify.Money(c.Total, c.Currency)},
			ActionURL: s.PublicURL + "/s/" + tenant + "/" + site + "/cart?recuperar=" + token}
		for _, l := range c.Lines {
			d.Lines = append(d.Lines, notify.Line{Title: l.Title, Qty: l.Qty, Total: notify.Money(l.Total, c.Currency)})
		}
		d.Totals = []notify.Row{{Label: "Total", Value: notify.Money(c.Total, c.Currency), Bold: true}}
		mid, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplAbandonedCart, To: c.Email, Name: c.Name, Data: d, CustomerID: c.CustomerID, Force: force,
			DedupeKey: "cart:" + id + ":" + s.now().UTC().Format("2006-01-02T15")})
		if err != nil {
			return err
		}
		if mid == "" {
			return ErrTemplateOff
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "cart.reminded"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "cart.reminded", id, map[string]any{})
	})
}

type scanSite struct {
	row  pgx.Row
	site *string
}

func (s scanSite) Scan(dst ...any) error { return s.row.Scan(append(dst, s.site)...) }

func firstName(n string) string {
	if f := strings.Fields(n); len(f) > 0 {
		return f[0]
	}
	return ""
}

// RemindDue sends the reminder for carts idle longer than the store's delay,
// only where the store turned the reminder template on. Each cart is reminded once.
func (s *Service) RemindDue(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT c.tenant_id, c.id FROM abandoned_carts c
		JOIN notification_templates t ON t.tenant_id=c.tenant_id AND t.key='abandoned_cart' AND t.enabled
		LEFT JOIN notification_settings ns ON ns.tenant_id=c.tenant_id
		WHERE c.status='open' AND c.updated_at < now() - make_interval(mins => COALESCE(ns.cart_reminder_minutes, 60))
		  AND c.updated_at > now() - interval '7 days' LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type ti struct{ t, id string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ti, error) {
		var x ti
		return x, r.Scan(&x.t, &x.id)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range list {
		err := s.Remind(ctx, x.t, "cart-reminder", x.id, false)
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrTemplateOff) && !errors.Is(err, crypto.ErrNoKey) {
			return n, err
		}
		if err == nil {
			n++
		}
	}
	return n, nil
}

// Restore returns the items of the cart behind a reminder link.
func (s *Service) Restore(ctx context.Context, tenant, token string) ([]CartLine, error) {
	if !strings.HasPrefix(token, "apc_") || len(token) > 100 {
		return nil, ErrNotFound
	}
	sum := sha256.Sum256([]byte(token))
	var c Cart
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, err = s.scanCart(tenant, tx.QueryRow(ctx, `SELECT `+cartCols+` FROM abandoned_carts WHERE tenant_id=$1 AND token_hash=$2 AND status IN ('open','reminded')
			AND updated_at > now() - interval '30 days'`, tenant, sum[:]), true)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return c.Lines, err
}

// DismissCart hides a cart from the reminders.
func (s *Service) DismissCart(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE abandoned_carts SET status='dismissed', updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status IN ('open','reminded')`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "cart.dismissed"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "cart.dismissed", id, map[string]any{})
	})
}
