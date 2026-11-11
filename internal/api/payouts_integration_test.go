package api

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// Automatic payouts: the money leaves the statement once, reaches the seller
// once, and comes back when the provider rejects the transfer.
func TestSellerAutomaticPayouts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("POST", "/v1/sellers", e.adminA, m{"name": "Sin cuenta", "payoutAuto": true}); st != 400 {
		t.Fatalf("automatic payouts without an account = %d", st)
	}
	sel := e.must(201, "POST", "/v1/sellers", e.adminA, m{"name": "Artesanías Luna", "commissionBps": 2000, "payoutAccount": "acct_luna", "payoutHoldDays": 7})
	id := sel["id"].(string)
	e.must(204, "PUT", "/v1/sellers/"+id+"/products", e.adminA, m{"productIds": []string{"P1"}})
	e.drain()
	seller := func() map[string]any {
		t.Helper()
		return e.must(200, "GET", "/v1/sellers/"+id, e.adminA, nil)["seller"].(map[string]any)
	}
	save := func(extra m) {
		t.Helper()
		body := m{"name": "Artesanías Luna", "commissionBps": 2000, "payoutAccount": "acct_luna", "payoutHoldDays": 0}
		for k, v := range extra {
			body[k] = v
		}
		e.must(200, "PUT", "/v1/sellers/"+id, e.adminA, body)
	}
	order := func(key string) (string, float64) {
		t.Helper()
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}},
			"customer": m{"email": "comprador@example.com", "name": "Carla Compradora", "address": "Calle 1"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", key)
		if o["id"] == nil {
			t.Fatalf("checkout %s = %d %v", key, st, o)
		}
		return o["id"].(string), o["grandTotal"].(float64)
	}
	orderID, grand := order("payout-key-1")

	// The sale is too recent to pay: it waits its 7 days.
	if s := seller(); s["balance"].(float64) != 16000 || s["payable"].(float64) != 0 {
		t.Fatalf("recent sale: balance=%v payable=%v", s["balance"], s["payable"])
	}
	if st, b := e.do("POST", "/v1/sellers/"+id+"/payouts", e.adminA, m{}); st != 409 || b["code"] != "nothing_to_pay" {
		t.Fatalf("paying a sale on hold = %d %v", st, b)
	}
	if n, _ := e.orders.RunPayouts(ctx); n != 0 {
		t.Fatalf("the round paid a sale on hold: %d", n)
	}

	// Without the wait it can be paid; more than what is owed cannot.
	save(nil)
	if s := seller(); s["payable"].(float64) != 16000 {
		t.Fatalf("payable = %v", s["payable"])
	}
	if st, _ := e.do("POST", "/v1/sellers/"+id+"/payouts", e.adminA, m{"amount": 16001}); st != 400 {
		t.Fatalf("payout above the payable amount = %d", st)
	}
	if st, _ := e.do("POST", "/v1/sellers/"+id+"/payouts", e.sfA, m{}); st != 403 && st != 401 {
		t.Fatalf("the storefront key started a payout: %d", st)
	}

	// The provider is down: the money is taken from the statement and the
	// payout waits; the next round sends it, once.
	e.sandbox.FailNext = errors.New("timeout")
	p := e.must(201, "POST", "/v1/sellers/"+id+"/payouts", e.adminA, m{"amount": 6000})
	if p["status"] != "pending" || seller()["balance"].(float64) != 10000 || e.sandbox.Transferred() != 0 {
		t.Fatalf("provider down: %v balance=%v sent=%d", p["status"], seller()["balance"], e.sandbox.Transferred())
	}
	if st, _ := e.do("POST", "/v1/sellers/"+id+"/payouts", e.adminA, m{}); st != 400 {
		t.Fatalf("a second payout while one is in flight = %d", st)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE seller_payouts SET created_at = now() - interval '5 minutes'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // running the round again must not send it again
		if _, err := e.orders.RunPayouts(ctx); err != nil {
			t.Fatal(err)
		}
	}
	list := e.must(200, "GET", "/v1/sellers/"+id+"/payouts", e.adminA, nil)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["status"] != "paid" || e.sandbox.Transferred() != 6000 || seller()["balance"].(float64) != 10000 {
		t.Fatalf("after the round: %v sent=%d balance=%v", list, e.sandbox.Transferred(), seller()["balance"])
	}

	// Two people press "pay" at once: one payout, not two.
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = e.do("POST", "/v1/sellers/"+id+"/payouts", e.adminA, m{})
		}()
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == 201 {
			ok++
		}
	}
	if ok != 1 || e.sandbox.Transferred() != 16000 || seller()["balance"].(float64) != 0 {
		t.Fatalf("simultaneous payouts: %v sent=%d balance=%v", codes, e.sandbox.Transferred(), seller()["balance"])
	}

	// A return after the payout leaves the seller owing; nothing more is sent
	// until new sales cover it.
	e.must(201, "POST", "/v1/orders/"+orderID+"/refunds", e.adminA, m{"amount": grand, "reason": "devolución", "notify": false})
	if b := seller()["balance"].(float64); b != -16000 {
		t.Fatalf("balance after a late return = %v", b)
	}
	save(m{"payoutAuto": true})
	order("payout-key-2")
	if n, _ := e.orders.RunPayouts(ctx); n != 0 || e.sandbox.Transferred() != 16000 {
		t.Fatalf("paid a seller that still owes: n=%d sent=%d", n, e.sandbox.Transferred())
	}
	order("payout-key-3")
	order("payout-key-4") // three sales of 16000 against 16000 owed: 32000 to pay
	if n, _ := e.orders.RunPayouts(ctx); n != 1 || e.sandbox.Transferred() != 48000 || seller()["balance"].(float64) != 0 {
		t.Fatalf("automatic round: n=%d sent=%d balance=%v", n, e.sandbox.Transferred(), seller()["balance"])
	}

	// An account the provider rejects: the payout fails and the money returns.
	bad := e.must(201, "POST", "/v1/sellers", e.adminA, m{"name": "Cuenta mala", "commissionBps": 0, "payoutAccount": "acct_fail", "payoutHoldDays": 0})["id"].(string)
	e.must(204, "POST", "/v1/sellers/"+bad+"/entries", e.adminA, m{"kind": "adjust", "amount": 5000, "note": "saldo inicial"})
	fp := e.must(201, "POST", "/v1/sellers/"+bad+"/payouts", e.adminA, m{})
	b := e.must(200, "GET", "/v1/sellers/"+bad, e.adminA, nil)["seller"].(map[string]any)["balance"].(float64)
	if fp["status"] != "failed" || b != 5000 || e.sandbox.Transferred() != 48000 {
		t.Fatalf("rejected transfer: %v balance=%v sent=%d", fp["status"], b, e.sandbox.Transferred())
	}

	// Another store sees none of it.
	if st, body := e.do("GET", "/v1/sellers/"+id+"/payouts", e.adminB, nil); st == 200 && len(body["items"].([]any)) != 0 {
		t.Fatalf("another store read the payouts: %v", body)
	}
	if st, _ := e.do("POST", "/v1/sellers/"+id+"/payouts", e.adminB, m{}); st != 404 {
		t.Fatalf("another store paid the seller: %d", st)
	}
	// The books add up: every peso that left the statement was sent or returned.
	var entries, paid int64
	if err := e.db.System.QueryRow(ctx, `SELECT COALESCE(-sum(net),0) FROM seller_entries WHERE kind IN ('payout','payout_failed') AND payout_id IS NOT NULL`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if err := e.db.System.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM seller_payouts WHERE status='paid'`).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if entries != paid || paid != e.sandbox.Transferred() {
		t.Fatalf("statement %d, payouts %d, provider %d", entries, paid, e.sandbox.Transferred())
	}
}
