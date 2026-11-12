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
	return loadProduct(ctx, d.System, tenant, id)
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
	// A preorder takes from its pool of units, not from stock.
	if ok, err := reservePreorderTx(ctx, tx, tenant, holder, sku, qty, ttl); ok || err != nil {
		return err
	}
	// A kit reserves its components: the kit itself holds no stock.
	comps, err := bundleTx(ctx, tx, tenant, sku)
	if err != nil {
		return err
	}
	if len(comps) > 0 {
		for _, c := range comps {
			if err := reserveSKUTx(ctx, tx, tenant, holder, c.SKU, qty*c.Qty, ttl); err != nil {
				return err
			}
		}
		return Emit(ctx, tx, tenant, TopicStock, sku, nil)
	}
	return reserveSKUTx(ctx, tx, tenant, holder, sku, qty, ttl)
}

func reserveSKUTx(ctx context.Context, tx pgx.Tx, tenant, holder, sku string, qty int64, ttl time.Duration) error {
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
		if x.loc == PreorderLocation {
			// Preorder units are not stock: a release gives them back to the pool.
			if !consume {
				if _, err := tx.Exec(ctx, `UPDATE preorders SET sold=GREATEST(sold-$3,0), updated_at=now() WHERE tenant_id=$1 AND sku=$2`, tenant, x.sku, x.qty); err != nil {
					return err
				}
			}
			if err := Emit(ctx, tx, tenant, TopicStock, x.sku, nil); err != nil {
				return err
			}
			continue
		}
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
	if location == PreorderLocation {
		if _, err := tx.Exec(ctx, `UPDATE preorders SET sold=GREATEST(sold-$3,0), updated_at=now() WHERE tenant_id=$1 AND sku=$2`, tenant, sku, qty); err != nil {
			return err
		}
		return Emit(ctx, tx, tenant, TopicStock, sku, nil)
	}
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

// ---------- preorders ----------

// PreorderLocation is the virtual location of units sold on preorder.
const PreorderLocation = "_preorder"

func reservePreorderTx(ctx context.Context, tx pgx.Tx, tenant, holder, sku string, qty int64, ttl time.Duration) (bool, error) {
	ct, err := tx.Exec(ctx, `UPDATE preorders SET sold=sold+$3, updated_at=now()
		WHERE tenant_id=$1 AND sku=$2 AND active AND (max_units=0 OR sold+$3 <= max_units)`, tenant, sku, qty)
	if err != nil {
		return false, err
	}
	if ct.RowsAffected() == 0 {
		var active bool
		err := tx.QueryRow(ctx, `SELECT active FROM preorders WHERE tenant_id=$1 AND sku=$2`, tenant, sku).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
			return false, nil // not on preorder: normal stock
		}
		if err != nil {
			return false, err
		}
		return true, fmt.Errorf("%w: %s preventa agotada", ErrInsufficient, sku)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO reservations (tenant_id, holder, sku, location_id, qty, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		tenant, holder, sku, PreorderLocation, qty, time.Now().Add(ttl)); err != nil {
		return true, err
	}
	return true, Emit(ctx, tx, tenant, TopicStock, sku, nil)
}

// Preorder is a SKU sold before it is in stock.
type Preorder struct {
	SKU        string    `json:"sku"`
	ShipsOn    string    `json:"shipsOn"` // YYYY-MM-DD
	DepositBps int       `json:"depositBps"`
	MaxUnits   int64     `json:"maxUnits"`
	Sold       int64     `json:"sold"`
	Active     bool      `json:"active"`
	Note       string    `json:"note"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

const preorderCols = `sku, to_char(ships_on,'YYYY-MM-DD'), deposit_bps, max_units, sold, active, note, updated_at`

func scanPreorder(r pgx.CollectableRow) (Preorder, error) {
	var p Preorder
	return p, r.Scan(&p.SKU, &p.ShipsOn, &p.DepositBps, &p.MaxUnits, &p.Sold, &p.Active, &p.Note, &p.UpdatedAt)
}

// Preorders lists the preorders of a tenant ("" = all), with the system pool
// when system is true (projector).
func (d *DB) Preorders(ctx context.Context, tenant, sku string, system bool) ([]Preorder, error) {
	q := `SELECT ` + preorderCols + ` FROM preorders WHERE tenant_id=$1 AND ($2='' OR sku=$2) ORDER BY sku`
	if system {
		rows, err := d.System.Query(ctx, q, tenant, sku)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, scanPreorder)
	}
	var out []Preorder
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, tenant, sku)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanPreorder)
		return err
	})
	return out, err
}

// SetPreorder creates or changes the preorder of sku. Units already sold are
// kept; the limit cannot go below them.
func (d *DB) SetPreorder(ctx context.Context, tenant, actor string, p Preorder) (Preorder, error) {
	var out Preorder
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `INSERT INTO preorders (tenant_id, sku, ships_on, deposit_bps, max_units, active, note) VALUES ($1,$2,$3::date,$4,$5,$6,$7)
			ON CONFLICT (tenant_id, sku) DO UPDATE SET ships_on=EXCLUDED.ships_on, deposit_bps=EXCLUDED.deposit_bps, max_units=EXCLUDED.max_units,
				active=EXCLUDED.active, note=EXCLUDED.note, updated_at=now()
			RETURNING `+preorderCols, tenant, p.SKU, p.ShipsOn, p.DepositBps, p.MaxUnits, p.Active, p.Note)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectExactlyOneRow(rows, scanPreorder); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicStock, p.SKU, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "preorder.set", p.SKU, p)
	})
	return out, err
}

// DeletePreorder ends a preorder that sold nothing; one with sales is
// deactivated instead, so the orders keep their record.
func (d *DB) DeletePreorder(ctx context.Context, tenant, actor, sku string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM preorders WHERE tenant_id=$1 AND sku=$2 AND sold=0`, tenant, sku); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE preorders SET active=false, updated_at=now() WHERE tenant_id=$1 AND sku=$2`, tenant, sku); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicStock, sku, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "preorder.ended", sku, nil)
	})
}

// ---------- kits ----------

func bundleTx(ctx context.Context, tx pgx.Tx, tenant, sku string) ([]inventory.Component, error) {
	rows, err := tx.Query(ctx, `SELECT component_sku, qty FROM bundle_components WHERE tenant_id=$1 AND bundle_sku=$2 ORDER BY position, component_sku`, tenant, sku)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (inventory.Component, error) {
		var c inventory.Component
		return c, r.Scan(&c.SKU, &c.Qty)
	})
}

// Bundle returns the components of a kit (empty for a normal SKU).
func (d *DB) Bundle(ctx context.Context, tenant, sku string) ([]inventory.Component, error) {
	var out []inventory.Component
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = bundleTx(ctx, tx, tenant, sku)
		return err
	})
	return out, err
}

// LoadBundles returns every kit of a tenant (system pool, for the projector).
func (d *DB) LoadBundles(ctx context.Context, tenant, sku string) (map[string][]inventory.Component, error) {
	q := `SELECT bundle_sku, component_sku, qty FROM bundle_components WHERE tenant_id=$1 ORDER BY bundle_sku, position, component_sku`
	args := []any{tenant}
	if sku != "" {
		q = `SELECT bundle_sku, component_sku, qty FROM bundle_components WHERE tenant_id=$1 AND bundle_sku=$2 ORDER BY position, component_sku`
		args = append(args, sku)
	}
	rows, err := d.System.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]inventory.Component{}
	for rows.Next() {
		var b string
		var c inventory.Component
		if err := rows.Scan(&b, &c.SKU, &c.Qty); err != nil {
			return nil, err
		}
		out[b] = append(out[b], c)
	}
	return out, rows.Err()
}

// ErrBundle explains why a kit cannot be saved.
var ErrBundle = errors.New("kit no válido")

// SetBundle replaces the components of kit sku (empty removes the kit). A
// component cannot itself be a kit, and a kit cannot be a component, so
// availability never needs more than one level.
func (d *DB) SetBundle(ctx context.Context, tenant, actor, sku string, comps []inventory.Component) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// One kit edit at a time per store, so two edits cannot nest kits.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || ':bundles'))`, tenant); err != nil {
			return err
		}
		if len(comps) > 0 {
			var used bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bundle_components WHERE tenant_id=$1 AND component_sku=$2)`, tenant, sku).Scan(&used); err != nil {
				return err
			}
			if used {
				return fmt.Errorf("%w: %s ya es componente de otro kit", ErrBundle, sku)
			}
		}
		for _, c := range comps {
			var nested bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bundle_components WHERE tenant_id=$1 AND bundle_sku=$2)`, tenant, c.SKU).Scan(&nested); err != nil {
				return err
			}
			if nested {
				return fmt.Errorf("%w: %s es un kit; un kit no puede contener otro kit", ErrBundle, c.SKU)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bundle_components WHERE tenant_id=$1 AND bundle_sku=$2`, tenant, sku); err != nil {
			return err
		}
		for i, c := range comps {
			if _, err := tx.Exec(ctx, `INSERT INTO bundle_components (tenant_id, bundle_sku, component_sku, qty, position) VALUES ($1,$2,$3,$4,$5)`,
				tenant, sku, c.SKU, c.Qty, i); err != nil {
				return err
			}
		}
		if err := Emit(ctx, tx, tenant, TopicStock, sku, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "bundle.set", sku, map[string]any{"components": comps})
	})
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
		// The price history (written by triggers) records who made the change.
		if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true)`, actor); err != nil {
			return err
		}
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

// APIKeyInfo describes a key without its secret.
type APIKeyInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"createdAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// ListAPIKeys lists the keys of a tenant.
func (d *DB) ListAPIKeys(ctx context.Context, tenant string) ([]APIKeyInfo, error) {
	rows, err := d.System.Query(ctx, `SELECT id, name, role, created_at, revoked_at FROM api_keys WHERE tenant_id=$1 ORDER BY created_at DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (APIKeyInfo, error) {
		var k APIKeyInfo
		return k, r.Scan(&k.ID, &k.Name, &k.Role, &k.CreatedAt, &k.RevokedAt)
	})
}
