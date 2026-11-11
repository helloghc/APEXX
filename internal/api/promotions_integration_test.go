package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/promostore"
)

// Test catalog (see setup): S1, S2, S3 at $100.00, 10 units each, 500 g.
// Channel mx: shipping $99 + $15/kg, free from $1,499.

type m = map[string]any

func items(pairs ...any) []m {
	var out []m
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, m{"sku": pairs[i], "qty": pairs[i+1]})
	}
	return out
}

func (e *env) mustPromo(body m) string {
	e.t.Helper()
	c, r := e.do("POST", "/v1/promotions", e.adminA, body)
	if c != 201 {
		e.t.Fatalf("create promotion: %d %v", c, r)
	}
	e.drain()
	return r["id"].(string)
}

func (e *env) mustCoupon(code, promotion string, extra m) {
	e.t.Helper()
	body := m{"code": code, "promotionId": promotion}
	for k, v := range extra {
		body[k] = v
	}
	if c, r := e.do("POST", "/v1/coupons", e.adminA, body); c != 201 {
		e.t.Fatalf("create coupon %s: %d %v", code, c, r)
	}
}

func (e *env) simulate(body m) m {
	e.t.Helper()
	e.drain()
	c, r := e.do("POST", "/v1/promotions/simulate", e.adminA, body)
	if c != 200 {
		e.t.Fatalf("simulate: %d %v", c, r)
	}
	return r["result"].(m)
}

func promoResult(t *testing.T, sim m, id string) m {
	t.Helper()
	for _, p := range sim["promotions"].([]any) {
		if p.(m)["id"] == id {
			return p.(m)
		}
	}
	t.Fatalf("promotion %s not explained in %v", id, sim["promotions"])
	return nil
}

func amount(v any) float64 {
	if v == nil {
		return 0
	}
	return v.(m)["amount"].(float64)
}

func (e *env) promoUses(id string) (uses, discount float64) {
	e.t.Helper()
	c, r := e.do("GET", "/v1/promotions/"+id, e.adminA, nil)
	if c != 200 {
		e.t.Fatalf("get promotion %s: %d %v", id, c, r)
	}
	p := r["promotion"].(m)
	return p["uses"].(float64), p["discountTotal"].(float64)
}

func TestPromotionTypesExplainedEndToEnd(t *testing.T) {
	e := setup(t)
	ids := []string{
		e.mustPromo(m{"id": "pct", "name": "10% desde $250", "type": "order_percent", "bps": 1000, "conditions": m{"minSubtotal": 25000}}),
		e.mustPromo(m{"id": "s1-fijo", "name": "$10 menos en Producto 1", "type": "item_fixed", "amount": 1000, "target": m{"productIds": []string{"P1"}}}),
		e.mustPromo(m{"id": "tresxdos", "name": "3x2 en Producto 2", "type": "buy_x_get_y", "buyQty": 2, "getQty": 1, "target": m{"skus": []string{"S2"}}}),
		e.mustPromo(m{"id": "escalas", "name": "Escalonado", "type": "tiered", "tierBy": "quantity", "tiers": []m{{"min": 20, "bps": 500}}}),
		e.mustPromo(m{"id": "regalo", "name": "Regalo S3", "type": "gift", "giftSku": "S3", "conditions": m{"minSubtotal": 20000}}),
		e.mustPromo(m{"id": "paquete", "name": "Paquete 1+2", "type": "bundle", "bundleItems": []m{{"sku": "S1", "qty": 1}, {"sku": "S2", "qty": 1}}, "bundlePrice": 18000,
			"status": "paused"}),
		e.mustPromo(m{"id": "envio", "name": "Envío gratis desde $200", "type": "free_shipping", "conditions": m{"minSubtotal": 20000}}),
		e.mustPromo(m{"id": "futura", "name": "Futura", "type": "item_percent", "bps": 500, "conditions": m{"startsAt": time.Now().Add(48 * time.Hour).Format(time.RFC3339)}}),
	}
	// Legacy body of phase 0 still works.
	if c, r := e.do("PUT", "/v1/promotions/legacy", e.adminA, m{"kind": "percent_over_threshold", "active": true,
		"rule": m{"name": "Legacy 5%", "bps": 500, "min": m{"amount": 9000000, "currency": "MXN"}}}); c != 200 {
		t.Fatalf("legacy put: %d %v", c, r)
	}
	ids = append(ids, "legacy")

	sim := e.simulate(m{"channel": "mx", "postalCode": "53129", "items": items("S1", 1, "S2", 3)})
	// Every promotion is explained, applied or not.
	for _, id := range ids {
		p := promoResult(t, sim, id)
		if p["reason"] == "" || p["code"] == "" {
			t.Fatalf("%s has no explanation: %v", id, p)
		}
	}
	want := func(id string, applied bool, code string, disc float64) {
		t.Helper()
		p := promoResult(t, sim, id)
		if p["applied"] != applied || p["code"] != code || amount(p["discount"]) != disc {
			t.Fatalf("%s: %v", id, p)
		}
	}
	want("tresxdos", true, "applied", 10000) // cheapest of 3 free
	want("s1-fijo", true, "applied", 1000)
	want("pct", true, "applied", 2900) // 10% of what is left: 400 - 100 - 10 = 290
	want("regalo", true, "applied", 0)
	want("escalas", false, "min_quantity", 0)
	want("paquete", false, "paused", 0)
	want("futura", false, "not_started", 0)
	want("legacy", false, "min_subtotal", 0)
	want("envio", true, "applied", 0)
	tot := sim["totals"].(m)
	if amount(tot["items"]) != 40000 || amount(tot["discounts"]) != 13900 || amount(tot["shipping"]) != 0 || amount(tot["grand"]) != 26100 {
		t.Fatalf("totals: %v", tot)
	}
	if amount(tot["shippingDiscount"]) == 0 {
		t.Fatalf("free shipping not reported: %v", tot)
	}
	// The gift was added as a free line.
	lines := sim["lines"].([]any)
	last := lines[len(lines)-1].(m)
	if last["sku"] != "S3" || last["gift"] != true || amount(last["total"]) != 0 {
		t.Fatalf("gift line: %v", last)
	}

	// Shoppers do not see back-office outcomes (paused, future), only what applies or what they can reach.
	c, pub := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": items("S1", 1, "S2", 3)})
	if c != 200 {
		t.Fatalf("public simulate %d %v", c, pub)
	}
	for _, p := range pub["promotions"].([]any) {
		if id := p.(m)["id"]; id == "paquete" || id == "futura" {
			t.Fatalf("shopper saw %v", p)
		}
	}
	if amount(pub["totals"].(m)["grand"]) != 26100 {
		t.Fatalf("storefront and admin disagree: %v", pub["totals"])
	}

	// List with filters, counts and plain-language summaries.
	_, list := e.do("GET", "/v1/promotions?status=activa&sort=name", e.adminA, nil)
	if list["total"].(float64) != 7 {
		t.Fatalf("active promotions: %v", list["total"])
	}
	counts := list["counts"].(m)
	if counts["pausada"].(float64) != 1 || counts["programada"].(float64) != 1 {
		t.Fatalf("counts: %v", counts)
	}
	for _, it := range list["items"].([]any) {
		if it.(m)["summary"] == "" || it.(m)["displayStatus"] != "activa" {
			t.Fatalf("list item: %v", it)
		}
	}
	// Validation errors point at fields; unknown fields are rejected.
	if c, r := e.do("POST", "/v1/promotions", e.adminA, m{"name": "mala", "type": "order_percent", "bps": 20000}); c != 400 || r["code"] != "validation_failed" {
		t.Fatalf("invalid promotion accepted: %d %v", c, r)
	}
	if c, _ := e.do("POST", "/v1/promotions", e.adminA, m{"name": "x", "type": "order_percent", "bps": 100, "nope": 1}); c != 400 {
		t.Fatalf("unknown field accepted: %d", c)
	}
	// Tenant isolation and permissions.
	if c, _ := e.do("GET", "/v1/promotions/pct", e.adminB, nil); c != 404 {
		t.Fatalf("store-b read store-a promotion: %d", c)
	}
	if c, _ := e.do("POST", "/v1/promotions", e.sfA, m{"name": "x", "type": "order_percent", "bps": 100}); c != 403 {
		t.Fatalf("storefront key created a promotion: %d", c)
	}
	// Preview of an unsaved definition: summary + behaviour on a test cart.
	c, pv := e.do("POST", "/v1/promotions/preview", e.adminA, m{
		"promotion": m{"name": "Borrador", "type": "item_percent", "bps": 2000, "status": "draft", "target": m{"skus": []string{"S1"}}, "conditions": m{"requiresCoupon": true}},
		"cart":      m{"channel": "mx", "items": items("S1", 2)}})
	if c != 200 || !strings.Contains(pv["summary"].(string), "20%") || pv["result"].(m)["applied"] != true {
		t.Fatalf("preview: %d %v", c, pv)
	}
	// Pause in bulk, then it stops applying (event applied per promotion).
	if c, r := e.do("POST", "/v1/promotions/bulk", e.adminA, m{"ids": []string{"pct", "s1-fijo"}, "action": "pause"}); c != 200 || r["changed"].(float64) != 2 {
		t.Fatalf("bulk pause: %d %v", c, r)
	}
	sim = e.simulate(m{"channel": "mx", "items": items("S1", 3)})
	if p := promoResult(t, sim, "pct"); p["code"] != "paused" {
		t.Fatalf("paused promotion still applies: %v", p)
	}
}

func TestCouponInvalidReasonsNeverShowAsApplied(t *testing.T) {
	e := setup(t)
	e.mustPromo(m{"id": "cupon10", "name": "10% con cupón", "type": "order_percent", "bps": 1000, "conditions": m{"requiresCoupon": true, "minSubtotal": 30000}})
	e.mustPromo(m{"id": "solo-s3", "name": "$50 en Producto 3", "type": "item_fixed", "amount": 5000, "target": m{"skus": []string{"S3"}}, "conditions": m{"requiresCoupon": true}})
	e.mustCoupon("hola10", "cupon10", nil)
	e.mustCoupon("VENCIDO", "cupon10", m{"endsAt": time.Now().Add(-time.Hour).Format(time.RFC3339)})
	e.mustCoupon("UNICO", "cupon10", m{"maxUses": 1})
	e.mustCoupon("TRES", "solo-s3", nil)
	if c, _ := e.do("POST", "/v1/coupons", e.adminA, m{"code": "HOLA10", "promotionId": "cupon10"}); c != 409 {
		t.Fatalf("duplicate code accepted: %d", c)
	}
	if c, _ := e.do("POST", "/v1/coupons", e.adminA, m{"code": "a b", "promotionId": "cupon10"}); c != 400 {
		t.Fatalf("bad code accepted: %d", c)
	}

	try := func(code string, cart []m) (m, m) {
		t.Helper()
		c, r := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": cart, "coupons": []string{code}})
		if c != 200 {
			t.Fatalf("simulate with %s: %d %v", code, c, r)
		}
		cs := r["coupons"].([]any)
		if len(cs) != 1 {
			t.Fatalf("coupon results: %v", cs)
		}
		return cs[0].(m), r
	}
	small, big := items("S1", 2), items("S1", 4)
	cases := []struct {
		code, reasonCode, text string
		cart                   []m
	}{
		{"NOEXISTE", "coupon_not_found", "no existe", big},
		{"VENCIDO", "coupon_expired", "venció", big},
		{"HOLA10", "min_subtotal", "$100", small}, // has $200, needs $300
		{"TRES", "no_matching_items", "participantes", big},
	}
	for _, c := range cases {
		cp, r := try(c.code, c.cart)
		if cp["applied"] != false || cp["reasonCode"] != c.reasonCode || !strings.Contains(cp["reason"].(string), c.text) {
			t.Fatalf("%s: %v", c.code, cp)
		}
		if amount(r["totals"].(m)["discounts"]) != 0 {
			t.Fatalf("%s gave a discount while invalid: %v", c.code, r["totals"])
		}
	}
	cp, _ := try("HOLA10", small)
	if amount(cp["missing"]) != 10000 {
		t.Fatalf("missing amount: %v", cp)
	}
	// Valid: applied, with the discount.
	cp, r := try(" hola10 ", big)
	if cp["applied"] != true || amount(cp["discount"]) != 4000 || amount(r["totals"].(m)["discounts"]) != 4000 {
		t.Fatalf("valid coupon: %v %v", cp, r["totals"])
	}

	// Usage limit: after one paid order the code is exhausted, with a clear reason.
	buy := func(key, code, email string, cart []m) (int, m) {
		return e.do("POST", "/s/store-a/api/checkout", "", m{"channel": "mx", "postalCode": "53129", "items": cart, "coupons": []string{code},
			"customer": m{"email": email, "name": "Ana"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", key)
	}
	if c, o := buy("coupon-key-0001", "UNICO", "ana@example.com", big); c != 201 || o["discounts"].(float64) != 4000 {
		t.Fatalf("checkout with coupon: %d %v", c, o)
	}
	e.drain()
	cp, _ = try("UNICO", big)
	if cp["applied"] != false || cp["reasonCode"] != "coupon_usage_limit_reached" || !strings.Contains(cp["reason"].(string), "límite de usos") {
		t.Fatalf("exhausted coupon: %v", cp)
	}
	// Checkout with an invalid coupon is refused with the reason: nobody pays an unexpected total.
	c, o := buy("coupon-key-0002", "UNICO", "luis@example.com", big)
	if c != 409 || o["code"] != "coupon_invalid" || !strings.Contains(o["detail"].(string), "límite de usos") {
		t.Fatalf("checkout with exhausted coupon: %d %v", c, o)
	}
	if av := e.available("S1"); av != 6 {
		t.Fatalf("stock after refused checkout: %v", av)
	}
}

func TestRedemptionsRecordedAndReleased(t *testing.T) {
	e := setup(t)
	e.do("POST", "/v1/campaigns", e.adminA, m{"id": "buen-fin", "name": "Buen Fin", "budgetType": "amount", "budgetLimit": 1000000})
	e.mustPromo(m{"id": "auto", "name": "5% siempre", "type": "order_percent", "bps": 500, "campaignId": "buen-fin"})
	e.mustPromo(m{"id": "cup", "name": "$20 con cupón", "type": "order_fixed", "amount": 2000, "conditions": m{"requiresCoupon": true, "maxUsesPerCustomer": 1}})
	e.mustCoupon("VEINTE", "cup", m{"maxUses": 2})
	body := m{"channel": "mx", "postalCode": "53129", "items": items("S1", 2), "coupons": []string{"VEINTE"},
		"customer": m{"email": "Ana@Example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}

	c, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "red-key-0001")
	if c != 201 || o["discounts"].(float64) != 2900 { // $20 first, then 5% of the remaining $180
		t.Fatalf("checkout: %d %v", c, o)
	}
	id := o["id"].(string)
	// The same key replays the same order: no second redemption.
	if c2, o2 := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "red-key-0001"); c2 != 201 || o2["id"] != id {
		t.Fatalf("replay: %d %v", c2, o2)
	}
	e.drain()
	if u, d := e.promoUses("cup"); u != 1 || d != 2000 {
		t.Fatalf("cup uses=%v discount=%v", u, d)
	}
	if u, d := e.promoUses("auto"); u != 1 || d != 900 {
		t.Fatalf("auto uses=%v discount=%v", u, d)
	}
	_, ord := e.do("GET", "/v1/orders/"+id, e.adminA, nil)
	reds := ord["promotions"].([]any)
	if len(reds) != 2 || reds[0].(m)["status"] != "redeemed" {
		t.Fatalf("order redemptions: %v", reds)
	}
	_, camp := e.do("GET", "/v1/campaigns/buen-fin", e.adminA, nil)
	if cc := camp["campaign"].(m); cc["spent"].(float64) != 900 || cc["used"].(float64) != 1 {
		t.Fatalf("campaign counters: %v", cc)
	}
	// Personal data never reaches the redemptions table in clear.
	var hash string
	if err := e.db.System.QueryRow(context.Background(), `SELECT customer_hash FROM promotion_redemptions WHERE order_id=$1 LIMIT 1`, id).Scan(&hash); err != nil || len(hash) != 64 || strings.Contains(hash, "@") {
		t.Fatalf("customer hash %q err %v", hash, err)
	}

	// Same customer (different capitalisation), second purchase: the per-customer limit says why.
	_, sim := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "items": items("S1", 1), "coupons": []string{"VEINTE"}, "email": "ana@example.com"})
	if cp := sim["coupons"].([]any)[0].(m); cp["applied"] != false || cp["reasonCode"] != "customer_limit_reached" {
		t.Fatalf("per-customer limit: %v", cp)
	}
	if c, r := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "red-key-0002"); c != 409 || r["code"] != "coupon_invalid" {
		t.Fatalf("second purchase with coupon: %d %v", c, r)
	}

	// Refund releases every use, inside the order transaction.
	if c, r := e.do("POST", "/v1/orders/"+id+"/cancel", e.adminA, m{"reason": "prueba"}); c != 200 || r["status"] != "refunded" {
		t.Fatalf("cancel: %d %v", c, r)
	}
	e.drain()
	if u, d := e.promoUses("cup"); u != 0 || d != 0 {
		t.Fatalf("after refund cup uses=%v discount=%v", u, d)
	}
	_, ord = e.do("GET", "/v1/orders/"+id, e.adminA, nil)
	for _, r := range ord["promotions"].([]any) {
		if r.(m)["status"] != "released" {
			t.Fatalf("redemption not released: %v", r)
		}
	}
	_, camp = e.do("GET", "/v1/campaigns/buen-fin", e.adminA, nil)
	if cc := camp["campaign"].(m); cc["spent"].(float64) != 0 || cc["used"].(float64) != 0 {
		t.Fatalf("campaign after refund: %v", cc)
	}
	// Cancelling again changes nothing (idempotent release).
	e.do("POST", "/v1/orders/"+id+"/cancel", e.adminA, m{"reason": "otra vez"})
	if u, _ := e.promoUses("auto"); u != 0 {
		t.Fatalf("double release: %v", u)
	}
	// The customer can use the coupon again after the refund.
	if c, r := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "red-key-0003"); c != 201 {
		t.Fatalf("reuse after refund: %d %v", c, r)
	}
	// A declined payment releases the reservation too.
	body2 := m{"channel": "mx", "postalCode": "53129", "items": items("S2", 1), "coupons": []string{"VEINTE"},
		"customer": m{"email": "luis@example.com", "name": "Luis"}, "paymentMethod": "pm_card_chargeDeclined"}
	if c, r := e.do("POST", "/v1/checkout", e.sfA, body2, "Idempotency-Key", "red-key-0004"); c != 402 {
		t.Fatalf("declined: %d %v", c, r)
	}
	e.drain()
	if u, _ := e.promoUses("cup"); u != 1 {
		t.Fatalf("declined payment kept a use: %v", u)
	}
	// The total the shopper saw is enforced.
	body2["paymentMethod"], body2["expectedTotal"] = "pm_card_visa", 1
	if c, r := e.do("POST", "/v1/checkout", e.sfA, body2, "Idempotency-Key", "red-key-0005"); c != 409 || r["code"] != "total_changed" {
		t.Fatalf("expected total ignored: %d %v", c, r)
	}
}

func TestLastUseUnderConcurrency(t *testing.T) {
	e := setup(t)
	e.mustPromo(m{"id": "ultimo", "name": "Solo 1 uso", "type": "order_fixed", "amount": 1000, "conditions": m{"maxUses": 1}})
	e.mustPromo(m{"id": "porcliente", "name": "1 por cliente", "type": "order_fixed", "amount": 500, "conditions": m{"maxUsesPerCustomer": 1}})
	ctx := context.Background()

	// 1. Deterministic: N transactions race to reserve the same last use.
	race := func(promotion, hash string, n int) (ok, limited int) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				err := e.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
					return promostore.ReserveTx(ctx, tx, "store-a", fmt.Sprintf("ord_race_%s_%d", promotion, i), hash, "MXN",
						[]promostore.RedemptionReq{{PromotionID: promotion, Name: promotion, Amount: 1000}})
				})
				mu.Lock()
				defer mu.Unlock()
				var lim *promostore.LimitError
				switch {
				case err == nil:
					ok++
				case errors.As(err, &lim):
					limited++
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		return
	}
	if ok, lim := race("ultimo", "h1", 8); ok != 1 || lim != 7 {
		t.Fatalf("last total use: %d got it, %d refused", ok, lim)
	}
	if ok, lim := race("porcliente", "same-customer", 8); ok != 1 || lim != 7 {
		t.Fatalf("last use per customer: %d got it, %d refused", ok, lim)
	}
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM promotion_redemptions WHERE promotion_id IN ('ultimo','porcliente') AND status='reserved'`).Scan(&n)
	if n != 2 {
		t.Fatalf("redemptions recorded: %d", n)
	}
	// Free both again for the end-to-end race.
	for _, p := range []string{"ultimo", "porcliente"} {
		for i := 0; i < 8; i++ {
			_ = e.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
				return promostore.ReleaseTx(ctx, tx, "store-a", fmt.Sprintf("ord_race_%s_%d", p, i))
			})
		}
	}
	e.drain()
	if u, _ := e.promoUses("ultimo"); u != 0 {
		t.Fatalf("uses after release: %v", u)
	}

	// 2. End to end: two shoppers pay at the same time for the last use.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	bodies := make([]m, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], bodies[i] = e.do("POST", "/s/store-a/api/checkout", "", m{"channel": "mx", "postalCode": "53129", "items": items("S1", 1),
				"customer": m{"email": "misma@example.com", "name": "Misma"}, "paymentMethod": "pm_card_visa", "expectedTotal": 10000 - 1500 + 9900 + 1500},
				"Idempotency-Key", fmt.Sprintf("race-key-%04d", i))
		}(i)
	}
	wg.Wait()
	created := 0
	for i, c := range codes {
		switch c {
		case 201:
			created++
			if bodies[i]["discounts"].(float64) != 1500 {
				t.Fatalf("winner paid %v", bodies[i])
			}
		case 409: // promotion_unavailable (lost the lock) or total_changed (saw the other order first)
			if code := bodies[i]["code"]; code != "promotion_unavailable" && code != "total_changed" {
				t.Fatalf("loser got %v", bodies[i])
			}
		default:
			t.Fatalf("checkout %d: %d %v", i, c, bodies[i])
		}
	}
	if created != 1 {
		t.Fatalf("%d orders got the last use (statuses %v)", created, codes)
	}
	e.drain()
	if u, _ := e.promoUses("ultimo"); u != 1 {
		t.Fatalf("uses after race: %v", u)
	}
	// The loser's stock reservation was rolled back with its order.
	if av := e.available("S1"); av != 9 {
		t.Fatalf("available after race: %v", av)
	}
	// And the simulation now explains the limit to the admin.
	sim := e.simulate(m{"channel": "mx", "items": items("S1", 1), "email": "misma@example.com"})
	if p := promoResult(t, sim, "ultimo"); p["code"] != "usage_limit_reached" {
		t.Fatalf("limit not explained: %v", p)
	}
	if p := promoResult(t, sim, "porcliente"); p["code"] != "customer_limit_reached" {
		t.Fatalf("customer limit not explained: %v", p)
	}
}

func TestCouponBatchesAndExport(t *testing.T) {
	e := setup(t)
	e.mustPromo(m{"id": "lote", "name": "=Lote", "type": "order_percent", "bps": 500, "conditions": m{"requiresCoupon": true}})
	c, b := e.do("POST", "/v1/coupons/generate", e.adminA, m{"promotionId": "lote", "prefix": "bf-", "length": 8, "quantity": 300, "maxUses": 1})
	if c != 201 || b["quantity"].(float64) != 300 {
		t.Fatalf("generate: %d %v", c, b)
	}
	batch := b["id"].(string)
	seen := map[string]bool{}
	cursor := ""
	for {
		_, l := e.do("GET", "/v1/coupons?batch="+batch+"&limit=120&cursor="+cursor, e.adminA, nil)
		if l["total"].(float64) != 300 {
			t.Fatalf("total: %v", l["total"])
		}
		for _, it := range l["items"].([]any) {
			code := it.(m)["code"].(string)
			if !strings.HasPrefix(code, "BF-") || len(code) != 11 || seen[code] {
				t.Fatalf("bad or repeated code %q", code)
			}
			seen[code] = true
		}
		cursor, _ = l["nextCursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(seen) != 300 {
		t.Fatalf("listed %d unique codes", len(seen))
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/coupons/export?batch="+batch, nil)
	req.Header.Set("Authorization", "Bearer "+e.adminA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || len(rows) != 301 || !strings.Contains(rows[0], "codigo,promocion") {
		t.Fatalf("export: %d %s rows=%d", resp.StatusCode, resp.Header.Get("Content-Type"), len(rows))
	}
	if !strings.Contains(rows[1], "'=Lote") {
		t.Fatalf("formula cell not neutralised: %s", rows[1])
	}
	// One of the generated codes works in a cart; disabling it stops it.
	var code string
	for k := range seen {
		code = k
		break
	}
	e.drain()
	_, sim := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "items": items("S1", 1), "coupons": []string{code}})
	if cp := sim["coupons"].([]any)[0].(m); cp["applied"] != true {
		t.Fatalf("generated code: %v", cp)
	}
	if c, r := e.do("POST", "/v1/coupons/bulk", e.adminA, m{"codes": []string{code}, "action": "disable"}); c != 200 || r["changed"].(float64) != 1 {
		t.Fatalf("disable: %d %v", c, r)
	}
	_, sim = e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "items": items("S1", 1), "coupons": []string{code}})
	if cp := sim["coupons"].([]any)[0].(m); cp["applied"] != false || cp["reasonCode"] != "coupon_disabled" {
		t.Fatalf("disabled code: %v", cp)
	}
	if c, _ := e.do("GET", "/v1/coupons?batch="+batch, e.adminB, nil); c != 200 {
		t.Fatal("store-b list failed")
	} else if _, l := e.do("GET", "/v1/coupons?batch="+batch, e.adminB, nil); l["total"].(float64) != 0 {
		t.Fatalf("store-b sees store-a coupons: %v", l["total"])
	}
}

func TestPriceListsBulkChangeAndHistory(t *testing.T) {
	e := setup(t)
	price := func(sku string) (unit float64, list any) {
		e.drain()
		_, sim := e.do("POST", "/v1/carts/simulate", e.adminA, m{"channel": "mx", "items": items(sku, 1)})
		l := sim["lines"].([]any)[0].(m)
		return amount(l["unit"]), l["priceList"]
	}
	// Scheduled override with priority over a catalogue-wide adjustment.
	if c, r := e.do("POST", "/v1/price-lists", e.adminA, m{"id": "todo-10", "name": "Todo -10%", "kind": "adjust", "adjustBps": -1000, "appliesTo": "all"}); c != 201 {
		t.Fatalf("adjust list: %d %v", c, r)
	}
	if u, l := price("S1"); u != 9000 || l != "Todo -10%" {
		t.Fatalf("adjust list not applied: %v %v", u, l)
	}
	if c, r := e.do("POST", "/v1/price-lists", e.adminA, m{"id": "s1-especial", "name": "S1 especial", "kind": "override", "priority": 10,
		"items": []m{{"sku": "S1", "amount": 7500}}}); c != 201 {
		t.Fatalf("override list: %d %v", c, r)
	}
	if u, _ := price("S1"); u != 7500 {
		t.Fatalf("override not applied: %v", u)
	}
	if u, _ := price("S2"); u != 9000 {
		t.Fatalf("S2: %v", u)
	}
	// A list scheduled for the future does not apply yet; one for another channel never does here.
	future := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	e.do("PUT", "/v1/price-lists/s1-especial", e.adminA, m{"name": "S1 especial", "kind": "override", "priority": 10, "startsAt": future, "items": []m{{"sku": "S1", "amount": 7500}}})
	if u, _ := price("S1"); u != 9000 {
		t.Fatalf("future list applied: %v", u)
	}
	_, ls := e.do("GET", "/v1/price-lists", e.adminA, nil)
	if first := ls["items"].([]any)[0].(m); first["displayStatus"] != "programada" || first["summary"] == "" {
		t.Fatalf("list view: %v", first)
	}
	if c, _ := e.do("POST", "/v1/price-lists", e.adminA, m{"name": "mala", "kind": "override", "items": []m{{"sku": "NOPE", "amount": 1}}}); c != 400 {
		t.Fatalf("unknown SKU accepted: %d", c)
	}
	e.do("DELETE", "/v1/price-lists/todo-10", e.adminA, nil)
	e.do("DELETE", "/v1/price-lists/s1-especial", e.adminA, nil)
	if u, l := price("S1"); u != 10000 || l != nil {
		t.Fatalf("lists not removed: %v %v", u, l)
	}

	// Bulk change: preview shows the diff and changes nothing.
	change := m{"channel": "mx", "mode": "percent", "value": -1550, "rounding": "peso", "compareAt": "previous", "scope": m{"skus": []string{"S1", "S2"}}}
	c, pv := e.do("POST", "/v1/pricing/bulk/preview", e.adminA, change)
	if c != 200 || pv["changed"].(float64) != 2 || pv["applied"] != false {
		t.Fatalf("preview: %d %v", c, pv)
	}
	row := pv["rows"].([]any)[0].(m)
	if row["old"].(float64) != 10000 || row["new"].(float64) != 8500 { // 84.50 rounded to the peso
		t.Fatalf("diff row: %v", row)
	}
	if u, _ := price("S1"); u != 10000 {
		t.Fatalf("preview changed prices: %v", u)
	}
	// Applying needs the preview fingerprint; a stale one is refused.
	change["hash"] = strings.Repeat("0", 64)
	if c, r := e.do("POST", "/v1/pricing/bulk/apply", e.adminA, change); c != 409 || r["code"] != "prices_changed" {
		t.Fatalf("stale apply: %d %v", c, r)
	}
	change["hash"] = pv["hash"]
	c, ap := e.do("POST", "/v1/pricing/bulk/apply", e.adminA, change)
	if c != 200 || ap["applied"] != true || ap["id"] == "" {
		t.Fatalf("apply: %d %v", c, ap)
	}
	if u, _ := price("S1"); u != 8500 {
		t.Fatalf("bulk change not applied: %v", u)
	}
	if u, _ := price("S3"); u != 10000 {
		t.Fatalf("S3 out of scope changed: %v", u)
	}
	// History per SKU: the first price, then the bulk change with who and which operation.
	_, h := e.do("GET", "/v1/pricing/history?sku=S1", e.adminA, nil)
	hist := h["items"].([]any)
	if len(hist) != 2 {
		t.Fatalf("history: %v", hist)
	}
	top := hist[0].(m)
	if top["old"].(float64) != 10000 || top["new"].(float64) != 8500 || top["source"] != "bulk" || top["ref"] != ap["id"] || top["actor"] == "" {
		t.Fatalf("history entry: %v", top)
	}
	if top["newCompareAt"].(float64) != 12000 { // the higher of old price and old compare-at
		t.Fatalf("compare-at: %v", top)
	}
	// A manual price change through the existing API lands in the history too.
	e.do("PUT", "/v1/prices/mx/S1", e.adminA, m{"tiers": []m{{"minQty": 1, "unit": m{"amount": 9900, "currency": "MXN"}}}})
	_, h = e.do("GET", "/v1/pricing/history?sku=S1", e.adminA, nil)
	if top := h["items"].([]any)[0].(m); len(h["items"].([]any)) != 3 || top["new"].(float64) != 9900 || top["source"] != "manual" {
		t.Fatalf("manual change not recorded: %v", h["items"])
	}
	// History is append-only and isolated per tenant.
	if _, err := e.db.System.Exec(context.Background(), `UPDATE price_history SET new_amount=1`); err == nil {
		t.Fatal("price history was modified")
	}
	if _, hb := e.do("GET", "/v1/pricing/history?sku=S1", e.adminB, nil); len(hb["items"].([]any)) != 0 {
		t.Fatalf("store-b sees store-a history")
	}
}
