package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/helloghc/apexx/internal/reports"
)

// The dashboard's low and out of stock counts come from memory; they must
// equal what the database says, also after a sale reserves stock and after
// stock runs out.
func TestStockCountsFromMemoryMatchDatabase(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	compare := func(label string) {
		t.Helper()
		e.drain()
		var low, out int64
		if err := e.db.System.QueryRow(ctx, `SELECT count(*) FILTER (WHERE avail > 0 AND avail <= $2), count(*) FILTER (WHERE avail <= 0) FROM (
			SELECT sum(on_hand - reserved) AS avail FROM stock_levels WHERE tenant_id=$1 GROUP BY sku) x`, "store-a", reports.LowStockThreshold).Scan(&low, &out); err != nil {
			t.Fatal(err)
		}
		ta, _ := e.proj.Tenant("store-a")
		ml, mo := ta.Inventory.StockCounts(reports.LowStockThreshold)
		if ml != low || mo != out {
			t.Fatalf("%s: memory low=%d out=%d, database low=%d out=%d", label, ml, mo, low, out)
		}
	}
	compare("start")
	body := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 2}},
		"customer": map[string]any{"email": "ana@example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
	if c, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "stock-counts-1"); c != 201 {
		t.Fatalf("checkout = %d %v", c, o)
	}
	compare("after a sale")
	if st, b := e.do("PUT", "/v1/inventory/S2/w1", e.adminA, map[string]any{"onHand": 0}); st >= 300 {
		t.Fatalf("set stock = %d %v", st, b)
	}
	compare("after running out")
	sameInventory(t, e, "after running out")
	ta, _ := e.proj.Tenant("store-a")
	if _, out := ta.Inventory.StockCounts(reports.LowStockThreshold); out == 0 {
		t.Fatal("S2 at zero is not counted as out of stock")
	}
}

// The inventory report from memory equals the one from the database: same
// totals, same rows in the same order, for every filter.
func sameInventory(t *testing.T, e *env, label string) {
	t.Helper()
	ctx := context.Background()
	mem := e.server.Reports
	sql := &reports.Service{DB: mem.DB, Keys: mem.Keys, Log: mem.Log, StockCounts: mem.StockCounts}
	if mem.StockLines == nil {
		t.Fatal("the server does not answer the inventory from memory")
	}
	for _, st := range []string{"", "low", "out", "ok"} {
		for _, limit := range []int{1, 500} {
			a, err1 := mem.InventoryReport(ctx, "store-a", "", st, 5, limit)
			b, err2 := sql.InventoryReport(ctx, "store-a", "", st, 5, limit)
			if err1 != nil || err2 != nil {
				t.Fatalf("%s: %v %v", label, err1, err2)
			}
			if fmt.Sprintf("%+v", a) != fmt.Sprintf("%+v", b) {
				t.Fatalf("%s state=%q limit=%d:\nmemory   %+v\ndatabase %+v", label, st, limit, a, b)
			}
		}
	}
}
