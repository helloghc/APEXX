package api

import (
	"context"
	"fmt"
	"testing"
)

// Gift cards pay like money: they are spent atomically, come back when the
// order fails or is refunded, never leak between stores and keep the ledger balanced.
func TestGiftCardsInstallmentsAndRisk(t *testing.T) {
	e := setup(t)
	n := 0
	checkout := func(want int, qty int, extra m) m {
		t.Helper()
		n++
		body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": qty}},
			"customer": m{"email": "ana@example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
		for k, v := range extra {
			body[k] = v
		}
		c, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", fmt.Sprintf("wallet-key-%d", n))
		if c != want {
			t.Fatalf("checkout #%d = %d %v", n, c, o)
		}
		return o
	}
	balance := func(id string) float64 {
		t.Helper()
		return e.must(200, "GET", "/v1/gift-cards/"+id, e.adminA, nil)["card"].(map[string]any)["balance"].(float64)
	}
	issue := func(amount int) (id, code string) {
		t.Helper()
		c := e.must(201, "POST", "/v1/gift-cards", e.adminA, m{"amount": amount, "recipientName": "Ana"})
		return c["id"].(string), c["code"].(string)
	}

	if st, _ := e.do("POST", "/v1/gift-cards", e.adminA, m{"amount": 5}); st != 400 {
		t.Fatalf("tiny card accepted: %d", st)
	}
	card, code := issue(50000)
	// The code works with or without dashes, only in its own store, and is not stored.
	if st, b := e.do("POST", "/s/store-a/api/giftcard", "", m{"code": code}); st != 200 || b["balance"].(map[string]any)["amount"].(float64) != 50000 {
		t.Fatalf("balance check = %d %v", st, b)
	}
	if st, b := e.do("POST", "/s/store-b/api/giftcard", "", m{"code": code}); st != 409 || b["code"] != "gift_card_unknown" {
		t.Fatalf("card of store A answered in store B: %d %v", st, b)
	}
	if st, b := e.do("POST", "/s/store-a/api/giftcard", "", m{"code": "AAAA-BBBB-CCCC-DDDD"}); st != 409 || b["code"] != "gift_card_unknown" {
		t.Fatalf("unknown code = %d %v", st, b)
	}
	var leaked int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM gift_cards WHERE encode(code_hash,'escape') LIKE '%'||$1||'%' OR last4 = $1`, code).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("the gift card code is stored in clear")
	}

	// 1. The card covers the whole order: nothing is charged to the bank card.
	calls := e.sandbox.Calls
	o1 := checkout(201, 1, m{"giftCards": []string{code}})
	g1 := o1["grandTotal"].(float64)
	if o1["status"] != "paid" || e.sandbox.Calls != calls {
		t.Fatalf("fully covered order: status=%v charged=%v", o1["status"], e.sandbox.Calls != calls)
	}
	if got := balance(card); got != 50000-g1 {
		t.Fatalf("balance after order 1 = %v, want %v", got, 50000-g1)
	}
	// 2. Partial: the card pays what it has left and the bank card the rest.
	left := balance(card)
	o2 := checkout(201, 6, m{"giftCards": []string{code}})
	g2 := o2["grandTotal"].(float64)
	if g2 <= left {
		t.Fatalf("test needs an order above the card balance: %v <= %v", g2, left)
	}
	full := e.must(200, "GET", "/v1/orders/"+o2["id"].(string), e.adminA, nil)
	pays := full["payments"].([]any)
	if len(pays) != 2 || balance(card) != 0 {
		t.Fatalf("split payment: %d payments, card balance %v", len(pays), balance(card))
	}
	var sum float64
	for _, p := range pays {
		sum += p.(map[string]any)["amount"].(float64)
	}
	if sum != g2 {
		t.Fatalf("payments add %v, order is %v", sum, g2)
	}
	// An empty card is refused with its reason and creates no order.
	if o := checkout(409, 1, m{"giftCards": []string{code}}); o["code"] != "gift_card_empty" {
		t.Fatalf("empty card: %v", o)
	}
	// 3. A declined bank card gives the gift card balance back.
	card2, code2 := issue(5000)
	checkout(402, 2, m{"giftCards": []string{code2}, "paymentMethod": "pm_card_chargeDeclined"})
	if got := balance(card2); got != 5000 {
		t.Fatalf("balance after declined payment = %v, want 5000", got)
	}
	// 4. Refunds return each part to where it came from.
	e.must(201, "POST", "/v1/orders/"+o2["id"].(string)+"/refunds", e.adminA, m{"amount": g2, "reason": "prueba", "notify": false})
	if got := balance(card); got != left {
		t.Fatalf("refund of the split order returned %v to the card, want %v", got, left)
	}
	e.must(201, "POST", "/v1/orders/"+o1["id"].(string)+"/refunds", e.adminA, m{"amount": g1, "reason": "prueba", "toCredit": true, "notify": false})
	if got := balance(card); got != 50000 {
		t.Fatalf("card after both refunds = %v, want 50000", got)
	}
	// 5. Refund as store credit: no money leaves, the customer gets a credit card.
	o3 := checkout(201, 1, nil)
	e.must(201, "POST", "/v1/orders/"+o3["id"].(string)+"/refunds", e.adminA, m{"amount": 3000, "reason": "compensación", "toCredit": true, "notify": false})
	credits := e.must(200, "GET", "/v1/gift-cards?kind=credit", e.adminA, nil)["items"].([]any)
	if len(credits) != 1 || credits[0].(map[string]any)["balance"].(float64) != 3000 {
		t.Fatalf("store credit = %v", credits)
	}
	// Disabled cards cannot pay; adjustments need a reason.
	e.must(200, "PATCH", "/v1/gift-cards/"+card2, e.adminA, m{"status": "disabled"})
	if o := checkout(409, 1, m{"giftCards": []string{code2}}); o["code"] != "gift_card_disabled" {
		t.Fatalf("disabled card: %v", o)
	}
	if st, _ := e.do("PATCH", "/v1/gift-cards/"+card2, e.adminA, m{"delta": 100}); st != 400 {
		t.Fatalf("adjustment without reason = %d", st)
	}
	if st, _ := e.do("GET", "/v1/gift-cards/"+card, e.adminB, nil); st != 404 {
		t.Fatalf("another store read the card: %d", st)
	}
	var unbalanced int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("%d ledger transactions do not balance", unbalanced)
	}
	// Card balances always equal the sum of their movements.
	var drift int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM gift_cards g WHERE balance <> (SELECT COALESCE(sum(amount),0) FROM gift_card_tx t WHERE t.tenant_id=g.tenant_id AND t.card_id=g.id)`).Scan(&drift)
	if drift != 0 {
		t.Fatalf("%d cards have a balance different from their movements", drift)
	}

	// Instalments: only the plans the store offers, from its minimum.
	e.must(200, "PUT", "/v1/payment-rules", e.adminA, m{"installments": m{"enabled": true, "plans": []int{6, 3}, "minAmount": 30000}})
	if st, _ := e.do("PUT", "/v1/payment-rules", e.adminA, m{"installments": m{"enabled": true, "plans": []int{5}}}); st != 400 {
		t.Fatalf("invalid plan accepted: %d", st)
	}
	if o := checkout(409, 3, m{"installments": 9, "items": []m{{"sku": "S2", "qty": 3}}}); o["code"] != "installments_unavailable" {
		t.Fatalf("plan not offered: %v", o)
	}
	if o := checkout(409, 1, m{"installments": 3, "items": []m{{"sku": "S2", "qty": 1}}}); o["code"] != "installments_unavailable" {
		t.Fatalf("below the minimum: %v", o)
	}
	o4 := checkout(201, 3, m{"installments": 3, "items": []m{{"sku": "S2", "qty": 3}}})
	if got := e.must(200, "GET", "/v1/orders/"+o4["id"].(string), e.adminA, nil)["installments"]; got != float64(3) {
		t.Fatalf("installments stored = %v", got)
	}

	// Risk rules: block lists refuse, thresholds flag for review.
	e.must(200, "PUT", "/v1/payment-rules", e.adminA, m{"risk": m{"enabled": true, "reviewAmount": 100, "blockedEmails": []string{"@Malo.com"}, "blockedPostalCodes": []string{"99999"}}})
	if o := checkout(403, 1, m{"customer": m{"email": "x@malo.com", "name": "X"}, "items": []m{{"sku": "S3", "qty": 1}}}); o["code"] != "order_blocked" {
		t.Fatalf("blocked domain: %v", o)
	}
	o5 := checkout(201, 1, m{"items": []m{{"sku": "S3", "qty": 1}}})
	d := e.must(200, "GET", "/v1/orders/"+o5["id"].(string), e.adminA, nil)
	if rk, _ := d["risk"].(map[string]any); rk == nil || rk["level"] != "review" {
		t.Fatalf("order above the threshold not flagged: %v", d["risk"])
	}
	if list := e.must(200, "GET", "/v1/orders?review=1", e.adminA, nil)["items"].([]any); len(list) != 1 {
		t.Fatalf("review filter lists %d orders", len(list))
	}
	e.must(204, "POST", "/v1/orders/"+o5["id"].(string)+"/risk/clear", e.adminA, nil)
	if rk := e.must(200, "GET", "/v1/orders/"+o5["id"].(string), e.adminA, nil)["risk"].(map[string]any); rk["level"] != "cleared" {
		t.Fatalf("risk after review = %v", rk)
	}
	if st, _ := e.do("POST", "/v1/orders/"+o5["id"].(string)+"/risk/clear", e.adminA, nil); st == 204 {
		t.Fatal("clearing twice must not succeed")
	}
	// The list shows the same amounts as each order's own page (it once read 0).
	for _, it := range e.must(200, "GET", "/v1/orders?limit=50", e.adminA, nil)["items"].([]any) {
		row := it.(map[string]any)
		full := e.must(200, "GET", "/v1/orders/"+row["id"].(string), e.adminA, nil)
		for _, k := range []string{"itemsTotal", "discounts", "shipping", "taxIncluded", "fees", "grandTotal", "refundedTotal", "paidTotal", "balance"} {
			if row[k] != full[k] {
				t.Errorf("order %v: list %s = %v, order page %v", row["number"], k, row[k], full[k])
			}
		}
	}
}
