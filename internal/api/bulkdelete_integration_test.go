package api

import (
	"fmt"
	"testing"
)

// Deleting many products at once: same rules as deleting one (catalog
// admin), ids that are gone are skipped, the store stops showing them and
// the audit chain stays whole.
func TestBulkDeleteProducts(t *testing.T) {
	e := setup(t)
	var ids []string
	for i := 0; i < 30; i++ {
		p := e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": fmt.Sprintf("borrar-%d", i), "title": fmt.Sprintf("Borrable %d", i), "status": "active",
			"variants": []map[string]any{{"sku": fmt.Sprintf("BOR-%d", i), "title": "Única"}}})
		ids = append(ids, p["id"].(string))
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
	e.drain()
	ta, _ := e.proj.Tenant("store-a")
	if got := ta.Catalog.Matching("borrable"); len(got) != 0 {
		t.Fatalf("the store still finds %d deleted products", len(got))
	}
	if broken, err := e.db.VerifyAudit(t.Context(), "store-a"); err != nil || broken != 0 {
		t.Fatalf("audit chain broken at %d (%v)", broken, err)
	}
}
