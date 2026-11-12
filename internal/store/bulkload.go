package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

// Start-up loaders: one query per kind for a whole store instead of one per
// product, SKU or price. With 50 000 products the store used to take 44 s to
// load (about 250 000 small queries); these stream the same rows at once.
// They read exactly what LoadProduct, LoadStock and LoadPrices read.

// EachProduct calls fn with every product of the tenant and its variants.
func (d *DB) EachProduct(ctx context.Context, tenant string, fn func(catalog.Product)) error {
	return d.eachProduct(ctx, tenant, nil, fn)
}

// EachProductIn calls fn with the products among ids that exist; ids that
// are not there (deleted) are simply not called.
func (d *DB) EachProductIn(ctx context.Context, tenant string, ids []string, fn func(catalog.Product)) error {
	if len(ids) == 0 {
		return nil
	}
	return d.eachProduct(ctx, tenant, ids, fn)
}

func (d *DB) eachProduct(ctx context.Context, tenant string, ids []string, fn func(catalog.Product)) error {
	type vrow struct {
		v       catalog.Variant
		product string
	}
	vrows, err := d.System.Query(ctx, `SELECT product_id, sku, title, options, barcode, weight_grams FROM variants
		WHERE tenant_id=$1 AND ($2::text[] IS NULL OR product_id = ANY($2)) ORDER BY product_id, position`, tenant, ids)
	if err != nil {
		return err
	}
	variants := map[string][]catalog.Variant{}
	for vrows.Next() {
		var r vrow
		var o []byte
		if err := vrows.Scan(&r.product, &r.v.SKU, &r.v.Title, &o, &r.v.Barcode, &r.v.WeightGrams); err != nil {
			vrows.Close()
			return err
		}
		_ = json.Unmarshal(o, &r.v.Options)
		variants[r.product] = append(variants[r.product], r.v)
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return err
	}
	rows, err := d.System.Query(ctx, `SELECT id, handle, title, description, brand, categories, attributes, status, updated_at, version,
			brand_id, product_type, tags, options, seo_title, seo_description, publish_at, media, variant_media, related, created_at
		FROM products WHERE tenant_id=$1 AND ($2::text[] IS NULL OR id = ANY($2)) ORDER BY id`, tenant, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p catalog.Product
		var attrs, opts, vmedia, related []byte
		if err := rows.Scan(&p.ID, &p.Handle, &p.Title, &p.Description, &p.Brand, &p.Categories, &attrs, &p.Status, &p.UpdatedAt, &p.Version,
			&p.BrandID, &p.Type, &p.Tags, &opts, &p.SEOTitle, &p.SEODescription, &p.PublishAt, &p.Media, &vmedia, &related, &p.CreatedAt); err != nil {
			return err
		}
		_ = json.Unmarshal(attrs, &p.Attributes)
		_ = json.Unmarshal(opts, &p.Options)
		_ = json.Unmarshal(related, &p.Related)
		vm := map[string]string{}
		_ = json.Unmarshal(vmedia, &vm)
		for _, v := range variants[p.ID] {
			v.MediaID = vm[v.SKU]
			p.Variants = append(p.Variants, v)
		}
		fn(p)
	}
	return rows.Err()
}

// AllStock returns the stock levels of every SKU of the tenant.
func (d *DB) AllStock(ctx context.Context, tenant string) (map[string][]inventory.Level, error) {
	rows, err := d.System.Query(ctx, `SELECT sku, location_id, on_hand, reserved FROM stock_levels WHERE tenant_id=$1 ORDER BY sku, location_id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]inventory.Level{}
	for rows.Next() {
		var sku string
		var l inventory.Level
		if err := rows.Scan(&sku, &l.LocationID, &l.OnHand, &l.Reserved); err != nil {
			return nil, err
		}
		out[sku] = append(out[sku], l)
	}
	return out, rows.Err()
}

// AllPrices returns the price tiers of every (channel, SKU) of the tenant.
func (d *DB) AllPrices(ctx context.Context, tenant string) (map[[2]string][]pricing.Tier, error) {
	rows, err := d.System.Query(ctx, `SELECT channel, sku, min_qty, amount, currency, compare_at FROM prices
		WHERE tenant_id=$1 ORDER BY channel, sku, min_qty`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string][]pricing.Tier{}
	for rows.Next() {
		var ch, sku, cur string
		var t pricing.Tier
		var cmp *int64
		if err := rows.Scan(&ch, &sku, &t.MinQty, &t.Unit.Amount, &cur, &cmp); err != nil {
			return nil, err
		}
		t.Unit.Currency = money.Currency(cur)
		if cmp != nil {
			c := money.New(*cmp, t.Unit.Currency)
			t.CompareAt = &c
		}
		k := [2]string{ch, sku}
		out[k] = append(out[k], t)
	}
	return out, rows.Err()
}

// StockFor returns the stock levels of the given SKUs (absent: no rows).
func (d *DB) StockFor(ctx context.Context, tenant string, skus []string) (map[string][]inventory.Level, error) {
	rows, err := d.System.Query(ctx, `SELECT sku, location_id, on_hand, reserved FROM stock_levels
		WHERE tenant_id=$1 AND sku = ANY($2) ORDER BY sku, location_id`, tenant, skus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]inventory.Level{}
	for rows.Next() {
		var sku string
		var l inventory.Level
		if err := rows.Scan(&sku, &l.LocationID, &l.OnHand, &l.Reserved); err != nil {
			return nil, err
		}
		out[sku] = append(out[sku], l)
	}
	return out, rows.Err()
}

// BundlesFor returns the components of the given kit SKUs.
func (d *DB) BundlesFor(ctx context.Context, tenant string, skus []string) (map[string][]inventory.Component, error) {
	rows, err := d.System.Query(ctx, `SELECT bundle_sku, component_sku, qty FROM bundle_components
		WHERE tenant_id=$1 AND bundle_sku = ANY($2) ORDER BY bundle_sku, position, component_sku`, tenant, skus)
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

// PreordersFor returns the preorders of the given SKUs.
func (d *DB) PreordersFor(ctx context.Context, tenant string, skus []string) (map[string][]Preorder, error) {
	rows, err := d.System.Query(ctx, `SELECT `+preorderCols+` FROM preorders WHERE tenant_id=$1 AND sku = ANY($2) ORDER BY sku`, tenant, skus)
	if err != nil {
		return nil, err
	}
	ps, err := pgx.CollectRows(rows, scanPreorder)
	if err != nil {
		return nil, err
	}
	out := map[string][]Preorder{}
	for _, p := range ps {
		out[p.SKU] = append(out[p.SKU], p)
	}
	return out, nil
}

// PricesFor returns the price tiers of the given (channel, SKU) pairs.
func (d *DB) PricesFor(ctx context.Context, tenant string, keys [][2]string) (map[[2]string][]pricing.Tier, error) {
	chs, skus := make([]string, len(keys)), make([]string, len(keys))
	for i, k := range keys {
		chs[i], skus[i] = k[0], k[1]
	}
	rows, err := d.System.Query(ctx, `SELECT p.channel, p.sku, p.min_qty, p.amount, p.currency, p.compare_at FROM prices p
		JOIN unnest($2::text[], $3::text[]) AS k(channel, sku) ON p.channel=k.channel AND p.sku=k.sku
		WHERE p.tenant_id=$1 ORDER BY p.channel, p.sku, p.min_qty`, tenant, chs, skus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string][]pricing.Tier{}
	for rows.Next() {
		var ch, sku, cur string
		var t pricing.Tier
		var cmp *int64
		if err := rows.Scan(&ch, &sku, &t.MinQty, &t.Unit.Amount, &cur, &cmp); err != nil {
			return nil, err
		}
		t.Unit.Currency = money.Currency(cur)
		if cmp != nil {
			c := money.New(*cmp, t.Unit.Currency)
			t.CompareAt = &c
		}
		k := [2]string{ch, sku}
		out[k] = append(out[k], t)
	}
	return out, rows.Err()
}
