package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

// Event topics written to the outbox.
const (
	TopicProduct   = "catalog.product.changed"
	TopicStock     = "inventory.changed"
	TopicPrice     = "pricing.changed"
	TopicChannel   = "channel.changed"
	TopicPromotion = "promotion.changed"
	TopicOrder     = "order.changed"
)

// Errors.
var (
	ErrNotFound     = errors.New("store: not found")
	ErrInsufficient = errors.New("store: insufficient stock")
	ErrVersion      = errors.New("store: version conflict")
)

// ---------- tenants and API keys ----------

// CreateTenant creates a tenant and its wrapped data key.
func (d *DB) CreateTenant(ctx context.Context, id, name string, wrappedKey []byte) error {
	return pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name) VALUES ($1,$2)`, id, name); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO tenant_keys (tenant_id, wrapped_key) VALUES ($1,$2)`, id, wrappedKey)
		return err
	})
}

// Tenants lists tenant ids.
func (d *DB) Tenants(ctx context.Context) ([]string, error) {
	rows, err := d.System.Query(ctx, `SELECT id FROM tenants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// WrappedKey returns the wrapped data key of a tenant.
func (d *DB) WrappedKey(ctx context.Context, tenant string) ([]byte, error) {
	var k []byte
	err := d.System.QueryRow(ctx, `SELECT wrapped_key FROM tenant_keys WHERE tenant_id=$1`, tenant).Scan(&k)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// Principal is an authenticated API key.
type Principal struct {
	KeyID  string
	Tenant string
	Role   string
	Name   string
}

// CreateAPIKey returns the full key "apx_<id>_<secret>". Only its hash is stored.
func (d *DB) CreateAPIKey(ctx context.Context, tenant, name, role string) (string, error) {
	idb := make([]byte, 6)
	sec := make([]byte, 24)
	if _, err := rand.Read(idb); err != nil {
		return "", err
	}
	if _, err := rand.Read(sec); err != nil {
		return "", err
	}
	id := "k" + hex.EncodeToString(idb)
	secret := hex.EncodeToString(sec)
	sum := sha256.Sum256([]byte(secret))
	_, err := d.System.Exec(ctx, `INSERT INTO api_keys (id, tenant_id, name, role, secret_hash) VALUES ($1,$2,$3,$4,$5)`,
		id, tenant, name, role, sum[:])
	if err != nil {
		return "", err
	}
	return "apx_" + id + "_" + secret, nil
}

// ErrBadKey means the key is malformed, unknown, revoked or wrong.
var ErrBadKey = errors.New("store: invalid api key")

// LookupAPIKey verifies a full key in constant time.
func (d *DB) LookupAPIKey(ctx context.Context, full string) (Principal, error) {
	parts := strings.SplitN(full, "_", 3)
	if len(parts) != 3 || parts[0] != "apx" {
		return Principal{}, ErrBadKey
	}
	var p Principal
	var hash []byte
	var revoked *time.Time
	err := d.System.QueryRow(ctx, `SELECT id, tenant_id, role, name, secret_hash, revoked_at FROM api_keys WHERE id=$1`, parts[1]).
		Scan(&p.KeyID, &p.Tenant, &p.Role, &p.Name, &hash, &revoked)
	sum := sha256.Sum256([]byte(parts[2]))
	if err != nil || revoked != nil || subtle.ConstantTimeCompare(sum[:], hash) != 1 {
		return Principal{}, ErrBadKey
	}
	return p, nil
}

// RevokeAPIKey revokes a key of tenant.
func (d *DB) RevokeAPIKey(ctx context.Context, tenant, id string) error {
	ct, err := d.System.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, id, tenant)
	if err == nil && ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---------- catalog ----------

// UpsertProduct writes a product and its variants. expectedVersion > 0 enables
// optimistic concurrency.
func (d *DB) UpsertProduct(ctx context.Context, tenant, actor string, p catalog.Product, expectedVersion int64) (int64, error) {
	var version int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		attrs, _ := json.Marshal(nonNilMap(p.Attributes))
		if p.Status == "" {
			p.Status = "active"
		}
		if p.Categories == nil {
			p.Categories = []string{}
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO products (tenant_id, id, handle, title, description, brand, categories, attributes, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id, id) DO UPDATE SET
				handle=EXCLUDED.handle, title=EXCLUDED.title, description=EXCLUDED.description,
				brand=EXCLUDED.brand, categories=EXCLUDED.categories, attributes=EXCLUDED.attributes,
				status=EXCLUDED.status, version=products.version+1, updated_at=now()
			WHERE $10 = 0 OR products.version = $10
			RETURNING version`,
			tenant, p.ID, p.Handle, p.Title, p.Description, p.Brand, p.Categories, attrs, p.Status, expectedVersion).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersion
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM variants WHERE tenant_id=$1 AND product_id=$2`, tenant, p.ID); err != nil {
			return err
		}
		for i, v := range p.Variants {
			opts, _ := json.Marshal(nonNilMap(v.Options))
			if _, err := tx.Exec(ctx, `INSERT INTO variants (tenant_id, sku, product_id, title, options, barcode, weight_grams, position)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, v.SKU, p.ID, v.Title, opts, v.Barcode, v.WeightGrams, i); err != nil {
				return fmt.Errorf("variant %s: %w", v.SKU, err)
			}
		}
		if err := Emit(ctx, tx, tenant, TopicProduct, p.ID, map[string]any{"version": version}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "product.upsert", p.ID, map[string]any{"version": version, "title": p.Title})
	})
	return version, err
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// LoadProduct reads a product with the system pool (projector use).
func (d *DB) LoadProduct(ctx context.Context, tenant, id string) (catalog.Product, error) {
	var p catalog.Product
	var attrs []byte
	err := d.System.QueryRow(ctx, `SELECT id, handle, title, description, brand, categories, attributes, status, updated_at
		FROM products WHERE tenant_id=$1 AND id=$2`, tenant, id).
		Scan(&p.ID, &p.Handle, &p.Title, &p.Description, &p.Brand, &p.Categories, &attrs, &p.Status, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(attrs, &p.Attributes)
	rows, err := d.System.Query(ctx, `SELECT sku, title, options, barcode, weight_grams FROM variants
		WHERE tenant_id=$1 AND product_id=$2 ORDER BY position`, tenant, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var v catalog.Variant
		var opts []byte
		if err := rows.Scan(&v.SKU, &v.Title, &opts, &v.Barcode, &v.WeightGrams); err != nil {
			return p, err
		}
		_ = json.Unmarshal(opts, &v.Options)
		p.Variants = append(p.Variants, v)
	}
	return p, rows.Err()
}

// ProductIDs lists all product ids of a tenant (system pool).
func (d *DB) ProductIDs(ctx context.Context, tenant string) ([]string, error) {
	rows, err := d.System.Query(ctx, `SELECT id FROM products WHERE tenant_id=$1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---------- inventory ----------

// SetStock sets on-hand stock of sku at location.
func (d *DB) SetStock(ctx context.Context, tenant, actor, sku, location string, onHand int64) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO stock_levels (tenant_id, sku, location_id, on_hand) VALUES ($1,$2,$3,$4)
			ON CONFLICT (tenant_id, sku, location_id) DO UPDATE SET on_hand=EXCLUDED.on_hand, updated_at=now()`,
			tenant, sku, location, onHand); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicStock, sku, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "stock.set", sku, map[string]any{"location": location, "onHand": onHand})
	})
}

// ReserveTx reserves qty of sku for holder inside tx, allocating across
// locations in order. Rows are locked, so concurrent checkouts cannot oversell.
func ReserveTx(ctx context.Context, tx pgx.Tx, tenant, holder, sku string, qty int64, ttl time.Duration) error {
	rows, err := tx.Query(ctx, `SELECT location_id, on_hand - reserved FROM stock_levels
		WHERE tenant_id=$1 AND sku=$2 ORDER BY location_id FOR UPDATE`, tenant, sku)
	if err != nil {
		return err
	}
	type loc struct {
		id    string
		avail int64
	}
	var locs []loc
	var total int64
	for rows.Next() {
		var l loc
		if err := rows.Scan(&l.id, &l.avail); err != nil {
			rows.Close()
			return err
		}
		if l.avail > 0 {
			locs = append(locs, l)
			total += l.avail
		}
	}
	rows.Close()
	if total < qty {
		return fmt.Errorf("%w: %s requested %d available %d", ErrInsufficient, sku, qty, total)
	}
	left := qty
	for _, l := range locs {
		if left == 0 {
			break
		}
		take := min(l.avail, left)
		if _, err := tx.Exec(ctx, `UPDATE stock_levels SET reserved=reserved+$4, updated_at=now()
			WHERE tenant_id=$1 AND sku=$2 AND location_id=$3`, tenant, sku, l.id, take); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO reservations (tenant_id, holder, sku, location_id, qty, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, tenant, holder, sku, l.id, take, time.Now().Add(ttl)); err != nil {
			return err
		}
		left -= take
	}
	return Emit(ctx, tx, tenant, TopicStock, sku, nil)
}

// ReleaseTx returns every reservation of holder to available stock.
func ReleaseTx(ctx context.Context, tx pgx.Tx, tenant, holder string) error {
	return settleTx(ctx, tx, tenant, holder, false)
}

// CommitTx turns the reservations of holder into a stock decrement (sale).
func CommitTx(ctx context.Context, tx pgx.Tx, tenant, holder string) error {
	return settleTx(ctx, tx, tenant, holder, true)
}

func settleTx(ctx context.Context, tx pgx.Tx, tenant, holder string, consume bool) error {
	rows, err := tx.Query(ctx, `DELETE FROM reservations WHERE tenant_id=$1 AND holder=$2 RETURNING sku, location_id, qty`, tenant, holder)
	if err != nil {
		return err
	}
	type r struct {
		sku, loc string
		qty      int64
	}
	var rs []r
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.sku, &x.loc, &x.qty); err != nil {
			rows.Close()
			return err
		}
		rs = append(rs, x)
	}
	rows.Close()
	seen := map[string]bool{}
	for _, x := range rs {
		q := `UPDATE stock_levels SET reserved=reserved-$4, updated_at=now() WHERE tenant_id=$1 AND sku=$2 AND location_id=$3`
		if consume {
			q = `UPDATE stock_levels SET reserved=reserved-$4, on_hand=on_hand-$4, updated_at=now() WHERE tenant_id=$1 AND sku=$2 AND location_id=$3`
		}
		if _, err := tx.Exec(ctx, q, tenant, x.sku, x.loc, x.qty); err != nil {
			return err
		}
		if !seen[x.sku] {
			seen[x.sku] = true
			if err := Emit(ctx, tx, tenant, TopicStock, x.sku, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// RestockTx adds qty back to on-hand stock (cancellation or return).
func RestockTx(ctx context.Context, tx pgx.Tx, tenant, sku, location string, qty int64) error {
	ct, err := tx.Exec(ctx, `UPDATE stock_levels SET on_hand=on_hand+$4, updated_at=now()
		WHERE tenant_id=$1 AND sku=$2 AND location_id=$3`, tenant, sku, location, qty)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO stock_levels (tenant_id, sku, location_id, on_hand) VALUES ($1,$2,$3,$4)`,
			tenant, sku, location, qty); err != nil {
			return err
		}
	}
	return Emit(ctx, tx, tenant, TopicStock, sku, nil)
}

// ExpireReservations releases reservations past their expiry for all tenants.
func (d *DB) ExpireReservations(ctx context.Context) (int, error) {
	rows, err := d.System.Query(ctx, `SELECT DISTINCT tenant_id, holder FROM reservations WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	type th struct{ t, h string }
	var list []th
	for rows.Next() {
		var x th
		if err := rows.Scan(&x.t, &x.h); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		if err := d.Tx(ctx, x.t, func(tx pgx.Tx) error { return ReleaseTx(ctx, tx, x.t, x.h) }); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// LoadStock reads the levels of sku (system pool).
func (d *DB) LoadStock(ctx context.Context, tenant, sku string) ([]inventory.Level, error) {
	rows, err := d.System.Query(ctx, `SELECT location_id, on_hand, reserved FROM stock_levels WHERE tenant_id=$1 AND sku=$2 ORDER BY location_id`, tenant, sku)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (inventory.Level, error) {
		var l inventory.Level
		err := r.Scan(&l.LocationID, &l.OnHand, &l.Reserved)
		return l, err
	})
}

// StockSKUs lists SKUs with stock rows (system pool).
func (d *DB) StockSKUs(ctx context.Context, tenant string) ([]string, error) {
	rows, err := d.System.Query(ctx, `SELECT DISTINCT sku FROM stock_levels WHERE tenant_id=$1`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---------- pricing ----------

// SetPrices replaces the tiers of sku in channel.
func (d *DB) SetPrices(ctx context.Context, tenant, actor, channel, sku string, tiers []pricing.Tier) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM prices WHERE tenant_id=$1 AND channel=$2 AND sku=$3`, tenant, channel, sku); err != nil {
			return err
		}
		for _, t := range tiers {
			var cmp *int64
			if t.CompareAt != nil {
				cmp = &t.CompareAt.Amount
			}
			if _, err := tx.Exec(ctx, `INSERT INTO prices (tenant_id, channel, sku, min_qty, amount, currency, compare_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`, tenant, channel, sku, t.MinQty, t.Unit.Amount, string(t.Unit.Currency), cmp); err != nil {
				return err
			}
		}
		if err := Emit(ctx, tx, tenant, TopicPrice, channel+"|"+sku, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "price.set", channel+"/"+sku, tiers)
	})
}

// LoadPrices reads tiers of sku in channel (system pool).
func (d *DB) LoadPrices(ctx context.Context, tenant, channel, sku string) ([]pricing.Tier, error) {
	rows, err := d.System.Query(ctx, `SELECT min_qty, amount, currency, compare_at FROM prices
		WHERE tenant_id=$1 AND channel=$2 AND sku=$3 ORDER BY min_qty`, tenant, channel, sku)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanTier)
}

func scanTier(r pgx.CollectableRow) (pricing.Tier, error) {
	var t pricing.Tier
	var cur string
	var cmp *int64
	if err := r.Scan(&t.MinQty, &t.Unit.Amount, &cur, &cmp); err != nil {
		return t, err
	}
	t.Unit.Currency = money.Currency(cur)
	if cmp != nil {
		c := money.New(*cmp, t.Unit.Currency)
		t.CompareAt = &c
	}
	return t, nil
}

// PriceKeys lists (channel, sku) pairs of a tenant (system pool).
func (d *DB) PriceKeys(ctx context.Context, tenant string) ([][2]string, error) {
	rows, err := d.System.Query(ctx, `SELECT DISTINCT channel, sku FROM prices WHERE tenant_id=$1`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]string, error) {
		var k [2]string
		err := r.Scan(&k[0], &k[1])
		return k, err
	})
}

// ---------- channels and promotions ----------

// SetChannel stores a channel configuration as JSON.
func (d *DB) SetChannel(ctx context.Context, tenant, actor, id string, cfg any) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO channels (tenant_id, id, config) VALUES ($1,$2,$3)
			ON CONFLICT (tenant_id, id) DO UPDATE SET config=EXCLUDED.config`, tenant, id, b); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicChannel, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "channel.set", id, json.RawMessage(b))
	})
}

// Channels returns raw channel configs (system pool).
func (d *DB) Channels(ctx context.Context, tenant string) (map[string]json.RawMessage, error) {
	rows, err := d.System.Query(ctx, `SELECT id, config FROM channels WHERE tenant_id=$1`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

// PromotionRow is a stored promotion.
type PromotionRow struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Rule   json.RawMessage `json:"rule"`
	Active bool            `json:"active"`
}

// SetPromotion stores a promotion.
func (d *DB) SetPromotion(ctx context.Context, tenant, actor string, p PromotionRow) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO promotions (tenant_id, id, kind, rule, active) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, id) DO UPDATE SET kind=EXCLUDED.kind, rule=EXCLUDED.rule, active=EXCLUDED.active`,
			tenant, p.ID, p.Kind, []byte(p.Rule), p.Active); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicPromotion, p.ID, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "promotion.set", p.ID, p)
	})
}

// Promotions lists active promotions (system pool).
func (d *DB) Promotions(ctx context.Context, tenant string) ([]PromotionRow, error) {
	rows, err := d.System.Query(ctx, `SELECT id, kind, rule, active FROM promotions WHERE tenant_id=$1 AND active ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PromotionRow, error) {
		var p PromotionRow
		var b []byte
		err := r.Scan(&p.ID, &p.Kind, &b, &p.Active)
		p.Rule = b
		return p, err
	})
}

// ---------- outbox ----------

// OutboxEvent is a row of the outbox.
type OutboxEvent struct {
	ID        int64
	Tenant    string
	Topic     string
	Key       string
	CreatedAt time.Time
}

// OutboxAfter returns up to limit events with id > after (system pool).
func (d *DB) OutboxAfter(ctx context.Context, after int64, limit int) ([]OutboxEvent, error) {
	rows, err := d.System.Query(ctx, `SELECT id, tenant_id, topic, key, created_at FROM outbox WHERE id > $1 ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OutboxEvent, error) {
		var e OutboxEvent
		err := r.Scan(&e.ID, &e.Tenant, &e.Topic, &e.Key, &e.CreatedAt)
		return e, err
	})
}

// OutboxMax returns the highest outbox id.
func (d *DB) OutboxMax(ctx context.Context) (int64, error) {
	var id int64
	err := d.System.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM outbox`).Scan(&id)
	return id, err
}

// PruneOutbox deletes processed events older than keep.
func (d *DB) PruneOutbox(ctx context.Context, upTo int64, keep time.Duration) error {
	_, err := d.System.Exec(ctx, `DELETE FROM outbox WHERE id <= $1 AND created_at < now() - $2::interval`, upTo, fmt.Sprintf("%d seconds", int(keep.Seconds())))
	return err
}

// ---------- payment accounts ----------

// PaymentAccount is the Stripe configuration of a store.
type PaymentAccount struct {
	Mode           string `json:"mode"` // connect | own_keys
	AccountID      string `json:"accountId,omitempty"`
	PublishableKey string `json:"publishableKey,omitempty"`
	SecretEnc      []byte `json:"-"`
	WebhookEnc     []byte `json:"-"`
	FeeBps         int64  `json:"feeBps"`
}

// SetPaymentAccount stores the account (secrets already encrypted by the caller).
func (d *DB) SetPaymentAccount(ctx context.Context, tenant, actor string, a PaymentAccount) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO payment_accounts (tenant_id, mode, account_id, publishable_key, secret_enc, webhook_secret_enc, fee_bps)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (tenant_id) DO UPDATE SET mode=EXCLUDED.mode, account_id=EXCLUDED.account_id, publishable_key=EXCLUDED.publishable_key,
				secret_enc=EXCLUDED.secret_enc, webhook_secret_enc=EXCLUDED.webhook_secret_enc, fee_bps=EXCLUDED.fee_bps, updated_at=now()`,
			tenant, a.Mode, a.AccountID, a.PublishableKey, a.SecretEnc, a.WebhookEnc, a.FeeBps); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "payments.account.set", a.Mode, map[string]any{"mode": a.Mode, "accountId": a.AccountID})
	})
}

// GetPaymentAccount reads the account of a store (system pool).
func (d *DB) GetPaymentAccount(ctx context.Context, tenant string) (PaymentAccount, error) {
	var a PaymentAccount
	err := d.System.QueryRow(ctx, `SELECT mode, account_id, publishable_key, secret_enc, webhook_secret_enc, fee_bps FROM payment_accounts WHERE tenant_id=$1`, tenant).
		Scan(&a.Mode, &a.AccountID, &a.PublishableKey, &a.SecretEnc, &a.WebhookEnc, &a.FeeBps)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// RotateTenantKeys re-wraps every tenant key atomically.
func (d *DB) RotateTenantKeys(ctx context.Context, rewrap func(tenant string, wrapped []byte) ([]byte, error)) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id, wrapped_key FROM tenant_keys ORDER BY tenant_id FOR UPDATE`)
		if err != nil {
			return err
		}
		type kv struct {
			t string
			w []byte
		}
		var list []kv
		for rows.Next() {
			var x kv
			if err := rows.Scan(&x.t, &x.w); err != nil {
				rows.Close()
				return err
			}
			list = append(list, x)
		}
		rows.Close()
		for _, x := range list {
			nw, err := rewrap(x.t, x.w)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE tenant_keys SET wrapped_key=$2 WHERE tenant_id=$1`, x.t, nw); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
