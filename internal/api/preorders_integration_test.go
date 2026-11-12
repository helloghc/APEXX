package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Preorders: sold from a pool without touching stock, with a deposit now and
// the balance by link later; the pool limit holds under concurrency; a
// cancellation refunds only the deposit; the ledger always balances.
func TestPreordersWithDeposit(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	ships := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	if st, _ := e.do("PUT", "/v1/preorders/S2", e.adminA, m{"shipsOn": "2020-01-01", "active": true}); st != 400 {
		t.Fatalf("past ship date accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/preorders/S2", e.adminA, m{"shipsOn": ships, "depositBps": 100, "active": true}); st != 400 {
		t.Fatalf("1%% deposit accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/preorders/S2", e.sfA, m{"shipsOn": ships, "active": true}); st != 401 && st != 403 {
		t.Fatalf("storefront key set a preorder: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/preorders/S2", e.adminB, m{"shipsOn": ships, "active": true}); st != 404 {
		t.Fatalf("store B set a preorder on store A's SKU: %d", st)
	}
	e.must(200, "PUT", "/v1/preorders/S2", e.adminA, m{"shipsOn": ships, "depositBps": 2000, "maxUnits": 3, "active": true, "note": "Llega en un mes"})
	if e.available("S2") != 3 {
		t.Fatalf("preorder availability = %v, want 3", e.available("S2"))
	}
	if b := e.must(200, "GET", "/v1/preorders", e.adminB, nil); len(b["items"].([]any)) != 0 {
		t.Fatalf("store B sees store A's preorders: %v", b)
	}
	sim := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S2", "qty": 2}}})
	tot := sim["totals"].(map[string]any)
	if tot["dueLater"].(map[string]any)["amount"].(float64) != 16000 || sim["lines"].([]any)[0].(map[string]any)["preorder"] == nil {
		t.Fatalf("cart: dueLater=%v line=%v", tot["dueLater"], sim["lines"])
	}
	grand := tot["grand"].(map[string]any)["amount"].(float64)

	n := 0
	buy := func(qty int, pm string) (int, m) {
		n++
		return e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S2", "qty": qty}},
			"customer": m{"email": "preventa@cliente.example", "name": "Preventa"}, "paymentMethod": pm}, "Idempotency-Key", fmt.Sprintf("preorder-%d", n))
	}
	st, o := buy(2, "pm_card_visa")
	if st != 201 || o["deferred"].(float64) != 16000 || o["paymentStatus"] != "balance_due" || o["lines"].([]any)[0].(map[string]any)["preorderShipsOn"] != ships {
		t.Fatalf("preorder order = %d deferred=%v status=%v lines=%v", st, o["deferred"], o["paymentStatus"], o["lines"])
	}
	var charged int64
	if err := e.db.System.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM payments WHERE tenant_id='store-a' AND order_id=$1 AND status='captured'`, o["id"]).Scan(&charged); err != nil || float64(charged) != grand-16000 {
		t.Fatalf("charged now %d, want %v (%v)", charged, grand-16000, err)
	}
	// Stock is untouched; the pool shrinks.
	var onHand int64
	_ = e.db.System.QueryRow(ctx, `SELECT on_hand FROM stock_levels WHERE tenant_id='store-a' AND sku='S2' AND location_id='w1'`).Scan(&onHand)
	if onHand != 10 || e.available("S2") != 1 {
		t.Fatalf("after preorder: on hand %d, pool %v", onHand, e.available("S2"))
	}
	if st, _ := buy(2, "pm_card_visa"); st == 201 {
		t.Fatal("sold more than the preorder pool")
	}
	// Five buyers at once for the last unit: exactly one gets it.
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := []string{}
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, b := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S2", "qty": 1}},
				"customer": m{"email": "carrera@cliente.example", "name": "Carrera"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("preorder-race-%d", i))
			if st == 201 {
				mu.Lock()
				won = append(won, b["id"].(string))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(won) != 1 || e.available("S2") != 0 {
		t.Fatalf("race: %d orders, pool %v", len(won), e.available("S2"))
	}
	// The limit cannot go under what was sold.
	if st, _ := e.do("PUT", "/v1/preorders/S2", e.adminA, m{"shipsOn": ships, "depositBps": 2000, "maxUnits": 1, "active": true}); st != 400 {
		t.Fatalf("limit under sold accepted: %d", st)
	}
	info := e.must(200, "GET", "/v1/preorders/S2", e.adminA, nil)
	if info["ordersOwing"].(float64) != 2 {
		t.Fatalf("orders owing = %v", info)
	}
	// Collect: both buyers get a link; one pays.
	if r := e.must(200, "POST", "/v1/preorders/S2/collect", e.adminA, nil); r["sent"].(float64) != 2 {
		t.Fatalf("collect = %v", r)
	}
	url, err := e.orders.SendBalanceLink(ctx, "store-a", "test", o["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	token := url[strings.Index(url, "t=")+2:]
	if _, err := e.orders.PayWithLink(ctx, "store-a", token, "pm_card_visa"); err != nil {
		t.Fatal(err)
	}
	if got := e.must(200, "GET", "/v1/orders/"+o["id"].(string), e.adminA, nil); got["paymentStatus"] != "paid" {
		t.Fatalf("after paying the balance: %v", got["paymentStatus"])
	}
	// The other one cancels: only its deposit goes back, and the unit returns to the pool.
	refunded := e.sandbox.Refunded()
	other := e.must(200, "GET", "/v1/orders/"+won[0], e.adminA, nil)
	e.must(200, "POST", "/v1/orders/"+won[0]+"/cancel", e.adminA, m{"reason": "ya no"})
	if paid := e.sandbox.Refunded() - refunded; float64(paid) != other["grandTotal"].(float64)-other["deferred"].(float64) {
		t.Fatalf("cancel refunded %d, deposit was %v", paid, other["grandTotal"].(float64)-other["deferred"].(float64))
	}
	if e.available("S2") != 1 {
		t.Fatalf("pool after cancel = %v", e.available("S2"))
	}
	// Every ledger entry of every order balances, and nothing is left owed on the receivable.
	var bad, owed int64
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries WHERE tenant_id='store-a' GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&bad)
	_ = e.db.System.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM ledger_entries WHERE tenant_id='store-a' AND account='customer_receivable' AND order_id = ANY($1)`, []string{o["id"].(string), won[0]}).Scan(&owed)
	if bad != 0 || owed != 0 {
		t.Fatalf("ledger: %d unbalanced, receivable left %d", bad, owed)
	}
	// Ending the preorder sells from stock again.
	e.must(204, "DELETE", "/v1/preorders/S2", e.adminA, nil)
	if e.available("S2") != 10 {
		t.Fatalf("after ending the preorder: %v", e.available("S2"))
	}
}
