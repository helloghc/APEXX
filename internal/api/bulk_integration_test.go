package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
)

// Bulk operations: 1,500 lines applied in chunks, bad lines reported and
// skipped, prices with their own permission, isolation, and a streamed export.
func TestBulkOperations(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < 1500; i++ {
		if _, err := e.db.UpsertProduct(ctx, "store-a", "test", catalog.Product{ID: fmt.Sprintf("B%04d", i), Handle: fmt.Sprintf("b%04d", i), Title: fmt.Sprintf("Masivo %d", i),
			Variants: []catalog.Variant{{SKU: fmt.Sprintf("BK%04d", i), Title: "Única"}}}, 0); err != nil {
			t.Fatal(err)
		}
	}
	e.drain()
	items := []m{}
	for i := 0; i < 1500; i++ {
		items = append(items, m{"sku": fmt.Sprintf("BK%04d", i), "location": "w1", "onHand": i % 7})
	}
	items = append(items, m{"sku": "NOPE", "location": "w1", "onHand": 1}, m{"sku": "BK0001", "location": "w1", "onHand": -5}, m{"sku": "BK0002", "location": "w1", "onHand": 99})
	if st, _ := e.do("POST", "/v1/bulk-operations", e.sfA, m{"kind": "stock", "items": items}); st != 401 && st != 403 {
		t.Fatalf("storefront key ran a bulk job: %d", st)
	}
	j := e.must(202, "POST", "/v1/bulk-operations", e.adminA, m{"kind": "stock", "items": items})
	if j["total"].(float64) != 1503 || j["failed"].(float64) != 2 || j["status"] != "queued" {
		t.Fatalf("job = %v", j)
	}
	if st, _ := e.do("GET", "/v1/bulk-operations/"+j["id"].(string), e.adminB, nil); st != 404 {
		t.Fatalf("store B read store A's job: %d", st)
	}
	if _, err := e.server.RunBulkJobs(ctx); err != nil {
		t.Fatal(err)
	}
	done := e.must(200, "GET", "/v1/bulk-operations/"+j["id"].(string), e.adminA, nil)
	if done["status"] != "done" || done["processed"].(float64) != 1503 || len(done["errors"].([]any)) != 2 {
		t.Fatalf("finished job = %v", done)
	}
	// The repeated line for BK0002 wins; the bad ones did nothing.
	if e.available("BK1499") != 1499%7 || e.available("BK0002") != 99 || e.available("BK0001") != 1 {
		t.Fatalf("stock BK1499=%v BK0002=%v BK0001=%v", e.available("BK1499"), e.available("BK0002"), e.available("BK0001"))
	}
	// Prices: volume tiers above 1 are kept; a compare-at not above the price is refused.
	cmp := 90000
	p := e.must(202, "POST", "/v1/bulk-operations", e.adminA, m{"kind": "prices", "channel": "mx", "items": []m{
		{"sku": "BK0000", "amount": 12345}, {"sku": "BK0001", "amount": 50000, "compareAt": cmp}, {"sku": "BK0003", "amount": 500, "compareAt": 100},
	}})
	if p["failed"].(float64) != 1 {
		t.Fatalf("prices job = %v", p)
	}
	_, _ = e.server.RunBulkJobs(ctx)
	e.drain()
	sim := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "BK0001", "qty": 1}}})
	if l := sim["lines"].([]any)[0].(map[string]any); l["unit"].(map[string]any)["amount"].(float64) != 50000 {
		t.Fatalf("bulk price not applied: %v", l)
	}
	if st, _ := e.do("POST", "/v1/bulk-operations", e.adminA, m{"kind": "prices", "channel": "nope", "items": []m{{"sku": "BK0000", "amount": 1}}}); st != 400 {
		t.Fatalf("unknown channel accepted: %d", st)
	}
	// Export: one line per product, streamed.
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/bulk/products.jsonl", nil)
	req.Header.Set("Authorization", "Bearer "+e.adminA)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	n := 0
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		n++
	}
	if res.StatusCode != 200 || n != 1503 {
		t.Fatalf("export = %d, %d lines", res.StatusCode, n)
	}
}
