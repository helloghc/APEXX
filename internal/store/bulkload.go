package store

import (
	"context"
	"encoding/json"

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
	type vrow struct {
		v       catalog.Variant
		product string
	}
	vrows, err := d.System.Query(ctx, `SELECT product_id, sku, title, options, barcode, weight_grams FROM variants
		WHERE tenant_id=$1 ORDER BY product_id, position`, tenant)
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
		FROM products WHERE tenant_id=$1 ORDER BY id`, tenant)
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
