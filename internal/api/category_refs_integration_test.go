package api

import (
	"context"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
)

// TestProductCategoryRefs: a product's categories are always categories of
// its store. The API refuses unknown ones; importers create them by name;
// older data is repaired.
func TestProductCategoryRefs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	st, out := e.do("POST", "/v1/products", e.adminA, m{"handle": "cat-x", "title": "X", "categories": []string{"no-existe"}, "variants": []m{{"sku": "CATX", "title": "Única"}}})
	if st != 400 {
		t.Fatalf("product with an unknown category = %d %v", st, out)
	}
	if errs, _ := out["errors"].([]any); len(errs) == 0 || errs[0].(map[string]any)["field"] != "categories" {
		t.Fatalf("error does not name the field: %v", out)
	}
	// An importer's names become real categories.
	if _, err := e.db.UpsertProduct(ctx, "store-a", "test", catalog.Product{ID: "IMP1", Handle: "imp-1", Title: "Importado", Categories: []string{"Ropa > Playeras"},
		Variants: []catalog.Variant{{SKU: "IMP1-1", Title: "Única"}}}, 0); err != nil {
		t.Fatal(err)
	}
	var cats []string
	_ = e.db.System.QueryRow(ctx, `SELECT categories FROM products WHERE tenant_id='store-a' AND id='IMP1'`).Scan(&cats)
	var name string
	if len(cats) == 1 {
		_ = e.db.System.QueryRow(ctx, `SELECT name FROM categories WHERE tenant_id='store-a' AND id=$1`, cats[0]).Scan(&name)
	}
	if name != "Playeras" {
		t.Fatalf("imported categories = %v (%q)", cats, name)
	}
	// Data written before the check is repaired, once.
	if _, err := e.db.System.Exec(ctx, `UPDATE products SET categories = categories || '{Deportes}' WHERE tenant_id='store-a' AND id='P1'`); err != nil {
		t.Fatal(err)
	}
	n, err := e.db.RepairCategoryRefs(ctx)
	if err != nil || n != 1 {
		t.Fatalf("repair = %d %v", n, err)
	}
	var dangling int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT p.tenant_id, unnest(p.categories) c FROM products p) x WHERE NOT EXISTS (SELECT 1 FROM categories k WHERE k.tenant_id=x.tenant_id AND k.id=x.c)`).Scan(&dangling)
	if dangling != 0 {
		t.Fatalf("%d dangling category references after repair", dangling)
	}
	if n, _ := e.db.RepairCategoryRefs(ctx); n != 0 {
		t.Fatalf("repair is not idempotent: %d", n)
	}
}
