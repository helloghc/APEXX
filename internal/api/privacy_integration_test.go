package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A customer's data can be exported and erased; erasing keeps the accounting.
func TestCustomerPrivacy(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "borrame@example.com", "name": "Rosa Privada", "address": "Calle Secreta 123"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "privacy-key-1")
	if st != 201 {
		t.Fatalf("checkout = %d %v", st, o)
	}
	e.drain()
	orderID, grand := o["id"].(string), o["grandTotal"].(float64)
	cid := e.must(200, "GET", "/v1/customers?q=borrame@example.com", e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)

	// Export: only for admins of customers, with the orders inside.
	_, raw, _ := e.raw("GET", "/v1/customers/"+cid+"/export", e.adminA, nil, "")
	var exp map[string]any
	if err := json.Unmarshal(raw, &exp); err != nil || !strings.Contains(string(raw), "Rosa Privada") || len(exp["orders"].([]any)) != 1 {
		t.Fatalf("export: %v %.300s", err, raw)
	}
	if st, _ := e.do("GET", "/v1/customers/"+cid+"/export", e.adminB, nil); st != 404 {
		t.Fatalf("another store exported the customer: %d", st)
	}

	// An active subscription blocks the erasure.
	if _, err := e.db.System.Exec(ctx, `INSERT INTO subscriptions (tenant_id, id, customer_id, status, customer, token_hash, channel, items, plan, next_at, first_order_id)
		VALUES ('store-a', 'sub_test', $1, 'active', '\x00', '\x00', 'mx', '[]', '1m', now() + interval '1 day', $2)`, cid, orderID); err != nil {
		t.Fatal(err)
	}
	if st, b := e.do("DELETE", "/v1/customers/"+cid, e.adminA, nil); st != 409 || b["code"] != "active_subscriptions" {
		t.Fatalf("erase with an active subscription = %d %v", st, b)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE subscriptions SET status='cancelled' WHERE id='sub_test'`); err != nil {
		t.Fatal(err)
	}

	e.must(204, "DELETE", "/v1/customers/"+cid, e.adminA, nil)
	e.drain()
	// The order is still there with its amount; the person is gone from it.
	ord := e.must(200, "GET", "/v1/orders/"+orderID, e.adminA, nil)
	b, _ := json.Marshal(ord)
	if ord["grandTotal"].(float64) != grand || strings.Contains(string(b), "Rosa Privada") || strings.Contains(string(b), "Calle Secreta") || strings.Contains(string(b), "borrame@") {
		t.Fatalf("order after erasure still identifies the person or lost its amount: %.400s", b)
	}
	if found := e.must(200, "GET", "/v1/customers?q=borrame@example.com", e.adminA, nil)["items"].([]any); len(found) != 0 {
		t.Fatal("the customer can still be found")
	}
	// Erasing twice is harmless.
	if st, _ := e.do("DELETE", "/v1/customers/"+cid, e.adminA, nil); st != 404 {
		t.Fatalf("second erase = %d", st)
	}
}
