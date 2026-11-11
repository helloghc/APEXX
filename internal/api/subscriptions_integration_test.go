package api

import (
	"context"
	"testing"
	"time"
)

// A subscription is created by a normal checkout, renewed by the worker with
// the saved card exactly once per delivery, and stops after repeated failures.
func TestSubscriptionLifecycle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("PUT", "/v1/subscription-config", e.adminA, m{"enabled": true, "plans": []string{"9y"}}); st != 400 {
		t.Fatalf("unknown plan accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/subscription-config", e.adminA, m{"enabled": true, "discountBps": 1000, "plans": []string{"1m", "2w"}, "productIds": []string{"P1"}})
	e.drain()

	sim := e.simulate(m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2, "plan": "1m"}, {"sku": "S2", "qty": 1, "plan": "1m"}}})
	tot := sim["totals"].(map[string]any)
	if d := tot["discounts"].(map[string]any)["amount"].(float64); d != 2000 { // 10% of the subscribed product only
		t.Fatalf("subscription discount = %v, want 2000", d)
	}
	body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2, "plan": "1m"}},
		"customer": m{"email": "sub@example.com", "name": "Sara Sub"}, "paymentMethod": "pm_card_visa"}
	card := e.must(201, "POST", "/v1/gift-cards", e.adminA, m{"amount": 10000})
	withCard := m{}
	for k, v := range body {
		withCard[k] = v
	}
	withCard["giftCards"] = []string{card["code"].(string)}
	if st, b := e.do("POST", "/v1/checkout", e.sfA, withCard, "Idempotency-Key", "sub-key-0"); st != 409 || b["code"] != "gift_card_not_with_subscription" {
		t.Fatalf("subscription with gift card = %d %v", st, b)
	}
	st, first := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "sub-key-1")
	if st != 201 || first["discounts"].(float64) != 2000 {
		t.Fatalf("first order = %d %v", st, first)
	}
	list := e.must(200, "GET", "/v1/subscriptions", e.adminA, nil)["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("subscriptions after checkout = %d", len(list))
	}
	sub := list[0].(map[string]any)
	id := sub["id"].(string)
	next, _ := time.Parse(time.RFC3339Nano, sub["nextAt"].(string))
	if sub["status"] != "active" || sub["plan"] != "1m" || next.Before(time.Now().AddDate(0, 0, 27)) || next.After(time.Now().AddDate(0, 0, 32)) {
		t.Fatalf("new subscription = %v", sub)
	}
	if st, _ := e.do("GET", "/v1/subscriptions/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("another store read the subscription: %d", st)
	}
	var clear int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE encode(customer,'escape') LIKE '%sub@example.com%'`).Scan(&clear)
	if clear != 0 {
		t.Fatal("the subscriber's data is stored in clear")
	}

	due := func() {
		t.Helper()
		if _, err := e.db.System.Exec(ctx, `UPDATE subscriptions SET next_at = now() - interval '1 minute' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	// Not due: nothing happens.
	calls := e.sandbox.Calls
	if n, err := e.orders.RenewSubscriptions(ctx); err != nil || n != 0 || e.sandbox.Calls != calls {
		t.Fatalf("renewal before the date: n=%d err=%v", n, err)
	}
	// Due: one order, one charge, next date moves one month, stock is taken.
	due()
	before := e.available("S1")
	if n, err := e.orders.RenewSubscriptions(ctx); err != nil || n != 1 {
		t.Fatalf("renewal: n=%d err=%v", n, err)
	}
	if n, _ := e.orders.RenewSubscriptions(ctx); n != 0 {
		t.Fatal("the same delivery renewed twice")
	}
	e.drain()
	d := e.must(200, "GET", "/v1/subscriptions/"+id, e.adminA, nil)
	if d["cycle"].(float64) != 2 || len(d["orders"].([]any)) != 2 || e.available("S1") != before-2 {
		t.Fatalf("after renewal: cycle=%v orders=%d stock=%v (was %v)", d["cycle"], len(d["orders"].([]any)), e.available("S1"), before)
	}
	second := d["orders"].([]any)[0].(map[string]any)
	if second["status"] != "paid" || second["total"].(float64) != first["grandTotal"].(float64) {
		t.Fatalf("renewal order = %v, first total %v", second, first["grandTotal"])
	}
	// Skip moves the date without charging; pause stops renewals; resume brings them back.
	e.must(200, "POST", "/v1/subscriptions/"+id+"/skip", e.adminA, nil)
	e.must(200, "POST", "/v1/subscriptions/"+id+"/pause", e.adminA, nil)
	due()
	if n, _ := e.orders.RenewSubscriptions(ctx); n != 0 {
		t.Fatal("a paused subscription was renewed")
	}
	e.must(200, "POST", "/v1/subscriptions/"+id+"/resume", e.adminA, nil)

	// A card that stops working: three attempts, then the subscription waits for the customer.
	if _, err := e.db.System.Exec(ctx, `UPDATE subscriptions SET payment_method='pm_card_chargeDeclined' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	stock := e.available("S1")
	for i := 1; i <= 3; i++ {
		due()
		if _, err := e.orders.RenewSubscriptions(ctx); err != nil {
			t.Fatal(err)
		}
		e.drain()
		d = e.must(200, "GET", "/v1/subscriptions/"+id, e.adminA, nil)
		want := "active"
		if i == 3 {
			want = "past_due"
		}
		if d["failures"].(float64) != float64(i) || d["status"] != want {
			t.Fatalf("attempt %d: failures=%v status=%v", i, d["failures"], d["status"])
		}
	}
	if d["cycle"].(float64) != 2 || e.available("S1") != stock {
		t.Fatalf("failed renewals changed deliveries or stock: cycle=%v stock %v -> %v", d["cycle"], stock, e.available("S1"))
	}
	due()
	if n, _ := e.orders.RenewSubscriptions(ctx); n != 0 {
		t.Fatal("a past-due subscription was charged again")
	}
	// The public manage link needs the right token; cancel forgets the saved card.
	if st, _ := e.do("POST", "/s/store-a/api/subscription/not-a-token/cancel", "", nil); st != 404 {
		t.Fatalf("manage with a wrong token = %d", st)
	}
	e.must(200, "POST", "/v1/subscriptions/"+id+"/cancel", e.adminA, nil)
	var pm string
	_ = e.db.System.QueryRow(ctx, `SELECT payment_method FROM subscriptions WHERE id=$1`, id).Scan(&pm)
	if pm != "" {
		t.Fatal("a cancelled subscription keeps the saved card")
	}
	if st, _ := e.do("POST", "/v1/subscriptions/"+id+"/resume", e.adminA, nil); st != 409 {
		t.Fatalf("resume after cancel = %d", st)
	}
	var unbalanced int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("%d ledger transactions do not balance", unbalanced)
	}
}
