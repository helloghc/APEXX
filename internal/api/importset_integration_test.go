package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/store"
)

// The set path writes new products exactly as the one-by-one path does.
// Store A takes the set path, store B the one-by-one path, from the same
// products; every row that describes them must match.
func TestImportSetPathEqualsOneByOne(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	i64 := func(v int64) *int64 { return &v }
	ips := []catalog.ImportProduct{
		{Row: 2, Handle: "set-playera", Title: "Playera Set", Description: "Algodón", Status: "active", Brand: "Marca Set", Categories: []string{"Ropa > Playeras"},
			Type: "Playera", Tags: []string{"verano", "básicos"}, SEOTitle: "Playera SEO", SEODescription: "desc", OptionNames: []string{"Color", "Talla"},
			Attributes: map[string]string{"material": "algodón"},
			Variants: []catalog.ImportVariant{
				{Row: 2, SKU: "SET-N-M", Options: map[string]string{"Color": "Negro", "Talla": "M"}, Barcode: "7501", WeightGrams: i64(180), Price: i64(29900), CompareAt: i64(34900), Stock: i64(7)},
				{Row: 3, SKU: "SET-N-G", Options: map[string]string{"Color": "Negro", "Talla": "G"}, WeightGrams: i64(190), Price: i64(29900), Stock: i64(0)},
			}},
		{Row: 4, Handle: "set-borrador", Title: "Borrador Set", Status: "draft", Variants: []catalog.ImportVariant{{Row: 4, SKU: "SET-B", Price: i64(1000)}}},
		{Row: 5, Handle: "set-sin-precio", Title: "Sin precio", Variants: []catalog.ImportVariant{{Row: 5, SKU: "SET-SP", Stock: i64(3)}}},
		// Fails: a "before" price without a price for a new SKU.
		{Row: 6, Handle: "set-malo", Title: "Malo", Variants: []catalog.ImportVariant{{Row: 6, SKU: "SET-MALO", CompareAt: i64(500)}}},
		// Fails: SET-DUENO already belongs to another product in both stores.
		{Row: 7, Handle: "set-roba", Title: "Roba", Variants: []catalog.ImportVariant{{Row: 7, SKU: "SET-DUENO", Price: i64(100)}}},
		// Exists in both stores: goes to the one-by-one path from the set call.
		{Row: 8, Handle: "p1", Title: "Producto uno renombrado"},
	}
	for _, key := range []string{e.adminA, e.adminB} {
		e.must(201, "POST", "/v1/products", key, map[string]any{"handle": "dueno-sku", "title": "Dueño", "status": "active",
			"variants": []map[string]any{{"sku": "SET-DUENO", "title": "Única"}}})
	}
	opt := store.ImportOptions{Channel: "mx", Currency: "MXN", Location: "w1"}
	outA, existing, err := e.db.ApplyNewProducts(ctx, "store-a", "test", ips, opt)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(existing) != "[5]" {
		t.Fatalf("existing = %v", existing)
	}
	outB := e.db.ApplyImportBatch(ctx, "store-b", "test", ips[:5], opt)
	for i := 0; i < 5; i++ {
		if (outA[i].Err == nil) != (outB[i].Err == nil) || outA[i].Created != outB[i].Created {
			t.Fatalf("product %d: set %+v, one by one %+v", i, outA[i], outB[i])
		}
		if outA[i].Err != nil && outA[i].Err.Error() != outB[i].Err.Error() {
			t.Fatalf("product %d error: set %q, one by one %q", i, outA[i].Err, outB[i].Err)
		}
	}
	// Row by row, with store-specific ids replaced by names.
	dump := func(tenant string) string {
		var b strings.Builder
		q := func(sql string) {
			rows, err := e.db.System.Query(ctx, sql, tenant)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				v, err := rows.Values()
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintln(&b, v...)
			}
		}
		q(`SELECT p.handle, p.title, p.description, p.brand, b.name, p.attributes, p.status, p.product_type, p.tags, p.options, p.seo_title, p.seo_description,
				p.publish_at, p.media, p.variant_media, p.related, p.version,
				ARRAY(SELECT c.name FROM categories c WHERE c.tenant_id=p.tenant_id AND c.id = ANY(p.categories) ORDER BY c.name)
			FROM products p LEFT JOIN brands b ON b.tenant_id=p.tenant_id AND b.id=p.brand_id
			WHERE p.tenant_id=$1 AND p.handle LIKE 'set-%' ORDER BY p.handle`)
		q(`SELECT v.sku, p.handle, v.title, v.options, v.barcode, v.weight_grams, v.position FROM variants v JOIN products p ON p.tenant_id=v.tenant_id AND p.id=v.product_id
			WHERE v.tenant_id=$1 AND v.sku LIKE 'SET-%' AND p.handle LIKE 'set-%' ORDER BY v.sku`)
		q(`SELECT channel, sku, min_qty, amount, currency, compare_at FROM prices WHERE tenant_id=$1 AND sku LIKE 'SET-%' ORDER BY sku`)
		q(`SELECT sku, location_id, on_hand, reserved FROM stock_levels WHERE tenant_id=$1 AND sku LIKE 'SET-%' ORDER BY sku`)
		q(`SELECT topic, count(*) FROM outbox o WHERE tenant_id=$1 AND (key LIKE '%SET-%' OR key IN (SELECT id FROM products WHERE tenant_id=o.tenant_id AND handle LIKE 'set-%')) GROUP BY topic ORDER BY topic`)
		q(`SELECT a.action, a.data - 'version' FROM audit_log a JOIN products p ON p.tenant_id=a.tenant_id AND p.id=a.target WHERE a.tenant_id=$1 AND p.handle LIKE 'set-%' ORDER BY p.handle`)
		return b.String()
	}
	a, b := dump("store-a"), dump("store-b")
	if a != b {
		t.Fatalf("the set path wrote something else:\nset:\n%s\none by one:\n%s", a, b)
	}
	if !strings.Contains(a, "SET-N-M") || !strings.Contains(a, "Marca Set") {
		t.Fatalf("nothing compared:\n%s", a)
	}
	for _, tenant := range []string{"store-a", "store-b"} {
		if broken, err := e.db.VerifyAudit(ctx, tenant); err != nil || broken != 0 {
			t.Fatalf("%s audit chain broken at %d (%v)", tenant, broken, err)
		}
	}
}
