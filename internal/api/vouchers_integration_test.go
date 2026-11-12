package api

import (
	"context"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/payments"
)

// Cash (OXXO) and transfer (SPEI): the order waits with its stock kept,
// becomes paid when the money arrives, and is cancelled (stock back) when
// nobody pays before the voucher expires.
func TestVoucherPayments(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	body := func(method, key string) (int, m) {
		return e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
			"customer": m{"email": "oxxo@cliente.example", "name": "Olga"}, "paymentMethod": method}, "Idempotency-Key", key)
	}
	if st, b := body("oxxo", "voucher-key-0"); st != 409 || !strings.Contains(b["detail"].(string), "no está disponible") {
		t.Fatalf("OXXO while off = %d %v", st, b)
	}
	if st, _ := e.do("PUT", "/v1/settings/payment-methods", e.sfA, m{"oxxo": true}); st != 401 && st != 403 {
		t.Fatalf("storefront key turned methods on: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/payment-methods", e.adminA, m{"oxxo": true, "spei": true})
	if pm := e.must(200, "GET", "/s/store-a/api/payment-methods", "", nil); pm["oxxo"] != true {
		t.Fatalf("shop methods = %v", pm)
	}
	if pm := e.must(200, "GET", "/s/store-b/api/payment-methods", "", nil); pm["oxxo"] == true {
		t.Fatal("store B shows store A's methods")
	}
	stock := e.available("S1")
	st, o := body("oxxo", "voucher-key-1")
	if st != 201 || o["status"] != "pending_payment" || o["voucher"] == nil || o["voucher"].(map[string]any)["kind"] != "oxxo" {
		t.Fatalf("OXXO order = %d %v", st, o)
	}
	if e.available("S1") != stock-1 {
		t.Fatal("the voucher order does not keep its stock")
	}
	id := o["id"].(string)
	// The reconciler leaves a live voucher alone.
	if _, err := e.orders.Reconcile(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil); got["status"] != "pending_payment" {
		t.Fatalf("live voucher was settled: %v", got["status"])
	}
	// The buyer pays at the store: the order is paid, once.
	if st, _ := e.do("POST", "/v1/sandbox/orders/"+id+"/pay-voucher", e.adminB, nil); st < 400 {
		t.Fatalf("store B paid store A's voucher: %d", st)
	}
	e.must(204, "POST", "/v1/sandbox/orders/"+id+"/pay-voucher", e.adminA, nil)
	paid := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if paid["status"] != "paid" || paid["voucher"] != nil {
		t.Fatalf("after paying: %v voucher=%v", paid["status"], paid["voucher"])
	}
	var pending int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id=$1 AND status='pending'`, id).Scan(&pending)
	if pending != 0 {
		t.Fatal("the pending payment row stayed after payment")
	}
	if st, _ := e.do("POST", "/v1/sandbox/orders/"+id+"/pay-voucher", e.adminA, nil); st != 409 {
		t.Fatalf("paying twice = %d", st)
	}
	// SPEI nobody pays: expired, cancelled, stock back.
	stock = e.available("S1")
	st, o2 := body("spei", "voucher-key-2")
	v := o2["voucher"].(map[string]any)
	if st != 201 || v["kind"] != "spei" || len(v["clabe"].(string)) != 18 {
		t.Fatalf("SPEI order = %d %v", st, o2)
	}
	e.server.Payments.Sandbox.(*payments.Sandbox).ExpireVoucher(o2["id"].(string))
	_, _ = e.db.System.Exec(ctx, `UPDATE orders SET voucher = jsonb_set(voucher, '{expiresAt}', to_jsonb(now() - interval '1 minute')) WHERE id=$1`, o2["id"])
	if _, err := e.orders.Reconcile(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.must(200, "GET", "/v1/orders/"+o2["id"].(string), e.adminA, nil); got["status"] != "cancelled" || e.available("S1") != stock {
		t.Fatalf("expired voucher: %v, stock %v -> %v", got["status"], stock, e.available("S1"))
	}
	// Limits: OXXO only up to $10,000; never with instalments.
	if st, _ := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}, "installments": 3,
		"customer": m{"email": "oxxo@cliente.example", "name": "Olga"}, "paymentMethod": "oxxo"}, "Idempotency-Key", "voucher-key-3"); st != 409 {
		t.Fatalf("OXXO with instalments = %d", st)
	}
	var unbalanced int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("unbalanced ledger: %d", unbalanced)
	}
}
