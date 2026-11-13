package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// What travels with a product, by table: its rows of a store, found by the
// product ids ({P}), their SKUs ({K}) and their media ids ({M}). Used to
// bring products into a sandbox, to send them to another sandbox, and to
// leave them out of a light copy.
var productRows = map[string]string{
	"products":          "x.id = ANY({P})",
	"variants":          "x.product_id = ANY({P})",
	"product_logistics": "x.product_id = ANY({P})",
	"seller_products":   "x.product_id = ANY({P})",
	"reviews":           "x.product_id = ANY({P})",
	"prices":            "x.sku = ANY({K})",
	"price_list_items":  "x.sku = ANY({K})",
	"stock_levels":      "x.sku = ANY({K})",
	"stock_thresholds":  "x.sku = ANY({K})",
	"preorders":         "x.sku = ANY({K})",
	"digital_files":     "x.sku = ANY({K})",
	"bundle_components": "x.bundle_sku = ANY({K})",
	"media":             "x.id = ANY({M})",
	"ai_chunks":         "x.source_kind = 'product' AND x.source_id = ANY({P})",
	"kg_edges": "(x.src_kind = 'product' AND x.src_id = ANY({P}) OR x.dst_kind = 'product' AND x.dst_id = ANY({P}))" +
		" AND (x.src_kind <> 'product' OR x.src_id = ANY({P})) AND (x.dst_kind <> 'product' OR x.dst_id = ANY({P}))",
	"custom_values": "x.resource = 'product' AND x.owner_id = ANY({P})",
}

// lightSkip: what a light sandbox does not copy (alias x). Media stays:
// the design uses it too, and its files are never duplicated anyway.
var lightSkip = map[string]string{
	"products": "true", "variants": "true", "product_logistics": "true", "seller_products": "true", "reviews": "true",
	"prices": "true", "price_list_items": "true", "stock_levels": "true", "stock_thresholds": "true", "preorders": "true",
	"digital_files": "true", "bundle_components": "true", "product_coviews": "true",
	"ai_chunks":     "x.source_kind = 'product'",
	"kg_edges":      "x.src_kind = 'product' OR x.dst_kind = 'product'",
	"custom_values": "x.resource = 'product'",
}

// rowsOf fills a productRows filter with placeholders. Every filter names
// the three lists (a statement must use every parameter it is given).
func rowsOf(table, p, k, m string) string {
	f := "(" + productRows[table] + ") AND cardinality({P}::text[]) >= 0 AND cardinality({K}::text[]) >= 0 AND cardinality({M}::text[]) >= 0"
	return strings.NewReplacer("{P}", p, "{K}", k, "{M}", m).Replace(f)
}

// ErrProductsNotFound lists ids that are not products of the source store.
type ErrProductsNotFound struct{ IDs []string }

func (e *ErrProductsNotFound) Error() string {
	return "store: products not found: " + strings.Join(e.IDs, ", ")
}

// ErrSameHandle means a product with the same handle (another id) is
// already where the products go.
var ErrSameHandle = errors.New("store: a product with the same handle is already there")

// productTables: the tables of productRows in parent-first order.
func (d *DB) productTables(ctx context.Context) ([]tableInfo, error) {
	tables, err := d.storeTables(ctx)
	if err != nil {
		return nil, err
	}
	var out []tableInfo
	for _, t := range tables {
		if _, ok := productRows[t.name]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// productKeys returns the SKUs and media ids of products in some stores.
func productKeys(ctx context.Context, tx pgx.Tx, tenants, products []string) (skus, media []string, err error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT sku FROM variants WHERE tenant_id = ANY($1) AND product_id = ANY($2)`, tenants, products)
	if err != nil {
		return nil, nil, err
	}
	if skus, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `SELECT DISTINCT m FROM (
			SELECT unnest(media) AS m FROM products WHERE tenant_id = ANY($1) AND id = ANY($2)
			UNION ALL SELECT v.value FROM products p, jsonb_each_text(p.variant_media) v WHERE p.tenant_id = ANY($1) AND p.id = ANY($2)) q WHERE m <> ''`, tenants, products)
	if err != nil {
		return nil, nil, err
	}
	media, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if skus == nil {
		skus = []string{}
	}
	if media == nil {
		media = []string{}
	}
	return skus, media, err
}

// MoveResult says what bringing or sending products did.
type MoveResult struct {
	Products int      `json:"products"` // products written
	Rows     int64    `json:"rows"`     // rows written in every table
	Skipped  []string `json:"skipped"`  // already there (bring without reset)
	MediaIDs []string `json:"-"`        // media whose files must be copied (send)
}

// BringProducts copies products of production, with everything that goes
// with them, into a sandbox. A product already in the sandbox is left as it
// is unless reset, which puts it back as production has it. The base is
// production's rows, so what the team changes later shows as a change.
func (d *DB) BringProducts(ctx context.Context, sandboxID string, products []string, reset bool) (MoveResult, error) {
	sb, err := d.SandboxByID(ctx, sandboxID)
	if err != nil {
		return MoveResult{}, err
	}
	return d.moveProducts(ctx, sb.ParentID, sb, products, reset, true)
}

// SendProducts copies products of one sandbox, as they are there, into
// another sandbox of the same store, replacing its version of them. The
// destination gets the source's base (production as it was), so publishing
// from it shows the same changes.
func (d *DB) SendProducts(ctx context.Context, fromID, toID string, products []string) (MoveResult, error) {
	from, err := d.SandboxByID(ctx, fromID)
	if err != nil {
		return MoveResult{}, err
	}
	to, err := d.SandboxByID(ctx, toID)
	if err != nil {
		return MoveResult{}, err
	}
	if from.ParentID != to.ParentID || from.ID == to.ID {
		return MoveResult{}, ErrNotFound
	}
	return d.moveProducts(ctx, from.ID, to, products, true, false)
}

// moveProducts writes products of src into the sandbox dst. replace deletes
// dst's version first; fromProduction says whether src is production (the
// base then is src's rows) or a sandbox (the base is copied from it).
func (d *DB) moveProducts(ctx context.Context, src string, dst Sandbox, products []string, replace, fromProduction bool) (MoveResult, error) {
	var res MoveResult
	products = slices.Compact(slices.Sorted(slices.Values(products)))
	if len(products) == 0 {
		return res, errors.New("store: no products")
	}
	tables, err := d.productTables(ctx)
	if err != nil {
		return res, err
	}
	pub := publishable()
	parent := dst.ParentID
	err = pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		// Every product must exist where it comes from.
		rows, err := tx.Query(ctx, `SELECT id FROM products WHERE tenant_id = $1 AND id = ANY($2)`, src, products)
		if err != nil {
			return err
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if len(found) != len(products) {
			var missing []string
			for _, p := range products {
				if !slices.Contains(found, p) {
					missing = append(missing, p)
				}
			}
			return &ErrProductsNotFound{IDs: missing}
		}
		if !replace {
			// Without reset, products already in the sandbox stay as they are.
			rows, err := tx.Query(ctx, `SELECT id FROM products WHERE tenant_id = $1 AND id = ANY($2)`, dst.ID, products)
			if err != nil {
				return err
			}
			there, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			res.Skipped = there
			products = slices.DeleteFunc(slices.Clone(products), func(p string) bool { return slices.Contains(there, p) })
			if len(products) == 0 {
				return nil
			}
		}
		// The keys of these products wherever they are (source, sandbox,
		// production): a variant the source deleted must leave too.
		skus, media, err := productKeys(ctx, tx, []string{src, dst.ID, parent}, products)
		if err != nil {
			return err
		}
		_, srcMedia, err := productKeys(ctx, tx, []string{src}, products)
		if err != nil {
			return err
		}
		res.MediaIDs = srcMedia
		// where: rows of these products in the store at placeholder tenant,
		// with products, SKUs and media at $3, $4 and $5.
		where := func(t tableInfo, tenant string) string {
			return "x.tenant_id = " + tenant + " AND " + rowsOf(t.name, "$3", "$4", "$5")
		}
		if replace {
			// The destination's version leaves: its base first, then its
			// rows, children before parents. Media is shared: it stays.
			for i := len(tables) - 1; i >= 0; i-- {
				t := tables[i]
				if t.name == "media" {
					continue
				}
				if pub[t.name] {
					if _, err := tx.Exec(ctx, `DELETE FROM sandbox_base b WHERE b.sandbox_id = $1 AND b.tbl = '`+t.name+`' AND b.pk IN (
						SELECT `+pkExpr("x", t)+` FROM `+quoteIdent(t.name)+` x WHERE `+where(t, "$1")+`
						UNION SELECT `+pkExpr("x", t)+` FROM `+quoteIdent(t.name)+` x WHERE `+where(t, "$2")+`)`,
						dst.ID, parent, products, skus, media); err != nil {
						return fmt.Errorf("move base %s: %w", t.name, err)
					}
				}
				if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(t.name)+` x WHERE x.tenant_id = $1 AND `+rowsOf(t.name, "$2", "$3", "$4"),
					dst.ID, products, skus, media); err != nil {
					return fmt.Errorf("move clear %s: %w", t.name, err)
				}
			}
		}
		for _, t := range tables {
			rule := sandboxTables[t.name]
			if t.name == "stock_levels" && !fromProduction {
				// From a sandbox, stock goes as the team left it: what its
				// test orders took is put back, and nothing is reserved.
				rule = tableRule{mode: rule.mode, where: rule.where, set: map[string]string{"on_hand": "x.on_hand + " + consumedBy("x"), "reserved": "0"}}
			}
			q := copyWhereSQL(t, rule, rowsOf(t.name, "$3", "$4", "$5"), "$1", "$2") + " ON CONFLICT DO NOTHING"
			tag, err := tx.Exec(ctx, q, dst.ID, src, products, skus, media)
			if err != nil {
				if isUnique(err) {
					return ErrSameHandle
				}
				return fmt.Errorf("move %s: %w", t.name, err)
			}
			res.Rows += tag.RowsAffected()
			if t.name == "products" {
				res.Products = int(tag.RowsAffected())
			}
			if !pub[t.name] {
				continue
			}
			// The base: production's rows as they are now (bring), or the
			// source sandbox's base for these rows (send).
			if fromProduction {
				_, err = tx.Exec(ctx, `INSERT INTO sandbox_base (sandbox_id, tbl, pk, hash) SELECT $1, '`+t.name+`', `+pkExpr("x", t)+`, `+rowHash("x", t.name)+`
					FROM `+quoteIdent(t.name)+` x WHERE `+where(t, "$2")+` ON CONFLICT DO NOTHING`, dst.ID, src, products, skus, media)
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO sandbox_base (sandbox_id, tbl, pk, hash) SELECT $1, b.tbl, b.pk, b.hash FROM sandbox_base b
					WHERE b.sandbox_id = $2 AND b.tbl = '`+t.name+`' AND b.pk IN (
						SELECT `+pkExpr("x", t)+` FROM `+quoteIdent(t.name)+` x WHERE `+where(t, "$2")+`
						UNION SELECT `+pkExpr("x", t)+` FROM `+quoteIdent(t.name)+` x WHERE `+where(t, "$6")+`)
					ON CONFLICT (sandbox_id, tbl, pk) DO UPDATE SET hash = EXCLUDED.hash`, dst.ID, src, products, skus, media, parent)
			}
			if err != nil {
				return fmt.Errorf("move base %s: %w", t.name, err)
			}
		}
		if err := d.sandboxRoom(ctx, tx, dst.ID); err != nil {
			return err
		}
		return Emit(ctx, tx, dst.ID, TopicTenantReload, "", nil)
	})
	if res.Skipped == nil {
		res.Skipped = []string{}
	}
	return res, err
}

// CatalogItem is a product in a list to pick from.
type CatalogItem struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Handle string   `json:"handle"`
	SKUs   []string `json:"skus"`
	There  bool     `json:"inSandbox"` // already in the sandbox
}

// PickProducts lists products of a store to choose from (title, handle or
// SKU), saying which are already in the sandbox.
func (d *DB) PickProducts(ctx context.Context, tenant, sandboxID, q string, limit int) ([]CatalogItem, int, error) {
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(q)) + "%"
	var total int
	if err := d.System.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id = $1`, tenant).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := d.System.Query(ctx, `SELECT p.id, p.title, p.handle,
			COALESCE((SELECT array_agg(v.sku ORDER BY v.position, v.sku) FROM variants v WHERE v.tenant_id = p.tenant_id AND v.product_id = p.id), '{}'),
			EXISTS (SELECT 1 FROM products s WHERE s.tenant_id = $2 AND s.id = p.id)
		FROM products p WHERE p.tenant_id = $1 AND ($3 = '%%' OR p.title ILIKE $3 OR p.handle ILIKE $3 OR p.id ILIKE $3
			OR EXISTS (SELECT 1 FROM variants v WHERE v.tenant_id = p.tenant_id AND v.product_id = p.id AND v.sku ILIKE $3))
		ORDER BY p.title, p.id LIMIT $4`, tenant, sandboxID, pattern, limit)
	if err != nil {
		return nil, 0, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CatalogItem, error) {
		var c CatalogItem
		return c, r.Scan(&c.ID, &c.Title, &c.Handle, &c.SKUs, &c.There)
	})
	if items == nil {
		items = []CatalogItem{}
	}
	return items, total, err
}

// SandboxProductIDs lists the products a sandbox has (refreshing a light
// sandbox brings the same ones back).
func (d *DB) SandboxProductIDs(ctx context.Context, sandboxID string) ([]string, error) {
	rows, err := d.System.Query(ctx, `SELECT id FROM products WHERE tenant_id = $1 ORDER BY id`, sandboxID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
