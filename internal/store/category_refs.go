package store

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

// A product's categories are ids of the store's categories, always. These
// helpers keep it so: the panel and API refuse an unknown id; the importers
// and seeds, which carry category names, create them.

// missingCategories returns the ids that are not categories of the store.
func missingCategories(ctx context.Context, tx pgx.Tx, tenant string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT x FROM unnest($2::text[]) x WHERE NOT EXISTS (SELECT 1 FROM categories c WHERE c.tenant_id = $1 AND c.id = x)`, tenant, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// checkCategoriesTx refuses a category that is not the store's.
func checkCategoriesTx(ctx context.Context, tx pgx.Tx, tenant string, ids []string) error {
	missing, err := missingCategories(ctx, tx, tenant, ids)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return &RefError{"categories", missing[0]}
	}
	return nil
}

// resolveCategoriesTx turns category ids or names (an importer's, "Ropa >
// Playeras") into ids, creating the categories that do not exist.
func resolveCategoriesTx(ctx context.Context, tx pgx.Tx, tenant, actor string, refs []string) ([]string, error) {
	missing, err := missingCategories(ctx, tx, tenant, refs)
	if err != nil || len(missing) == 0 {
		return refs, err
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if !slices.Contains(missing, r) {
			out = append(out, r)
			continue
		}
		id, err := ensureCategoryPathTx(ctx, tx, tenant, actor, r)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// RepairCategoryRefs fixes products that point to categories that do not
// exist (written before the check, for example by an import): each one
// becomes a real category with that name, and the products point to it.
// Idempotent; returns how many products changed.
func (d *DB) RepairCategoryRefs(ctx context.Context) (int, error) {
	rows, err := d.System.Query(ctx, `SELECT DISTINCT x.tenant_id, x.ref FROM (SELECT p.tenant_id, unnest(p.categories) ref FROM products p) x
		WHERE NOT EXISTS (SELECT 1 FROM categories c WHERE c.tenant_id = x.tenant_id AND c.id = x.ref) ORDER BY 1, 2`)
	if err != nil {
		return 0, err
	}
	type ref struct{ tenant, name string }
	refs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ref, error) {
		var x ref
		return x, r.Scan(&x.tenant, &x.name)
	})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, x := range refs {
		err := d.Tx(ctx, x.tenant, func(tx pgx.Tx) error {
			id, err := ensureCategoryPathTx(ctx, tx, x.tenant, "system:repair", x.name)
			if err != nil {
				return err
			}
			n, err := touchProductsTx(ctx, tx, x.tenant, `UPDATE products SET categories = array_replace(categories, $2, $3), version=version+1, updated_at=now()
				WHERE tenant_id=$1 AND $2 = ANY(categories) RETURNING id`, x.name, id)
			total += n
			if err != nil {
				return err
			}
			return Audit(ctx, tx, x.tenant, "system:repair", "category.repair", id, map[string]any{"from": x.name, "products": n})
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
