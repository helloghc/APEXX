package api

import (
	"fmt"
	"testing"

	"github.com/helloghc/apexx/internal/store"
)

// Deleting many products at once: same rules as deleting one (catalog
// admin), ids that are gone are skipped, the store stops showing them and
// the audit chain stays whole.
func TestBulkDeleteProducts(t *testing.T) {
	e := setup(t)
	var ids []string
	for i := 0; i < 30; i++ {
		sku := fmt.Sprintf("BOR-%d", i)
		p := e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": fmt.Sprintf("borrar-%d", i), "title": fmt.Sprintf("Borrable %d", i), "status": "active",
			"variants": []map[string]any{{"sku": sku, "title": "Única"}},
			"prices":   map[string]any{sku: map[string]any{"amount": 10000, "currency": "MXN"}},
			"stock":    map[string]any{sku: map[string]any{"w1": 5}}})
		ids = append(ids, p["id"].(string))
	}
	// A manual collection mixing kept and deleted products keeps the rest in order.
	col := e.must(201, "POST", "/v1/catalog/collections", e.adminA, map[string]any{"title": "Mezcla", "kind": "manual", "productIds": []string{"P2", ids[0], "P1", ids[1]}})["collection"].(map[string]any)["id"].(string)
	var outboxBefore int64
	if err := e.db.System.QueryRow(t.Context(), `SELECT COALESCE(max(id),0) FROM outbox`).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	editor := e.must(201, "POST", "/v1/team/api-keys", e.adminA, map[string]any{"name": "editor", "role": "editor"})["key"].(string)
	if st, _ := e.do("POST", "/v1/products/bulk", editor, map[string]any{"ids": ids, "action": "delete"}); st != 403 {
		t.Fatalf("editor deleted in bulk: %d", st)
	}
	r := e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": append(ids, "no-existe"), "action": "delete"})
	if r["changed"].(float64) != 30 || r["selected"].(float64) != 31 {
		t.Fatalf("bulk delete = %v", r)
	}
	var n int
	if err := e.db.System.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM products WHERE tenant_id='store-a' AND handle LIKE 'borrar-%')
		+ (SELECT count(*) FROM variants WHERE tenant_id='store-a' AND sku LIKE 'BOR-%')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left: %d (%v)", n, err)
	}
	var left int
	if err := e.db.System.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM prices WHERE tenant_id='store-a' AND sku LIKE 'BOR-%')
		+ (SELECT count(*) FROM stock_levels WHERE tenant_id='store-a' AND sku LIKE 'BOR-%')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("prices or stock left: %d (%v)", left, err)
	}
	var kept []string
	if err := e.db.System.QueryRow(t.Context(), `SELECT product_ids FROM collections WHERE tenant_id='store-a' AND id=$1`, col).Scan(&kept); err != nil || fmt.Sprint(kept) != "[P2 P1]" {
		t.Fatalf("collection = %v (%v)", kept, err)
	}
	// Events, as deleting one by one emits them: a price, a stock and a
	// product event per deleted product, and the collection.
	var prices, stocks, products, cols, audits int
	if err := e.db.System.QueryRow(t.Context(), `SELECT
		count(*) FILTER (WHERE topic=$2 AND key LIKE '%|BOR-%'), count(*) FILTER (WHERE topic=$3 AND key LIKE 'BOR-%'),
		count(*) FILTER (WHERE topic=$4 AND payload->>'deleted'='true'), count(*) FILTER (WHERE topic=$5 AND key=$6)
		FROM outbox WHERE tenant_id='store-a' AND id > $1`, outboxBefore, store.TopicPrice, store.TopicStock, store.TopicProduct, store.TopicCollection, col).Scan(&prices, &stocks, &products, &cols); err != nil {
		t.Fatal(err)
	}
	if prices != 30 || stocks != 30 || products != 30 || cols != 1 {
		t.Fatalf("events: prices %d stock %d products %d collection %d", prices, stocks, products, cols)
	}
	if err := e.db.System.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE tenant_id='store-a' AND action='product.delete' AND data->>'skus'='1' AND data->>'title' LIKE 'Borrable %'`).Scan(&audits); err != nil || audits != 30 {
		t.Fatalf("audit entries = %d (%v)", audits, err)
	}
	e.drain()
	ta, _ := e.proj.Tenant("store-a")
	if got := ta.Catalog.Matching("borrable"); len(got) != 0 {
		t.Fatalf("the store still finds %d deleted products", len(got))
	}
	if broken, err := e.db.VerifyAudit(t.Context(), "store-a"); err != nil || broken != 0 {
		t.Fatalf("audit chain broken at %d (%v)", broken, err)
	}
}
