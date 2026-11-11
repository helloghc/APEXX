package api

import (
	"context"
	"testing"
)

// Reversion audit: an order that touches everything at once (coupon, gift
// card, bank card, a marketplace seller's product, stock in two products) is
// shipped in part and then cancelled. Every counter must return to where it
// was, and the books must show no money left anywhere for that order.
func TestFullOrderReversion(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	seller := e.must(201, "POST", "/v1/sellers", e.adminA, m{"name": "Vendedor Uno", "commissionBps": 2000})["id"].(string)
	e.must(204, "PUT", "/v1/sellers/"+seller+"/products", e.adminA, m{"productIds": []string{"P1"}})
	promo := e.mustPromo(m{"name": "Vuelve 10", "type": "order_percent", "bps": 1000, "conditions": m{"requiresCoupon": true}})
	e.mustCoupon("VUELVE10", promo, nil)
	card := e.must(201, "POST", "/v1/gift-cards", e.adminA, m{"amount": 5000})
	cardID, code := card["id"].(string), card["code"].(string)
	e.drain()

	stock1, stock2 := e.available("S1"), e.available("S2")
	refundedBefore := e.sandbox.Refunded()
	body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}, {"sku": "S2", "qty": 1}}, "coupons": []string{"VUELVE10"}, "giftCards": []string{code},
		"customer": m{"email": "reversa@example.com", "name": "Rita Reversa"}, "paymentMethod": "pm_card_visa"}
	st, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "reversion-1")
	if st != 201 || o["discounts"].(float64) != 3000 {
		t.Fatalf("checkout = %d discounts=%v %v", st, o["discounts"], o["detail"])
	}
	id, grand := o["id"].(string), o["grandTotal"].(float64)
	e.drain()
	if uses, _ := e.promoUses(promo); uses != 1 {
		t.Fatalf("promotion uses after the order = %v", uses)
	}
	sel := func() m { return e.must(200, "GET", "/v1/sellers/"+seller, e.adminA, nil)["seller"].(m) }
	if s := sel(); s["sales"].(float64) != 18000 || s["balance"].(float64) != 14400 { // 2 × 100 less 10%, 20% commission
		t.Fatalf("seller after the sale: %v", s)
	}
	if e.available("S1") != stock1-2 || e.available("S2") != stock2-1 {
		t.Fatal("stock was not taken")
	}
	// One unit already left the warehouse: it cannot come back by cancelling.
	e.must(201, "POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"items": []m{{"line": 1, "qty": 1}}, "carrier": "Estafeta", "trackingNumber": "X1", "notify": false})
	e.must(200, "POST", "/v1/orders/"+id+"/cancel", e.adminA, m{"reason": "auditoría de reversión"})
	e.drain()

	full := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if full["refundedTotal"].(float64) != grand || full["paidTotal"].(float64) != 0 {
		t.Fatalf("after cancelling: refunded=%v paid=%v of %v", full["refundedTotal"], full["paidTotal"], grand)
	}
	if got := e.must(200, "GET", "/v1/gift-cards/"+cardID, e.adminA, nil)["card"].(m)["balance"].(float64); got != 5000 {
		t.Fatalf("gift card balance after cancelling = %v, want 5000", got)
	}
	if got := e.sandbox.Refunded() - refundedBefore; float64(got) != grand-5000 {
		t.Fatalf("bank card got back %v, paid %v", got, grand-5000)
	}
	if s := sel(); s["sales"].(float64) != 0 || s["balance"].(float64) != 0 || s["commission"].(float64) != 0 {
		t.Fatalf("seller statement after cancelling: %v", s)
	}
	if uses, disc := e.promoUses(promo); uses != 0 || disc != 0 {
		t.Fatalf("promotion after cancelling: uses=%v discount=%v", uses, disc)
	}
	// Units that never shipped are sellable again; the shipped one is not.
	if e.available("S1") != stock1-1 || e.available("S2") != stock2 {
		t.Fatalf("stock after cancelling: S1=%v (want %v) S2=%v (want %v)", e.available("S1"), stock1-1, e.available("S2"), stock2)
	}
	// The books: every transaction balances, and the order leaves nothing in any account.
	var unbalanced, open int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT account FROM ledger_entries WHERE order_id=$1 GROUP BY account HAVING sum(amount) <> 0) x`, id).Scan(&open)
	if unbalanced != 0 || open != 0 {
		rows, _ := e.db.System.Query(ctx, `SELECT account, sum(amount) FROM ledger_entries WHERE order_id=$1 GROUP BY account ORDER BY 1`, id)
		for rows.Next() {
			var a string
			var n int64
			_ = rows.Scan(&a, &n)
			t.Logf("  %s = %d", a, n)
		}
		t.Fatalf("ledger: %d unbalanced transactions, %d accounts with money left for the order", unbalanced, open)
	}
	// The same coupon and the same card work again for a new order.
	body["items"] = []m{{"sku": "S2", "qty": 1}}
	if st, again := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "reversion-2"); st != 201 || again["discounts"].(float64) != 1000 {
		t.Fatalf("buying again after the reversion = %d %v", st, again["detail"])
	}
	// Cancelling twice changes nothing.
	if st, _ := e.do("POST", "/v1/orders/"+id+"/cancel", e.adminA, m{"reason": "otra vez"}); st == 200 {
		t.Fatal("an order was cancelled twice")
	}
}
