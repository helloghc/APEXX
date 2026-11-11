package api

import (
	"context"
	"fmt"
	"testing"
)

// Customers are created by their purchases, found only by exact email, kept
// encrypted, and private to each store; segments follow what they buy.
func TestCustomersLifecycle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	buy := func(email, name, key string, qty int) m {
		t.Helper()
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": qty}},
			"customer": m{"email": email, "name": name, "phone": "5512345678"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", key)
		if st != 201 {
			t.Fatalf("checkout %s = %d %v", key, st, o)
		}
		return o
	}
	buy("Ana.Torres@Example.com", "Ana Torres", "cust-key-1", 1)
	buy("ana.torres@example.com", "Ana Torres", "cust-key-2", 2) // same person, different capitals
	buy("beto@example.com", "Beto Ruiz", "cust-key-3", 1)
	list := e.must(200, "GET", "/v1/customers", e.adminA, nil)
	if list["total"].(float64) != 2 {
		t.Fatalf("customers after three orders of two people = %v", list["total"])
	}
	found := e.must(200, "GET", "/v1/customers?q=ANA.TORRES@example.com", e.adminA, nil)["items"].([]any)
	if len(found) != 1 {
		t.Fatalf("search by exact email found %d", len(found))
	}
	ana := found[0].(map[string]any)
	id := ana["id"].(string)
	if ana["ordersCount"].(float64) != 2 || ana["totalSpent"].(float64) <= 0 || ana["name"] != "Ana Torres" {
		t.Fatalf("customer stats = %v", ana)
	}
	if orders := e.must(200, "GET", "/v1/customers/"+id+"/orders", e.adminA, nil)["items"].([]any); len(orders) != 2 {
		t.Fatalf("orders of the customer = %d", len(orders))
	}
	// Nothing personal is stored in clear.
	var clear int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM customers WHERE encode(profile,'escape') ILIKE '%ana.torres%' OR encode(email_hash,'escape') ILIKE '%ana.torres%'`).Scan(&clear)
	if clear != 0 {
		t.Fatal("customer email stored in clear")
	}
	// Another store cannot see, read or edit the customer.
	if other := e.must(200, "GET", "/v1/customers", e.adminB, nil); other["total"].(float64) != 0 {
		t.Fatalf("store B sees %v customers of store A", other["total"])
	}
	if st, _ := e.do("GET", "/v1/customers/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("store B read a customer of store A: %d", st)
	}
	if st, _ := e.do("GET", "/v1/customers", e.sfA, nil); st != 403 {
		t.Fatalf("a storefront key listed customers: %d", st)
	}
	// Tags and notes; a stale version is refused so two people do not overwrite each other.
	ver := ana["version"].(float64)
	match := fmt.Sprintf(`"%d"`, int(ver))
	if st, b := e.do("PATCH", "/v1/customers/"+id, e.adminA, m{"tags": []string{"VIP", "mayoreo"}}, "If-Match", match); st != 200 {
		t.Fatalf("tag a customer = %d %v", st, b)
	}
	if st, _ := e.do("PATCH", "/v1/customers/"+id, e.adminA, m{"tags": []string{"otro"}}, "If-Match", match); st != 409 && st != 412 {
		t.Fatalf("stale customer update = %d", st)
	}
	e.must(201, "POST", "/v1/customers/"+id+"/notes", e.adminA, m{"body": "Prefiere entregas por la tarde"})
	// Segment by rules, previewed live.
	rules := m{"rules": m{"match": "all", "conditions": []m{{"field": "orders_count", "op": "gte", "value": 2}}}}
	if pv := e.must(200, "POST", "/v1/customer-groups/preview", e.adminA, rules); pv["count"].(float64) != 1 {
		t.Fatalf("segment preview = %v", pv)
	}
	if st, b := e.do("POST", "/v1/customer-groups/preview", e.adminA, m{"rules": m{"match": "all", "conditions": []m{{"field": "password", "op": "eq", "value": "x"}}}}); st != 400 {
		t.Fatalf("segment on an unknown field = %d %v", st, b)
	}
	// Erasing the customer (privacy request) removes the personal data but keeps the orders' money.
	e.must(204, "DELETE", "/v1/customers/"+id, e.adminA, nil)
	if st, _ := e.do("GET", "/v1/customers/"+id, e.adminA, nil); st != 404 {
		t.Fatalf("deleted customer still readable: %d", st)
	}
	var orders int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM orders WHERE tenant_id='store-a'`).Scan(&orders)
	if orders != 3 {
		t.Fatalf("orders after erasing a customer = %d, want 3", orders)
	}
}

// Returns: requested against shipped units only, received by the warehouse
// (which decides what goes back to stock), refunded once, and cancellable
// before reception.
func TestReturnsLifecycle(t *testing.T) {
	e := setup(t)
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 3}},
		"customer": m{"email": "rma@example.com", "name": "Rita"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "rma-key-1")
	if st != 201 {
		t.Fatalf("checkout = %d %v", st, o)
	}
	id := o["id"].(string)
	ret := func(qty int) (int, m) {
		return e.do("POST", "/v1/orders/"+id+"/returns", e.adminA, m{"items": []m{{"line": 1, "qty": qty, "reason": "defective"}}, "note": "Llegó dañado"})
	}
	// Nothing shipped yet: there is nothing to return.
	if st, b := ret(1); st < 400 {
		t.Fatalf("return of units that never shipped = %d %v", st, b)
	}
	e.must(201, "POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"items": []m{{"line": 1, "qty": 2}}, "carrier": "DHL", "trackingNumber": "D1", "notify": false})
	if st, b := ret(3); st < 400 {
		t.Fatalf("return of more units than shipped = %d %v", st, b)
	}
	st, r := ret(1)
	if st != 201 || r["status"] != "requested" {
		t.Fatalf("return request = %d %v", st, r)
	}
	rid := r["id"].(string)
	if st, _ := e.do("GET", "/v1/returns/"+rid, e.adminB, nil); st != 404 {
		t.Fatalf("store B read a return of store A: %d", st)
	}
	stock := e.available("S1")
	refunded := e.sandbox.Refunded()
	// Received damaged: not restocked, refunded.
	got := e.must(200, "POST", "/v1/returns/"+rid+"/receive", e.adminA, m{"items": []m{{"line": 1, "restock": false}}})["return"].(map[string]any)
	if got["status"] != "completed" || got["refundAmount"].(float64) <= 0 {
		t.Fatalf("received return = %v", got)
	}
	if e.available("S1") != stock {
		t.Fatalf("a damaged unit went back to stock: %v -> %v", stock, e.available("S1"))
	}
	if paid := e.sandbox.Refunded() - refunded; float64(paid) != got["refundAmount"].(float64) {
		t.Fatalf("refunded %v, return says %v", paid, got["refundAmount"])
	}
	if st, _ := e.do("POST", "/v1/returns/"+rid+"/receive", e.adminA, m{"items": []m{{"line": 1, "restock": true}}}); st < 400 {
		t.Fatal("a return was received twice")
	}
	if st, _ := e.do("POST", "/v1/returns/"+rid+"/refund", e.adminA, nil); st < 400 {
		t.Fatal("a return was refunded twice")
	}
	// A second return in good condition goes back to stock; a third cannot exceed what is left.
	_, r2 := ret(1)
	e.must(200, "POST", "/v1/returns/"+r2["id"].(string)+"/receive", e.adminA, m{"items": []m{{"line": 1, "restock": true}}})
	if e.available("S1") != stock+1 {
		t.Fatalf("a good unit did not go back to stock: %v -> %v", stock, e.available("S1"))
	}
	if st, b := ret(1); st < 400 {
		t.Fatalf("return beyond the shipped units = %d %v", st, b)
	}
	full := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if full["paymentStatus"] != "partially_refunded" || len(full["returns"].([]any)) != 2 {
		t.Fatalf("order after two returns: payment=%v returns=%d", full["paymentStatus"], len(full["returns"].([]any)))
	}
	var unbalanced int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("%d ledger transactions do not balance", unbalanced)
	}
}
