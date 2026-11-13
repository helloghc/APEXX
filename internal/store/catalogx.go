package store

// Advanced catalog persistence: full product writes (with prices and stock in
// the same transaction), bulk edits, scheduled publishing, the category tree,
// brands, collections, attribute definitions, vocabularies and the loaders the
// projector uses. Every write is one tenant transaction that also appends the
// outbox events and the audit record.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helloghc/apexx/internal/catalog"
)

// Outbox topics of the advanced catalog.
const (
	TopicCategory   = "catalog.category.changed"
	TopicBrand      = "catalog.brand.changed"
	TopicCollection = "catalog.collection.changed"
	TopicAttribute  = "catalog.attribute.changed"
	TopicMedia      = "catalog.media.changed"
)

// Errors of the advanced catalog.
var (
	ErrConflict    = errors.New("store: already exists")
	ErrHasChildren = errors.New("store: category has children")
	ErrCycle       = errors.New("store: category cycle")
	ErrInUse       = errors.New("store: in use")
)

// RefError says a write points to something that does not exist.
type RefError struct {
	Field string
	ID    string
}

func (e *RefError) Error() string { return fmt.Sprintf("store: %s %q not found", e.Field, e.ID) }

// NewID returns prefix + 12 random hex characters.
func NewID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// querier is satisfied by a pool and by a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------- products ----------

func loadProduct(ctx context.Context, q querier, tenant, id string) (catalog.Product, error) {
	var p catalog.Product
	var attrs, opts, vmedia, related []byte
	err := q.QueryRow(ctx, `SELECT id, handle, title, description, brand, categories, attributes, status, updated_at, version,
			brand_id, product_type, tags, options, seo_title, seo_description, publish_at, media, variant_media, related, created_at
		FROM products WHERE tenant_id=$1 AND id=$2`, tenant, id).
		Scan(&p.ID, &p.Handle, &p.Title, &p.Description, &p.Brand, &p.Categories, &attrs, &p.Status, &p.UpdatedAt, &p.Version,
			&p.BrandID, &p.Type, &p.Tags, &opts, &p.SEOTitle, &p.SEODescription, &p.PublishAt, &p.Media, &vmedia, &related, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(attrs, &p.Attributes)
	_ = json.Unmarshal(opts, &p.Options)
	_ = json.Unmarshal(related, &p.Related)
	vm := map[string]string{}
	_ = json.Unmarshal(vmedia, &vm)
	rows, err := q.Query(ctx, `SELECT sku, title, options, barcode, weight_grams FROM variants
		WHERE tenant_id=$1 AND product_id=$2 ORDER BY position`, tenant, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var v catalog.Variant
		var o []byte
		if err := rows.Scan(&v.SKU, &v.Title, &o, &v.Barcode, &v.WeightGrams); err != nil {
			return p, err
		}
		_ = json.Unmarshal(o, &v.Options)
		v.MediaID = vm[v.SKU]
		p.Variants = append(p.Variants, v)
	}
	return p, rows.Err()
}

// PriceInput is the unit price of a SKU for quantity 1 in a channel.
type PriceInput struct {
	Amount    int64  `json:"amount"`
	CompareAt *int64 `json:"compareAt,omitempty"`
	Currency  string `json:"currency"`
}

// ProductWrite is a full product save. Prices and Stock are optional: SKUs
// that are absent keep what they had.
type ProductWrite struct {
	Product catalog.Product
	Channel string
	Prices  map[string]PriceInput       // by SKU
	Stock   map[string]map[string]int64 // SKU -> location -> on hand
	Create  bool                        // fail with ErrConflict when the id exists
}

// SaveProduct writes the product with every advanced field, its variants and
// optionally prices and stock, atomically. expectedVersion > 0 enables
// optimistic concurrency.
func (d *DB) SaveProduct(ctx context.Context, tenant, actor string, w ProductWrite, expectedVersion int64) (int64, error) {
	var version int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		version, err = saveProductTx(ctx, tx, tenant, actor, w, expectedVersion)
		if err != nil {
			return err
		}
		return d.sandboxRoom(ctx, tx, tenant)
	})
	return version, err
}

func saveProductTx(ctx context.Context, tx pgx.Tx, tenant, actor string, w ProductWrite, expectedVersion int64) (int64, error) {
	p := w.Product
	if p.Status == "" {
		p.Status = "active"
	}
	if err := checkCategoriesTx(ctx, tx, tenant, p.Categories); err != nil {
		return 0, err
	}
	if p.BrandID != "" {
		err := tx.QueryRow(ctx, `SELECT name FROM brands WHERE tenant_id=$1 AND id=$2`, tenant, p.BrandID).Scan(&p.Brand)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, &RefError{"brandId", p.BrandID}
		}
		if err != nil {
			return 0, err
		}
	}
	attrs, _ := json.Marshal(nonNilMap(p.Attributes))
	opts, _ := json.Marshal(p.Options)
	if p.Options == nil {
		opts = []byte("[]")
	}
	vmedia := map[string]string{}
	for _, v := range p.Variants {
		if v.MediaID != "" {
			vmedia[v.SKU] = v.MediaID
		}
	}
	vm, _ := json.Marshal(vmedia)
	related, _ := json.Marshal(p.Related)

	rows, err := tx.Query(ctx, `SELECT sku FROM variants WHERE tenant_id=$1 AND product_id=$2`, tenant, p.ID)
	if err != nil {
		return 0, err
	}
	oldSKUs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}

	var version int64
	args := []any{tenant, p.ID, p.Handle, p.Title, p.Description, p.Brand, nonNil(p.Categories), attrs, p.Status,
		p.BrandID, p.Type, nonNil(p.Tags), opts, p.SEOTitle, p.SEODescription, p.PublishAt, nonNil(p.Media), vm, related}
	if w.Create {
		err = tx.QueryRow(ctx, `
			INSERT INTO products (tenant_id, id, handle, title, description, brand, categories, attributes, status,
				brand_id, product_type, tags, options, seo_title, seo_description, publish_at, media, variant_media, related)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
			RETURNING version`, args...).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, `
			INSERT INTO products (tenant_id, id, handle, title, description, brand, categories, attributes, status,
				brand_id, product_type, tags, options, seo_title, seo_description, publish_at, media, variant_media, related)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
			ON CONFLICT (tenant_id, id) DO UPDATE SET
				handle=EXCLUDED.handle, title=EXCLUDED.title, description=EXCLUDED.description,
				brand=EXCLUDED.brand, categories=EXCLUDED.categories, attributes=EXCLUDED.attributes, status=EXCLUDED.status,
				brand_id=EXCLUDED.brand_id, product_type=EXCLUDED.product_type, tags=EXCLUDED.tags, options=EXCLUDED.options,
				seo_title=EXCLUDED.seo_title, seo_description=EXCLUDED.seo_description, publish_at=EXCLUDED.publish_at,
				media=EXCLUDED.media, variant_media=EXCLUDED.variant_media, related=EXCLUDED.related,
				version=products.version+1, updated_at=now()
			WHERE $20 = 0 OR products.version = $20
			RETURNING version`, append(args, expectedVersion)...).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrVersion
	}
	if isUnique(err) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	// The writes go to the database in two pipelined batches instead of one
	// round trip each (about 15 for a product with two variants), in the
	// same order as before: variants first, then the SKUs that left, then
	// prices, stock and events.
	keep := map[string]bool{}
	first := &pgx.Batch{}
	first.Queue(`DELETE FROM variants WHERE tenant_id=$1 AND product_id=$2`, tenant, p.ID)
	for i, v := range p.Variants {
		keep[v.SKU] = true
		o, _ := json.Marshal(nonNilMap(v.Options))
		first.Queue(`INSERT INTO variants (tenant_id, sku, product_id, title, options, barcode, weight_grams, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, v.SKU, p.ID, v.Title, o, v.Barcode, v.WeightGrams, i)
	}
	if err := sendBatch(ctx, tx, first, func(i int, err error) error {
		if i == 0 {
			return err
		}
		sku := p.Variants[i-1].SKU
		if isUnique(err) {
			return fmt.Errorf("%w: sku %s", ErrConflict, sku)
		}
		return fmt.Errorf("variant %s: %w", sku, err)
	}); err != nil {
		return 0, err
	}
	// A SKU that left the product takes its prices and idle stock with it.
	for _, sku := range oldSKUs {
		if keep[sku] {
			continue
		}
		if err := dropSKUTx(ctx, tx, tenant, sku); err != nil {
			return 0, err
		}
	}
	second := &pgx.Batch{}
	emit := func(topic, key string, payload any) error {
		bs, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		second.Queue(`INSERT INTO outbox (tenant_id, topic, key, payload) VALUES ($1,$2,$3,$4)`, tenant, topic, key, bs)
		return nil
	}
	for sku, pr := range w.Prices {
		if !keep[sku] {
			continue
		}
		second.Queue(`INSERT INTO prices (tenant_id, channel, sku, min_qty, amount, currency, compare_at)
			VALUES ($1,$2,$3,1,$4,$5,$6)
			ON CONFLICT (tenant_id, channel, sku, min_qty) DO UPDATE SET amount=EXCLUDED.amount, currency=EXCLUDED.currency, compare_at=EXCLUDED.compare_at`,
			tenant, w.Channel, sku, pr.Amount, pr.Currency, pr.CompareAt)
		if err := emit(TopicPrice, w.Channel+"|"+sku, nil); err != nil {
			return 0, err
		}
	}
	for sku, locs := range w.Stock {
		if !keep[sku] {
			continue
		}
		for loc, onHand := range locs {
			second.Queue(`INSERT INTO stock_levels (tenant_id, sku, location_id, on_hand) VALUES ($1,$2,$3,$4)
				ON CONFLICT (tenant_id, sku, location_id) DO UPDATE SET on_hand=EXCLUDED.on_hand, updated_at=now()`,
				tenant, sku, loc, onHand)
			if strings.HasPrefix(tenant, SandboxPrefix) {
				second.Queue(`DELETE FROM sandbox_stock_moves WHERE tenant_id=$1 AND (sku, location_id) IN (SELECT s, l FROM unnest($2::text[], $3::text[]) AS x(s, l))`, tenant, []string{sku}, []string{loc})
			}
		}
		if err := emit(TopicStock, sku, nil); err != nil {
			return 0, err
		}
	}
	if err := emit(TopicProduct, p.ID, map[string]any{"version": version}); err != nil {
		return 0, err
	}
	if err := sendBatch(ctx, tx, second, func(_ int, err error) error { return err }); err != nil {
		return 0, err
	}
	return version, Audit(ctx, tx, tenant, actor, "product.save", p.ID, map[string]any{
		"version": version, "title": p.Title, "status": p.Status, "variants": len(p.Variants), "prices": len(w.Prices), "stock": len(w.Stock)})
}

func dropSKUTx(ctx context.Context, tx pgx.Tx, tenant, sku string) error {
	rows, err := tx.Query(ctx, `DELETE FROM prices WHERE tenant_id=$1 AND sku=$2 RETURNING channel`, tenant, sku)
	if err != nil {
		return err
	}
	chans, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, ch := range chans {
		if seen[ch] {
			continue
		}
		seen[ch] = true
		if err := Emit(ctx, tx, tenant, TopicPrice, ch+"|"+sku, nil); err != nil {
			return err
		}
	}
	// Stock that is reserved by an open checkout stays until it settles.
	ct, err := tx.Exec(ctx, `DELETE FROM stock_levels WHERE tenant_id=$1 AND sku=$2 AND reserved=0`, tenant, sku)
	if err != nil {
		return err
	}
	if ct.RowsAffected() > 0 {
		return Emit(ctx, tx, tenant, TopicStock, sku, nil)
	}
	return nil
}

// DeleteProduct removes a product, its variants, their prices and idle stock,
// and takes it out of manual collections.
func (d *DB) DeleteProduct(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error { return deleteProductTx(ctx, tx, tenant, actor, id) })
}

// DeleteProducts deletes up to 500 products in one transaction with the
// same effects as DeleteProduct on each (variants, prices, idle stock,
// manual collections, events and one audit entry per product), but with a
// handful of set statements instead of a dozen round trips per product. Ids
// that no longer exist are skipped; it returns how many were deleted.
func (d *DB) DeleteProducts(ctx context.Context, tenant, actor string, ids []string) (int, error) {
	n := 0
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// SKUs of each product, before the cascade removes the variants.
		rows, err := tx.Query(ctx, `SELECT product_id, sku FROM variants WHERE tenant_id=$1 AND product_id = ANY($2)`, tenant, ids)
		if err != nil {
			return err
		}
		skusOf := map[string][]string{}
		for rows.Next() {
			var pid, sku string
			if err := rows.Scan(&pid, &sku); err != nil {
				rows.Close()
				return err
			}
			skusOf[pid] = append(skusOf[pid], sku)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		del, err := tx.Query(ctx, `DELETE FROM products WHERE tenant_id=$1 AND id = ANY($2) RETURNING id, title`, tenant, ids)
		if err != nil {
			return err
		}
		type gone struct{ id, title string }
		var deleted []gone
		for del.Next() {
			var g gone
			if err := del.Scan(&g.id, &g.title); err != nil {
				del.Close()
				return err
			}
			deleted = append(deleted, g)
		}
		del.Close()
		if err := del.Err(); err != nil {
			return err
		}
		n = len(deleted)
		if n == 0 {
			return nil
		}
		// Only the SKUs of products that were really deleted.
		var skus []string
		for _, g := range deleted {
			skus = append(skus, skusOf[g.id]...)
		}
		b := &pgx.Batch{}
		// Prices and idle stock of those SKUs, one event per channel|sku and
		// per sku, as dropSKUTx emits them.
		b.Queue(`WITH p AS (DELETE FROM prices WHERE tenant_id=$1 AND sku = ANY($2) RETURNING channel, sku)
			INSERT INTO outbox (tenant_id, topic, key, payload)
			SELECT DISTINCT $1, $3, channel || '|' || sku, 'null'::jsonb FROM p`, tenant, skus, TopicPrice)
		b.Queue(`WITH s AS (DELETE FROM stock_levels WHERE tenant_id=$1 AND sku = ANY($2) AND reserved=0 RETURNING sku)
			INSERT INTO outbox (tenant_id, topic, key, payload)
			SELECT DISTINCT $1, $3, sku, 'null'::jsonb FROM s`, tenant, skus, TopicStock)
		gids := make([]string, len(deleted))
		for i, g := range deleted {
			gids[i] = g.id
		}
		b.Queue(`WITH c AS (UPDATE collections SET product_ids = ARRAY(SELECT x FROM unnest(product_ids) WITH ORDINALITY AS u(x, o) WHERE NOT x = ANY($2) ORDER BY o), updated_at=now()
				WHERE tenant_id=$1 AND product_ids && $2 RETURNING id)
			INSERT INTO outbox (tenant_id, topic, key, payload) SELECT $1, $3, id, 'null'::jsonb FROM c`, tenant, gids, TopicCollection)
		b.Queue(`INSERT INTO outbox (tenant_id, topic, key, payload) SELECT $1, $2, x, '{"deleted":true}'::jsonb FROM unnest($3::text[]) x`,
			tenant, TopicProduct, gids)
		// Their vectors and graph edges go with them (no AI leftovers).
		b.Queue(`DELETE FROM ai_chunks WHERE tenant_id=$1 AND source_kind='product' AND source_id = ANY($2)`, tenant, gids)
		b.Queue(`DELETE FROM kg_edges WHERE tenant_id=$1 AND ((src_kind='product' AND src_id = ANY($2)) OR (dst_kind='product' AND dst_id = ANY($2)))`, tenant, gids)
		b.Queue(`DELETE FROM tenant_docs WHERE tenant_id=$1 AND kind='datasheet' AND id = ANY($2)`, tenant, gids)
		b.Queue(`DELETE FROM ai_chunks WHERE tenant_id=$1 AND source_kind='datasheet' AND source_id = ANY($2)`, tenant, gids)
		b.Queue(`DELETE FROM ai_suggestions WHERE tenant_id=$1 AND target_kind='product' AND target_id = ANY($2)`, tenant, gids)
		if err := sendBatch(ctx, tx, b, func(_ int, err error) error { return err }); err != nil {
			return err
		}
		entries := make([]auditEntry, len(deleted))
		for i, g := range deleted {
			entries[i] = auditEntry{action: "product.delete", target: g.id, data: map[string]any{"title": g.title, "skus": len(skusOf[g.id])}}
		}
		return auditMany(ctx, tx, tenant, actor, entries)
	})
	return n, err
}

func deleteProductTx(ctx context.Context, tx pgx.Tx, tenant, actor, id string) error {
	{
		rows, err := tx.Query(ctx, `SELECT sku FROM variants WHERE tenant_id=$1 AND product_id=$2`, tenant, id)
		if err != nil {
			return err
		}
		skus, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		var title string
		err = tx.QueryRow(ctx, `DELETE FROM products WHERE tenant_id=$1 AND id=$2 RETURNING title`, tenant, id).Scan(&title)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		for _, sku := range skus {
			if err := dropSKUTx(ctx, tx, tenant, sku); err != nil {
				return err
			}
		}
		crow, err := tx.Query(ctx, `UPDATE collections SET product_ids = array_remove(product_ids, $2), updated_at=now()
			WHERE tenant_id=$1 AND $2 = ANY(product_ids) RETURNING id`, tenant, id)
		if err != nil {
			return err
		}
		cols, err := pgx.CollectRows(crow, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, c := range cols {
			if err := Emit(ctx, tx, tenant, TopicCollection, c, nil); err != nil {
				return err
			}
		}
		if err := Emit(ctx, tx, tenant, TopicProduct, id, map[string]any{"deleted": true}); err != nil {
			return err
		}
		if err := dropAIOf(ctx, tx, tenant, "product", id); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "product.delete", id, map[string]any{"title": title, "skus": len(skus)})
	}
}

// BulkOp is one bulk edit over a selection of products.
type BulkOp struct {
	Action     string // status | category_add | category_remove | brand | price_percent
	Status     string
	CategoryID string
	BrandID    string
	Bps        int64 // price_percent: +1000 = +10 %, -2500 = -25 %
	KeepOld    bool  // price_percent with a discount: keep the old price as "before" price
	Channel    string
}

// BulkProducts applies op to ids in one transaction and returns how many
// products (or, for prices, SKUs) actually changed.
func (d *DB) BulkProducts(ctx context.Context, tenant, actor string, ids []string, op BulkOp) (int, error) {
	changed := 0
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var rows pgx.Rows
		var err error
		touch := "version=version+1, updated_at=now()"
		switch op.Action {
		case "status":
			rows, err = tx.Query(ctx, `UPDATE products SET status=$3, publish_at=NULL, `+touch+`
				WHERE tenant_id=$1 AND id = ANY($2) AND status <> $3 RETURNING id`, tenant, ids, op.Status)
		case "category_add":
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM categories WHERE tenant_id=$1 AND id=$2)`, tenant, op.CategoryID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return &RefError{"categoryId", op.CategoryID}
			}
			rows, err = tx.Query(ctx, `UPDATE products SET categories = array_append(categories, $3), `+touch+`
				WHERE tenant_id=$1 AND id = ANY($2) AND NOT ($3 = ANY(categories)) RETURNING id`, tenant, ids, op.CategoryID)
		case "category_remove":
			rows, err = tx.Query(ctx, `UPDATE products SET categories = array_remove(categories, $3), `+touch+`
				WHERE tenant_id=$1 AND id = ANY($2) AND $3 = ANY(categories) RETURNING id`, tenant, ids, op.CategoryID)
		case "brand":
			name := ""
			if op.BrandID != "" {
				e := tx.QueryRow(ctx, `SELECT name FROM brands WHERE tenant_id=$1 AND id=$2`, tenant, op.BrandID).Scan(&name)
				if errors.Is(e, pgx.ErrNoRows) {
					return &RefError{"brandId", op.BrandID}
				}
				if e != nil {
					return e
				}
			}
			rows, err = tx.Query(ctx, `UPDATE products SET brand_id=$3, brand=$4, `+touch+`
				WHERE tenant_id=$1 AND id = ANY($2) AND (brand_id <> $3 OR brand <> $4) RETURNING id`, tenant, ids, op.BrandID, name)
		case "price_percent":
			prow, e := tx.Query(ctx, `UPDATE prices SET
					compare_at = CASE WHEN $5 AND $4 < 0 AND compare_at IS NULL THEN amount ELSE compare_at END,
					amount = GREATEST(0, round(amount::numeric * (10000 + $4) / 10000))::bigint
				WHERE tenant_id=$1 AND channel=$3 AND sku IN (SELECT sku FROM variants WHERE tenant_id=$1 AND product_id = ANY($2))
				RETURNING sku`, tenant, ids, op.Channel, op.Bps, op.KeepOld)
			if e != nil {
				return e
			}
			skus, e := pgx.CollectRows(prow, pgx.RowTo[string])
			if e != nil {
				return e
			}
			seen := map[string]bool{}
			for _, sku := range skus {
				if seen[sku] {
					continue
				}
				seen[sku] = true
				if err := Emit(ctx, tx, tenant, TopicPrice, op.Channel+"|"+sku, nil); err != nil {
					return err
				}
			}
			changed = len(seen)
			return Audit(ctx, tx, tenant, actor, "product.bulk.price_percent", op.Channel, map[string]any{"products": len(ids), "skus": changed, "bps": op.Bps, "keepOld": op.KeepOld})
		default:
			return fmt.Errorf("store: unknown bulk action %q", op.Action)
		}
		if err != nil {
			return err
		}
		done, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range done {
			if err := Emit(ctx, tx, tenant, TopicProduct, id, nil); err != nil {
				return err
			}
		}
		changed = len(done)
		return Audit(ctx, tx, tenant, actor, "product.bulk."+op.Action, fmt.Sprint(len(ids), " productos"), map[string]any{
			"changed": changed, "status": op.Status, "categoryId": op.CategoryID, "brandId": op.BrandID})
	})
	return changed, err
}

// PublishDue activates drafts whose scheduled time has arrived, for every tenant.
func (d *DB) PublishDue(ctx context.Context) (int, error) {
	rows, err := d.System.Query(ctx, `SELECT tenant_id, id FROM products WHERE status='draft' AND publish_at IS NOT NULL AND publish_at <= now() LIMIT 500`)
	if err != nil {
		return 0, err
	}
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]string, error) {
		var k [2]string
		return k, r.Scan(&k[0], &k[1])
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, k := range due {
		err := d.Tx(ctx, k[0], func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, `UPDATE products SET status='active', publish_at=NULL, version=version+1, updated_at=now()
				WHERE tenant_id=$1 AND id=$2 AND status='draft' AND publish_at <= now()`, k[0], k[1])
			if err != nil || ct.RowsAffected() == 0 {
				return err
			}
			n++
			if err := Emit(ctx, tx, k[0], TopicProduct, k[1], nil); err != nil {
				return err
			}
			return Audit(ctx, tx, k[0], "system:scheduler", "product.publish_scheduled", k[1], map[string]any{})
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Locations lists the stock locations a tenant uses.
func (d *DB) Locations(ctx context.Context, tenant string) ([]string, error) {
	var out []string
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// Skip from one location to the next on the (tenant_id, location_id)
		// index: the cost follows the number of locations, not stock rows.
		rows, err := tx.Query(ctx, `WITH RECURSIVE l(id) AS (
				(SELECT location_id FROM stock_levels WHERE tenant_id=$1 ORDER BY location_id LIMIT 1)
				UNION ALL
				SELECT (SELECT location_id FROM stock_levels WHERE tenant_id=$1 AND location_id > l.id ORDER BY location_id LIMIT 1)
				FROM l WHERE l.id IS NOT NULL)
			SELECT id FROM l WHERE id IS NOT NULL`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return out, err
}

// ---------- categories ----------

const categoryCols = `id, parent_id, handle, name, description, seo_title, seo_description, active, position`

func scanCategory(r pgx.CollectableRow) (catalog.Category, error) {
	var c catalog.Category
	return c, r.Scan(&c.ID, &c.ParentID, &c.Handle, &c.Name, &c.Description, &c.SEOTitle, &c.SEODescription, &c.Active, &c.Position)
}

// Categories lists the categories of a tenant; id != "" selects one (system pool).
func (d *DB) Categories(ctx context.Context, tenant, id string) ([]catalog.Category, error) {
	rows, err := d.System.Query(ctx, `SELECT `+categoryCols+` FROM categories WHERE tenant_id=$1 AND ($2 = '' OR id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCategory)
}

// checkParentTx makes sure parent exists and that id is not among its ancestors.
func checkParentTx(ctx context.Context, tx pgx.Tx, tenant, id, parent string) error {
	for i := 0; parent != ""; i++ {
		if parent == id || i > 32 {
			return ErrCycle
		}
		var next string
		err := tx.QueryRow(ctx, `SELECT parent_id FROM categories WHERE tenant_id=$1 AND id=$2`, tenant, parent).Scan(&next)
		if errors.Is(err, pgx.ErrNoRows) {
			return &RefError{"parentId", parent}
		}
		if err != nil {
			return err
		}
		parent = next
	}
	return nil
}

func saveCategoryTx(ctx context.Context, tx pgx.Tx, tenant, actor string, c catalog.Category, create bool) error {
	if err := checkParentTx(ctx, tx, tenant, c.ID, c.ParentID); err != nil {
		return err
	}
	var err error
	if create {
		_, err = tx.Exec(ctx, `INSERT INTO categories (tenant_id, id, parent_id, handle, name, description, seo_title, seo_description, active, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
				(SELECT COALESCE(max(position)+1, 0) FROM categories WHERE tenant_id=$1 AND parent_id=$3))`,
			tenant, c.ID, c.ParentID, c.Handle, c.Name, c.Description, c.SEOTitle, c.SEODescription, c.Active)
	} else {
		var ct pgconn.CommandTag
		ct, err = tx.Exec(ctx, `UPDATE categories SET parent_id=$3, handle=$4, name=$5, description=$6, seo_title=$7, seo_description=$8, active=$9, updated_at=now()
			WHERE tenant_id=$1 AND id=$2`, tenant, c.ID, c.ParentID, c.Handle, c.Name, c.Description, c.SEOTitle, c.SEODescription, c.Active)
		if err == nil && ct.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if err := Emit(ctx, tx, tenant, TopicCategory, c.ID, nil); err != nil {
		return err
	}
	return Audit(ctx, tx, tenant, actor, "category.save", c.ID, map[string]any{"name": c.Name, "handle": c.Handle, "parentId": c.ParentID, "active": c.Active})
}

// SaveCategory creates or updates a category (position is kept on update).
func (d *DB) SaveCategory(ctx context.Context, tenant, actor string, c catalog.Category, create bool) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error { return saveCategoryTx(ctx, tx, tenant, actor, c, create) })
}

// CategoryMove places a category under a parent at a position.
type CategoryMove struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Position int    `json:"position"`
}

// ReorderCategories applies moves atomically and rejects cycles.
func (d *DB) ReorderCategories(ctx context.Context, tenant, actor string, moves []CategoryMove) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, m := range moves {
			ct, err := tx.Exec(ctx, `UPDATE categories SET parent_id=$3, position=$4, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenant, m.ID, m.ParentID, m.Position)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return &RefError{"id", m.ID}
			}
		}
		for _, m := range moves {
			if err := checkParentTx(ctx, tx, tenant, m.ID, m.ParentID); err != nil {
				return err
			}
			if err := Emit(ctx, tx, tenant, TopicCategory, m.ID, nil); err != nil {
				return err
			}
		}
		return Audit(ctx, tx, tenant, actor, "category.reorder", fmt.Sprint(len(moves), " categorías"), moves)
	})
}

// DeleteCategory removes a leaf category and takes it out of its products.
func (d *DB) DeleteCategory(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var kids int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM categories WHERE tenant_id=$1 AND parent_id=$2`, tenant, id).Scan(&kids); err != nil {
			return err
		}
		if kids > 0 {
			return ErrHasChildren
		}
		ct, err := tx.Exec(ctx, `DELETE FROM categories WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		n, err := touchProductsTx(ctx, tx, tenant, `UPDATE products SET categories = array_remove(categories, $2), version=version+1, updated_at=now()
			WHERE tenant_id=$1 AND $2 = ANY(categories) RETURNING id`, id)
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicCategory, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "category.delete", id, map[string]any{"products": n})
	})
}

// touchProductsTx runs an UPDATE … RETURNING id over products and emits one
// product event per row, so every projection picks the change up.
func touchProductsTx(ctx context.Context, tx pgx.Tx, tenant, sql string, args ...any) (int, error) {
	rows, err := tx.Query(ctx, sql, append([]any{tenant}, args...)...)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := Emit(ctx, tx, tenant, TopicProduct, id, nil); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// ---------- brands ----------

func scanBrand(r pgx.CollectableRow) (catalog.Brand, error) {
	var b catalog.Brand
	return b, r.Scan(&b.ID, &b.Handle, &b.Name, &b.Description, &b.LogoMediaID, &b.Active)
}

// Brands lists the brands of a tenant; id != "" selects one (system pool).
func (d *DB) Brands(ctx context.Context, tenant, id string) ([]catalog.Brand, error) {
	rows, err := d.System.Query(ctx, `SELECT id, handle, name, description, logo_media_id, active FROM brands WHERE tenant_id=$1 AND ($2 = '' OR id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanBrand)
}

func saveBrandTx(ctx context.Context, tx pgx.Tx, tenant, actor string, b catalog.Brand, create bool) error {
	if b.LogoMediaID != "" {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media WHERE tenant_id=$1 AND id=$2)`, tenant, b.LogoMediaID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return &RefError{"logoMediaId", b.LogoMediaID}
		}
	}
	var err error
	if create {
		_, err = tx.Exec(ctx, `INSERT INTO brands (tenant_id, id, handle, name, description, logo_media_id, active) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, b.ID, b.Handle, b.Name, b.Description, b.LogoMediaID, b.Active)
	} else {
		var ct pgconn.CommandTag
		ct, err = tx.Exec(ctx, `UPDATE brands SET handle=$3, name=$4, description=$5, logo_media_id=$6, active=$7, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenant, b.ID, b.Handle, b.Name, b.Description, b.LogoMediaID, b.Active)
		if err == nil && ct.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	// Products carry the brand name for search and legacy readers; keep it in step.
	n, err := touchProductsTx(ctx, tx, tenant, `UPDATE products SET brand=$3, version=version+1, updated_at=now()
		WHERE tenant_id=$1 AND brand_id=$2 AND brand <> $3 RETURNING id`, b.ID, b.Name)
	if err != nil {
		return err
	}
	if err := Emit(ctx, tx, tenant, TopicBrand, b.ID, nil); err != nil {
		return err
	}
	return Audit(ctx, tx, tenant, actor, "brand.save", b.ID, map[string]any{"name": b.Name, "handle": b.Handle, "active": b.Active, "productsRenamed": n})
}

// SaveBrand creates or updates a brand and renames it on its products.
func (d *DB) SaveBrand(ctx context.Context, tenant, actor string, b catalog.Brand, create bool) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error { return saveBrandTx(ctx, tx, tenant, actor, b, create) })
}

// DeleteBrand removes a brand; its products keep the name as plain text.
func (d *DB) DeleteBrand(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM brands WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		n, err := touchProductsTx(ctx, tx, tenant, `UPDATE products SET brand_id='', version=version+1, updated_at=now()
			WHERE tenant_id=$1 AND brand_id=$2 RETURNING id`, id)
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicBrand, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "brand.delete", id, map[string]any{"products": n})
	})
}

// ---------- collections ----------

func scanCollection(r pgx.CollectableRow) (catalog.Collection, error) {
	var c catalog.Collection
	var rules []byte
	if err := r.Scan(&c.ID, &c.Handle, &c.Title, &c.Description, &c.Kind, &c.ProductIDs, &rules, &c.SEOTitle, &c.SEODescription, &c.Active); err != nil {
		return c, err
	}
	_ = json.Unmarshal(rules, &c.Rules)
	return c, nil
}

// Collections lists the collections of a tenant; id != "" selects one (system pool).
func (d *DB) Collections(ctx context.Context, tenant, id string) ([]catalog.Collection, error) {
	rows, err := d.System.Query(ctx, `SELECT id, handle, title, description, kind, product_ids, rules, seo_title, seo_description, active
		FROM collections WHERE tenant_id=$1 AND ($2 = '' OR id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCollection)
}

// SaveCollection creates or updates a collection.
func (d *DB) SaveCollection(ctx context.Context, tenant, actor string, c catalog.Collection, create bool) error {
	rules, _ := json.Marshal(c.Rules)
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO collections (tenant_id, id, handle, title, description, kind, product_ids, rules, seo_title, seo_description, active)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				tenant, c.ID, c.Handle, c.Title, c.Description, c.Kind, nonNil(c.ProductIDs), rules, c.SEOTitle, c.SEODescription, c.Active)
		} else {
			var ct pgconn.CommandTag
			ct, err = tx.Exec(ctx, `UPDATE collections SET handle=$3, title=$4, description=$5, kind=$6, product_ids=$7, rules=$8,
				seo_title=$9, seo_description=$10, active=$11, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenant, c.ID, c.Handle, c.Title, c.Description, c.Kind, nonNil(c.ProductIDs), rules, c.SEOTitle, c.SEODescription, c.Active)
			if err == nil && ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if isUnique(err) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicCollection, c.ID, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "collection.save", c.ID, map[string]any{"title": c.Title, "kind": c.Kind, "products": len(c.ProductIDs), "active": c.Active})
	})
}

// DeleteCollection removes a collection.
func (d *DB) DeleteCollection(ctx context.Context, tenant, actor, id string) error {
	return d.deleteSimple(ctx, tenant, actor, "collections", "id", id, TopicCollection, "collection.delete")
}

func (d *DB) deleteSimple(ctx context.Context, tenant, actor, table, col, id, topic, action string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE tenant_id=$1 AND `+col+`=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, topic, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, action, id, map[string]any{})
	})
}

// ---------- attributes ----------

func scanAttribute(r pgx.CollectableRow) (catalog.AttributeDef, error) {
	var a catalog.AttributeDef
	return a, r.Scan(&a.ID, &a.Name, &a.Kind, &a.Options, &a.Unit, &a.Filterable, &a.Position)
}

// Attributes lists attribute definitions; id != "" selects one (system pool).
func (d *DB) Attributes(ctx context.Context, tenant, id string) ([]catalog.AttributeDef, error) {
	rows, err := d.System.Query(ctx, `SELECT id, name, kind, options, unit, filterable, position FROM attribute_defs WHERE tenant_id=$1 AND ($2 = '' OR id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanAttribute)
}

// SaveAttribute creates or updates an attribute definition. The kind of an
// existing attribute cannot change (its stored values would stop making sense).
func (d *DB) SaveAttribute(ctx context.Context, tenant, actor string, a catalog.AttributeDef, create bool) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO attribute_defs (tenant_id, id, name, kind, options, unit, filterable, position)
				VALUES ($1,$2,$3,$4,$5,$6,$7,(SELECT COALESCE(max(position)+1, 0) FROM attribute_defs WHERE tenant_id=$1))`,
				tenant, a.ID, a.Name, a.Kind, nonNil(a.Options), a.Unit, a.Filterable)
		} else {
			var ct pgconn.CommandTag
			ct, err = tx.Exec(ctx, `UPDATE attribute_defs SET name=$3, options=$4, unit=$5, filterable=$6, position=$7, updated_at=now()
				WHERE tenant_id=$1 AND id=$2`, tenant, a.ID, a.Name, nonNil(a.Options), a.Unit, a.Filterable, a.Position)
			if err == nil && ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if isUnique(err) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicAttribute, a.ID, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "attribute.save", a.ID, map[string]any{"name": a.Name, "kind": a.Kind, "filterable": a.Filterable})
	})
}

// DeleteAttribute removes a definition and its values from every product.
func (d *DB) DeleteAttribute(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM attribute_defs WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		n, err := touchProductsTx(ctx, tx, tenant, `UPDATE products SET attributes = attributes - $2, version=version+1, updated_at=now()
			WHERE tenant_id=$1 AND attributes ? $2 RETURNING id`, id)
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicAttribute, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "attribute.delete", id, map[string]any{"products": n})
	})
}

// ---------- types and tags ----------

// Term is a managed product type or tag with how many products use it.
type Term struct {
	Name     string `json:"name"`
	Products int    `json:"products"`
	Managed  bool   `json:"managed"` // listed in the vocabulary (not only found on products)
}

// Terms lists the vocabulary of kind ("type" or "tag") merged with the values
// found on products.
func (d *DB) Terms(ctx context.Context, tenant, kind string) ([]Term, error) {
	var out []Term
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		used := `SELECT product_type AS name, count(*) AS n FROM products WHERE tenant_id=$1 AND product_type <> '' GROUP BY 1`
		if kind == "tag" {
			used = `SELECT t AS name, count(*) AS n FROM products, unnest(tags) AS t WHERE tenant_id=$1 GROUP BY 1`
		}
		rows, err := tx.Query(ctx, `WITH used AS (`+used+`)
			SELECT COALESCE(c.name, u.name), COALESCE(u.n, 0), c.name IS NOT NULL
			FROM (SELECT name FROM catalog_terms WHERE tenant_id=$1 AND kind=$2) c
			FULL JOIN used u ON u.name = c.name ORDER BY lower(COALESCE(c.name, u.name))`, tenant, kind)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Term, error) {
			var t Term
			return t, r.Scan(&t.Name, &t.Products, &t.Managed)
		})
		return err
	})
	return out, err
}

// AddTerm adds a name to the vocabulary (idempotent).
func (d *DB) AddTerm(ctx context.Context, tenant, actor, kind, name string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `INSERT INTO catalog_terms (tenant_id, kind, name) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, kind, name)
		if err != nil || ct.RowsAffected() == 0 {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "term.add", kind+":"+name, map[string]any{})
	})
}

// DeleteTerm removes a name from the vocabulary and from every product.
func (d *DB) DeleteTerm(ctx context.Context, tenant, actor, kind, name string) (int, error) {
	n := 0
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM catalog_terms WHERE tenant_id=$1 AND kind=$2 AND name=$3`, tenant, kind, name); err != nil {
			return err
		}
		q := `UPDATE products SET product_type='', version=version+1, updated_at=now() WHERE tenant_id=$1 AND product_type=$2 RETURNING id`
		if kind == "tag" {
			q = `UPDATE products SET tags = array_remove(tags, $2), version=version+1, updated_at=now() WHERE tenant_id=$1 AND $2 = ANY(tags) RETURNING id`
		}
		var err error
		if n, err = touchProductsTx(ctx, tx, tenant, q, name); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "term.delete", kind+":"+name, map[string]any{"products": n})
	})
	return n, err
}

// ---------- media ----------

func scanMedia(r pgx.CollectableRow) (catalog.Media, error) {
	var m catalog.Media
	var vs []byte
	var ed []byte
	if err := r.Scan(&m.ID, &m.Filename, &m.ContentType, &m.Bytes, &m.Width, &m.Height, &m.Alt, &vs, &m.CreatedAt, &ed); err != nil {
		return m, err
	}
	if len(ed) > 0 {
		m.Edits = ed
	}
	_ = json.Unmarshal(vs, &m.Variants)
	return m, nil
}

const mediaCols = `id, filename, content_type, bytes, width, height, alt, variants, created_at, edits`

// Media lists the media of a tenant; id != "" selects one (system pool).
func (d *DB) Media(ctx context.Context, tenant, id string) ([]catalog.Media, error) {
	rows, err := d.System.Query(ctx, `SELECT `+mediaCols+` FROM media WHERE tenant_id=$1 AND ($2 = '' OR id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanMedia)
}

// MediaBySHA finds an identical file already in the library.
func (d *DB) MediaBySHA(ctx context.Context, tenant string, sum []byte) (catalog.Media, bool, error) {
	var out []catalog.Media
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+mediaCols+` FROM media WHERE tenant_id=$1 AND sha256=$2 LIMIT 1`, tenant, sum)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanMedia)
		return err
	})
	if err != nil || len(out) == 0 {
		return catalog.Media{}, false, err
	}
	return out[0], true, nil
}

// InsertMedia records an uploaded image (its files are already in storage).
func (d *DB) InsertMedia(ctx context.Context, tenant, actor string, m catalog.Media, sum []byte) error {
	vs, _ := json.Marshal(m.Variants)
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO media (tenant_id, id, filename, content_type, bytes, width, height, alt, sha256, variants, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			tenant, m.ID, m.Filename, m.ContentType, m.Bytes, m.Width, m.Height, m.Alt, sum, vs, actor, m.CreatedAt); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicMedia, m.ID, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "media.upload", m.ID, map[string]any{"filename": m.Filename, "bytes": m.Bytes, "width": m.Width, "height": m.Height})
	})
}

// SetMediaEdit records new renditions made from the original with edits
// (nil edits: the original as it was).
func (d *DB) SetMediaEdit(ctx context.Context, tenant, actor, id string, edits []byte, w, h int, variants []catalog.MediaVariant) error {
	vs, _ := json.Marshal(variants)
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE media SET edits=$3, width=$4, height=$5, variants=$6 WHERE tenant_id=$1 AND id=$2`, tenant, id, edits, w, h, vs)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, TopicMedia, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "media.edit", id, map[string]any{"edits": json.RawMessage(rawOrNull(edits)), "width": w, "height": h})
	})
}

func rawOrNull(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

// SetMediaAlt changes the alt text of an image.
func (d *DB) SetMediaAlt(ctx context.Context, tenant, actor, id, alt string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE media SET alt=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, alt)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, TopicMedia, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "media.alt", id, map[string]any{"alt": alt})
	})
}

// MediaUse says where an image is used.
type MediaUse struct {
	Products []string `json:"products"`
	Brands   []string `json:"brands"`
}

func mediaUseTx(ctx context.Context, tx pgx.Tx, tenant, id string) (MediaUse, error) {
	var u MediaUse
	rows, err := tx.Query(ctx, `SELECT title FROM products WHERE tenant_id=$1 AND ($2 = ANY(media) OR variant_media::text LIKE '%"' || $2 || '"%') ORDER BY title LIMIT 20`, tenant, id)
	if err != nil {
		return u, err
	}
	if u.Products, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return u, err
	}
	rows, err = tx.Query(ctx, `SELECT name FROM brands WHERE tenant_id=$1 AND logo_media_id=$2 ORDER BY name LIMIT 20`, tenant, id)
	if err != nil {
		return u, err
	}
	u.Brands, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return u, err
}

// MediaUsage reports where an image is used.
func (d *DB) MediaUsage(ctx context.Context, tenant, id string) (MediaUse, error) {
	var u MediaUse
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) (err error) {
		u, err = mediaUseTx(ctx, tx, tenant, id)
		return err
	})
	return u, err
}

// DeleteMedia removes the index row of an image. Without force it fails with
// ErrInUse while a product or brand shows it; with force it is detached first.
func (d *DB) DeleteMedia(ctx context.Context, tenant, actor, id string, force bool) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		u, err := mediaUseTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if len(u.Products)+len(u.Brands) > 0 {
			if !force {
				return ErrInUse
			}
			if _, err := touchProductsTx(ctx, tx, tenant, `UPDATE products SET media = array_remove(media, $2),
					variant_media = COALESCE((SELECT jsonb_object_agg(k, v) FROM jsonb_each(variant_media) AS e(k, v) WHERE v <> to_jsonb($2::text)), '{}'::jsonb),
					version=version+1, updated_at=now()
				WHERE tenant_id=$1 AND ($2 = ANY(media) OR variant_media::text LIKE '%"' || $2 || '"%') RETURNING id`, id); err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `UPDATE brands SET logo_media_id='', updated_at=now() WHERE tenant_id=$1 AND logo_media_id=$2 RETURNING id`, tenant, id)
			if err != nil {
				return err
			}
			bs, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			for _, b := range bs {
				if err := Emit(ctx, tx, tenant, TopicBrand, b, nil); err != nil {
					return err
				}
			}
		}
		ct, err := tx.Exec(ctx, `DELETE FROM media WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, TopicMedia, id, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "media.delete", id, map[string]any{"force": force})
	})
}

// ListMedia pages the library, newest first. after is the id of the last item
// of the previous page; q filters by file name or alt text.
func (d *DB) ListMedia(ctx context.Context, tenant, q, after string, limit int) ([]catalog.Media, string, int, error) {
	var out []catalog.Media
	total := 0
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM media WHERE tenant_id=$1 AND (filename ILIKE $2 OR alt ILIKE $2)`, tenant, like).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+mediaCols+` FROM media m WHERE tenant_id=$1 AND (filename ILIKE $2 OR alt ILIKE $2)
			AND ($3 = '' OR (created_at, id) < (SELECT created_at, id FROM media WHERE tenant_id=$1 AND id=$3))
			ORDER BY created_at DESC, id DESC LIMIT $4`, tenant, like, after, limit+1)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanMedia)
		return err
	})
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, total, err
}

// ---------- CSV imports ----------

// ImportJob is one CSV import.
type ImportJob struct {
	ID        string          `json:"id"`
	Filename  string          `json:"filename"`
	Status    string          `json:"status"`
	Summary   json.RawMessage `json:"summary"`
	Errors    json.RawMessage `json:"errors"`
	Total     int             `json:"total"`
	Processed int             `json:"processed"`
	Created   int             `json:"created"`
	Updated   int             `json:"updated"`
	Failed    int             `json:"failed"`
	CreatedBy string          `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Source    []byte          `json:"-"`
}

const importCols = `id, filename, status, summary, errors, total, processed, created, updated, failed, created_by, created_at, updated_at`

func scanImport(r pgx.CollectableRow) (ImportJob, error) {
	var j ImportJob
	var s, e []byte
	err := r.Scan(&j.ID, &j.Filename, &j.Status, &s, &e, &j.Total, &j.Processed, &j.Created, &j.Updated, &j.Failed, &j.CreatedBy, &j.CreatedAt, &j.UpdatedAt)
	j.Summary, j.Errors = s, e
	return j, err
}

// CreateImport stores a validated CSV as a preview.
func (d *DB) CreateImport(ctx context.Context, tenant, actor string, j ImportJob) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO import_jobs (tenant_id, id, filename, status, source, summary, errors, total, created_by)
			VALUES ($1,$2,$3,'preview',$4,$5,$6,$7,$8)`, tenant, j.ID, j.Filename, j.Source, []byte(j.Summary), []byte(j.Errors), j.Total, actor); err != nil {
			return err
		}
		// Previews nobody applied are not kept forever.
		_, err := tx.Exec(ctx, `DELETE FROM import_jobs WHERE tenant_id=$1 AND status='preview' AND created_at < now() - interval '7 days'`, tenant)
		return err
	})
}

// GetImport reads a job; withSource also loads the CSV.
func (d *DB) GetImport(ctx context.Context, tenant, id string, withSource bool) (ImportJob, error) {
	var j ImportJob
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+importCols+` FROM import_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		js, err := pgx.CollectRows(rows, scanImport)
		if err != nil {
			return err
		}
		if len(js) == 0 {
			return ErrNotFound
		}
		j = js[0]
		if withSource {
			return tx.QueryRow(ctx, `SELECT source FROM import_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&j.Source)
		}
		return nil
	})
	return j, err
}

// ListImports returns the latest jobs.
func (d *DB) ListImports(ctx context.Context, tenant string, limit int) ([]ImportJob, error) {
	var out []ImportJob
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+importCols+` FROM import_jobs WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2`, tenant, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanImport)
		return err
	})
	return out, err
}

// ClaimImport moves a job to running. Only one caller wins; a job left
// running by a node that died can be claimed again after two minutes.
func (d *DB) ClaimImport(ctx context.Context, tenant, actor, id string, total int) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE import_jobs SET status='running', total=$3, processed=0, created=0, updated=0, failed=0, updated_at=now()
			WHERE tenant_id=$1 AND id=$2 AND (status='preview' OR (status='running' AND updated_at < now() - interval '2 minutes'))`, tenant, id, total)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrConflict
		}
		return Audit(ctx, tx, tenant, actor, "import.apply", id, map[string]any{"products": total})
	})
}

// ImportProgress updates the counters of a running job; status "" keeps it running.
func (d *DB) ImportProgress(ctx context.Context, tenant, id string, processed, created, updated, failed int, errs any, status string) error {
	b, _ := json.Marshal(errs)
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE import_jobs SET processed=$3, created=$4, updated=$5, failed=$6, errors=$7,
			status=CASE WHEN $8 = '' THEN status ELSE $8 END, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenant, id, processed, created, updated, failed, b, status)
		return err
	})
}

// ImportOptions says where prices and stock of an import go.
type ImportOptions struct {
	Channel  string
	Currency string
	Location string
}

// ApplyError is a problem found while applying one product of an import.
type ApplyError struct{ Message string }

func (e *ApplyError) Error() string { return e.Message }

// ImportOutcome is what happened to one product of an import batch.
type ImportOutcome struct {
	Created bool
	Err     error
}

// ApplyImportBatch applies several products in one transaction, each in
// its own savepoint: a product that fails is rolled back alone and the rest
// are kept, as with one transaction per product, but with one commit for
// the batch instead of one per product. If the batch itself cannot commit,
// every product of it reports that error.
func (d *DB) ApplyImportBatch(ctx context.Context, tenant, actor string, ips []catalog.ImportProduct, opt ImportOptions) []ImportOutcome {
	out := make([]ImportOutcome, len(ips))
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for i, ip := range ips {
			sp, err := tx.Begin(ctx) // savepoint
			if err != nil {
				return err
			}
			var created bool
			if err := applyImportTx(ctx, sp, tenant, actor, ip, opt, &created); err != nil {
				if rerr := sp.Rollback(ctx); rerr != nil {
					return rerr
				}
				out[i] = ImportOutcome{Err: err}
				continue
			}
			if err := sp.Commit(ctx); err != nil { // release savepoint
				return err
			}
			out[i] = ImportOutcome{Created: created}
		}
		return d.sandboxRoom(ctx, tx, tenant)
	})
	if err != nil {
		for i := range out {
			out[i] = ImportOutcome{Err: err}
		}
	}
	return out
}

func applyImportTx(ctx context.Context, tx pgx.Tx, tenant, actor string, ip catalog.ImportProduct, opt ImportOptions, created *bool) error {
	var id string
	e := tx.QueryRow(ctx, `SELECT id FROM products WHERE tenant_id=$1 AND handle=$2 FOR UPDATE`, tenant, ip.Handle).Scan(&id)
	var p catalog.Product
	switch {
	case errors.Is(e, pgx.ErrNoRows):
		*created = true
		p = catalog.Product{ID: NewID("prod_"), Handle: ip.Handle, Status: "draft"}
	case e != nil:
		return e
	default:
		if p, e = loadProduct(ctx, tx, tenant, id); e != nil {
			return e
		}
	}
	applyImportFields(&p, ip)
	if ip.Brand != "" {
		if p.BrandID, e = ensureBrandTx(ctx, tx, tenant, actor, ip.Brand); e != nil {
			return e
		}
	}
	if len(ip.Categories) > 0 {
		p.Categories = p.Categories[:0]
		for _, path := range ip.Categories {
			cid, e := ensureCategoryPathTx(ctx, tx, tenant, actor, path)
			if e != nil {
				return e
			}
			p.Categories = append(p.Categories, cid)
		}
	}

	w, e := importVariants(&p, ip, opt, *created, func(sku string) (int64, bool) {
		var cur int64
		err := tx.QueryRow(ctx, `SELECT amount FROM prices WHERE tenant_id=$1 AND channel=$2 AND sku=$3 AND min_qty=1`, tenant, opt.Channel, sku).Scan(&cur)
		return cur, err == nil
	})
	if e != nil {
		return e
	}
	w.Product = p
	_, e = saveProductTx(ctx, tx, tenant, actor, w, 0)
	if errors.Is(e, ErrConflict) {
		return &ApplyError{"Un SKU del producto ya pertenece a otro producto"}
	}
	return e
}

// ensureBrandTx finds a brand by name (ignoring case and accents) or creates it.
func ensureBrandTx(ctx context.Context, tx pgx.Tx, tenant, actor, name string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT id, handle, name, description, logo_media_id, active FROM brands WHERE tenant_id=$1`, tenant)
	if err != nil {
		return "", err
	}
	bs, err := pgx.CollectRows(rows, scanBrand)
	if err != nil {
		return "", err
	}
	handles := map[string]bool{}
	for _, b := range bs {
		if catalog.Fold(b.Name) == catalog.Fold(name) {
			return b.ID, nil
		}
		handles[b.Handle] = true
	}
	b := catalog.Brand{ID: NewID("br_"), Handle: uniqueHandle(catalog.Slug(name), "marca", handles), Name: name, Active: true}
	return b.ID, saveBrandTx(ctx, tx, tenant, actor, b, true)
}

func uniqueHandle(base, fallback string, taken map[string]bool) string {
	if base == "" {
		base = fallback
	}
	h := base
	for n := 2; taken[h]; n++ {
		h = fmt.Sprintf("%s-%d", base, n)
	}
	return h
}

// ensureCategoryPathTx finds or creates every level of "Ropa > Playeras" and
// returns the id of the last one.
func ensureCategoryPathTx(ctx context.Context, tx pgx.Tx, tenant, actor, path string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT `+categoryCols+` FROM categories WHERE tenant_id=$1`, tenant)
	if err != nil {
		return "", err
	}
	cs, err := pgx.CollectRows(rows, scanCategory)
	if err != nil {
		return "", err
	}
	handles := map[string]bool{}
	for _, c := range cs {
		handles[c.Handle] = true
	}
	parent := ""
	for _, name := range strings.Split(path, ">") {
		name = strings.TrimSpace(name)
		found := ""
		for _, c := range cs {
			if c.ParentID == parent && catalog.Fold(c.Name) == catalog.Fold(name) {
				found = c.ID
				break
			}
		}
		if found == "" {
			c := catalog.Category{ID: NewID("cat_"), ParentID: parent, Handle: uniqueHandle(catalog.Slug(name), "categoria", handles), Name: name, Active: true}
			if err := saveCategoryTx(ctx, tx, tenant, actor, c, true); err != nil {
				return "", err
			}
			handles[c.Handle] = true
			cs = append(cs, c)
			found = c.ID
		}
		parent = found
	}
	return parent, nil
}

// sendBatch runs a pipelined batch and returns the first failure, as wrap
// describes it (wrap gets the index of the failed statement).
func sendBatch(ctx context.Context, tx pgx.Tx, b *pgx.Batch, wrap func(i int, err error) error) error {
	if b.Len() == 0 {
		return nil
	}
	br := tx.SendBatch(ctx, b)
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return wrap(i, err)
		}
	}
	return br.Close()
}

// importVariants applies the variants, options, prices and stock of an
// imported product to p (new or loaded), exactly as the import does, and
// returns what to write. It reads nothing: curPrice gives the current price
// of a SKU when the file brings only a "before" price.
func importVariants(p *catalog.Product, ip catalog.ImportProduct, opt ImportOptions, created bool, curPrice func(sku string) (int64, bool)) (ProductWrite, error) {
	var w ProductWrite
	bySKU := map[string]int{}
	for i, v := range p.Variants {
		bySKU[v.SKU] = i
	}
	if len(ip.OptionNames) > 0 {
		same := len(p.Options) == len(ip.OptionNames)
		for i := 0; same && i < len(p.Options); i++ {
			same = p.Options[i].Name == ip.OptionNames[i]
		}
		if !same {
			// The file redefines the options: only its variants fit them.
			p.Options = make([]catalog.Option, len(ip.OptionNames))
			for i, n := range ip.OptionNames {
				p.Options[i].Name = n
			}
			inFile := map[string]bool{}
			for _, v := range ip.Variants {
				inFile[v.SKU] = true
			}
			kept := p.Variants[:0]
			for _, v := range p.Variants {
				if inFile[v.SKU] {
					kept = append(kept, v)
				}
			}
			p.Variants = kept
			bySKU = map[string]int{}
			for i, v := range p.Variants {
				bySKU[v.SKU] = i
			}
		}
	} else if len(p.Options) > 0 {
		for _, v := range ip.Variants {
			if _, ok := bySKU[v.SKU]; !ok {
				return w, &ApplyError{fmt.Sprintf("El SKU %s es nuevo y el producto usa opciones (%s); agrega las columnas option1_name/option1_value", v.SKU, p.Options[0].Name)}
			}
		}
	}
	w = ProductWrite{Channel: opt.Channel, Prices: map[string]PriceInput{}, Stock: map[string]map[string]int64{}, Create: created}
	for _, iv := range ip.Variants {
		i, ok := bySKU[iv.SKU]
		if !ok {
			p.Variants = append(p.Variants, catalog.Variant{SKU: iv.SKU})
			i = len(p.Variants) - 1
			bySKU[iv.SKU] = i
		}
		v := &p.Variants[i]
		if iv.Barcode != "" {
			v.Barcode = iv.Barcode
		}
		if iv.WeightGrams != nil {
			v.WeightGrams = *iv.WeightGrams
		}
		if len(iv.Options) > 0 {
			v.Options = iv.Options
			for oi := range p.Options {
				val := iv.Options[p.Options[oi].Name]
				found := false
				for _, x := range p.Options[oi].Values {
					found = found || x == val
				}
				if !found {
					p.Options[oi].Values = append(p.Options[oi].Values, val)
				}
			}
		}
		if iv.Price != nil {
			w.Prices[iv.SKU] = PriceInput{Amount: *iv.Price, CompareAt: iv.CompareAt, Currency: opt.Currency}
		} else if iv.CompareAt != nil {
			cur, ok := curPrice(iv.SKU)
			if !ok || *iv.CompareAt < cur {
				return w, &ApplyError{fmt.Sprintf("compare_at_price del SKU %s necesita un price igual o menor", iv.SKU)}
			}
			w.Prices[iv.SKU] = PriceInput{Amount: cur, CompareAt: iv.CompareAt, Currency: opt.Currency}
		}
		if iv.Stock != nil {
			w.Stock[iv.SKU] = map[string]int64{opt.Location: *iv.Stock}
		}
	}
	for i := range p.Variants {
		p.Variants[i].Title = catalog.VariantTitle(p.Options, p.Variants[i].Options)
	}
	// Option values no variant uses any more are dropped.
	for oi := range p.Options {
		used := p.Options[oi].Values[:0]
		for _, val := range p.Options[oi].Values {
			for _, v := range p.Variants {
				if v.Options[p.Options[oi].Name] == val {
					used = append(used, val)
					break
				}
			}
		}
		p.Options[oi].Values = used
	}
	if e := catalog.ValidateVariantOptions(p.Options, p.Variants); e != nil {
		return w, &ApplyError{e.Error()}
	}
	if len(p.Variants) > catalog.MaxVariants {
		return w, &ApplyError{fmt.Sprintf("El producto quedaría con más de %d variantes", catalog.MaxVariants)}
	}
	return w, nil
}

// applyImportFields copies the product fields a CSV row brings onto p; an
// empty cell keeps what p had.
func applyImportFields(p *catalog.Product, ip catalog.ImportProduct) {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&p.Title, ip.Title)
	set(&p.Description, ip.Description)
	set(&p.Status, ip.Status)
	set(&p.Type, ip.Type)
	set(&p.SEOTitle, ip.SEOTitle)
	set(&p.SEODescription, ip.SEODescription)
	if p.Status != "draft" {
		p.PublishAt = nil
	}
	if ip.Tags != nil {
		p.Tags = ip.Tags
	}
	if len(ip.Attributes) > 0 {
		if p.Attributes == nil {
			p.Attributes = map[string]string{}
		}
		for k, v := range ip.Attributes {
			p.Attributes[k] = v
		}
	}
}
