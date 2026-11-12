package api

import (
	"context"
	"fmt"
	"testing"
)

// Loyalty: points on what was paid, at the tier's rate; a refund takes back
// exactly its share and a cancellation all of it; redemption becomes store
// credit; the ledger stays balanced; another store sees nothing.
func TestLoyaltyPoints(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("PUT", "/v1/settings/loyalty", e.adminA, m{"active": true, "earnPer": 5}); st != 400 {
		t.Fatalf("invalid program accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/loyalty", e.adminA, m{"active": true, "earnPer": 1000, "pointValue": 10, "minRedeem": 50,
		"tiers": []m{{"name": "VIP", "minSpend": 30000, "multiplierPct": 200}}})
	n := 0
	buy := func(qty int) m {
		n++
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": qty}},
			"customer": m{"email": "puntos@cliente.example", "name": "Puntos"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("loyalty-%d", n))
		if st != 201 {
			t.Fatalf("checkout = %d %v", st, o)
		}
		return o
	}
	o1 := buy(2)
	var cid string
	_ = e.db.System.QueryRow(ctx, `SELECT customer_id FROM orders WHERE id=$1`, o1["id"]).Scan(&cid)
	acct := func() m { return e.must(200, "GET", "/v1/customers/"+cid+"/loyalty", e.adminA, nil) }
	base := o1["grandTotal"].(float64) - o1["shipping"].(float64)
	if got := acct()["points"].(float64); got != float64(int64(base)/1000) {
		t.Fatalf("points after first order = %v, want %v", got, int64(base)/1000)
	}
	first := acct()["points"].(float64)
	// Refund one unit: the order keeps only the points of what stays paid.
	e.must(201, "POST", "/v1/orders/"+o1["id"].(string)+"/refunds", e.adminA, m{"amount": 10000, "reason": "uno de dos", "notify": false})
	afterRefund := float64((int64(base) - 10000) / 1000)
	if got := acct()["points"].(float64); got != afterRefund {
		t.Fatalf("points after refund = %v, want %v (had %v)", got, afterRefund, first)
	}
	// Spend past 300.00: the next order earns double.
	o2 := buy(3)
	before := acct()["points"].(float64)
	o3 := buy(1)
	b3 := o3["grandTotal"].(float64) - o3["shipping"].(float64)
	if got := acct()["points"].(float64) - before; got != float64(int64(b3)/1000*2) || acct()["tier"] != "VIP" {
		t.Fatalf("VIP order earned %v, want %v (tier %v)", got, int64(b3)/1000*2, acct()["tier"])
	}
	// Cancelling takes back all of that order's points.
	beforeCancel := acct()["points"].(float64)
	b2 := o2["grandTotal"].(float64) - o2["shipping"].(float64)
	e.must(200, "POST", "/v1/orders/"+o2["id"].(string)+"/cancel", e.adminA, m{"reason": "prueba"})
	if got := acct()["points"].(float64); got != beforeCancel-float64(int64(b2)/1000) {
		t.Fatalf("points after cancel = %v, want %v", got, beforeCancel-float64(int64(b2)/1000))
	}
	// Manual adjustment needs a reason and cannot go negative.
	if st, _ := e.do("POST", "/v1/customers/"+cid+"/loyalty/adjust", e.adminA, m{"points": 100}); st != 400 {
		t.Fatalf("adjust without reason: %d", st)
	}
	if st, _ := e.do("POST", "/v1/customers/"+cid+"/loyalty/adjust", e.adminA, m{"points": -100000, "note": "error"}); st != 400 {
		t.Fatalf("negative balance allowed: %d", st)
	}
	e.must(200, "POST", "/v1/customers/"+cid+"/loyalty/adjust", e.adminA, m{"points": 60, "note": "Bienvenida"})
	if st, _ := e.do("GET", "/v1/customers/"+cid+"/loyalty", e.adminB, nil); st != 404 {
		t.Fatalf("store B read store A's points: %d", st)
	}
	// The shopper redeems into store credit.
	_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, cid)
	if err := e.server.Customers.SetPassword(ctx, "store-a", cid, "Una-clave-larga-2026", ""); err != nil {
		t.Fatal(err)
	}
	token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", "puntos@cliente.example", "Una-clave-larga-2026")
	if err != nil {
		t.Fatal(err)
	}
	as := func(method, path string, body any) (int, m) {
		return e.do(method, path, "", body, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL)
	}
	st, mine := as("GET", "/s/store-a/api/account/loyalty", nil)
	bal := mine["account"].(map[string]any)["points"].(float64)
	if st != 200 || bal < 50 {
		t.Fatalf("shopper account = %d %v", st, mine)
	}
	if st, b := as("POST", "/s/store-a/api/account/loyalty/redeem", m{"points": 10}); st != 409 {
		t.Fatalf("redeem under the minimum = %d %v", st, b)
	}
	if st, b := as("POST", "/s/store-a/api/account/loyalty/redeem", m{"points": bal + 1}); st != 409 {
		t.Fatalf("redeem over the balance = %d %v", st, b)
	}
	st, card := as("POST", "/s/store-a/api/account/loyalty/redeem", m{"points": 50})
	if st != 201 || card["amount"].(float64) != 500 || card["code"] == "" {
		t.Fatalf("redeem = %d %v", st, card)
	}
	if got := acct()["points"].(float64); got != bal-50 {
		t.Fatalf("balance after redeem = %v, want %v", got, bal-50)
	}
	// Every ledger entry balances.
	var bad int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries WHERE tenant_id='store-a' GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&bad)
	if bad != 0 {
		t.Fatalf("%d unbalanced ledger transactions", bad)
	}
}
