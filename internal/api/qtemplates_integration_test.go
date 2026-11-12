package api

import (
	"context"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/customers"
)

// Quote templates: an agreement fixes price, quantities, minimum order and
// dates; buyers order from it without renegotiating, on credit (or with a
// payment link when the company pays in advance); its usage comes from paid
// orders and cancellations give it back; other companies cannot use it.
func TestQuoteTemplates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Acuerdo SA", "terms": "net30", "creditLimit": 5000000, "buyerEmails": []string{"compras@acuerdo.example"}})["id"].(string)
	pre := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Contado SA", "buyerEmails": []string{"compras@contado.example"}})["id"].(string)
	today := time.Now().Format("2006-01-02")
	later := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	tpl := func(company string, extra m) m {
		b := m{"companyId": company, "name": "Contrato anual", "validFrom": today, "validUntil": later, "minOrderTotal": 15000, "commitTotal": 100000,
			"items": []m{{"sku": "S1", "unit": 5000, "minQty": 2, "maxQty": 10}}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	for _, bad := range []m{tpl(co, m{"items": []m{{"sku": "S1", "unit": 5000, "minQty": 5, "maxQty": 2}}}), tpl(co, m{"items": []m{{"sku": "NOPE", "unit": 1}}}),
		tpl(co, m{"validUntil": "2020-01-01"}), tpl(co, m{"items": []m{{"sku": "S1", "unit": 0}}})} {
		if st, _ := e.do("POST", "/v1/quote-templates", e.adminA, bad); st != 400 {
			t.Fatalf("accepted %v: %d", bad["items"], st)
		}
	}
	if st, _ := e.do("POST", "/v1/quote-templates", e.adminB, tpl(co, nil)); st != 400 {
		t.Fatalf("store B made an agreement for store A's company: %d", st)
	}
	created := e.must(201, "POST", "/v1/quote-templates", e.adminA, tpl(co, nil))
	id := created["id"].(string)
	preID := e.must(201, "POST", "/v1/quote-templates", e.adminA, tpl(pre, nil))["id"].(string)
	login := func(email string) string {
		c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, c.ID)
		_ = e.server.Customers.SetPassword(ctx, "store-a", c.ID, "Una-clave-larga-2026", "")
		tok, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	tok, tokPre := login("compras@acuerdo.example"), login("compras@contado.example")
	as := func(tk, method, path string, body any) (int, m) {
		return e.do(method, path, "", body, "Cookie", "apexx_shopper="+tk, "Origin", e.srv.URL)
	}
	_, l := as(tok, "GET", "/s/store-a/api/account/company/templates", nil)
	if items := l["items"].([]any); len(items) != 1 || items[0].(map[string]any)["items"].([]any)[0].(map[string]any)["title"] == "" {
		t.Fatalf("buyer agreements = %v", l)
	}
	order := func(tk, tid string, qty int) (int, m) {
		return as(tk, "POST", "/s/store-a/api/account/company/templates/"+tid+"/order", m{"items": []m{{"sku": "S1", "qty": qty}}})
	}
	for _, q := range []int{1, 11, 2} {
		if st, r := order(tok, id, q); st != 409 || r["code"] != "agreement_rule" {
			t.Fatalf("qty %d = %d %v", q, st, r)
		}
	}
	st, r := order(tok, id, 3)
	if st != 201 {
		t.Fatalf("order from agreement = %d %v", st, r)
	}
	oid := r["order"].(map[string]any)["id"].(string)
	o := e.must(200, "GET", "/v1/orders/"+oid, e.adminA, nil)
	if o["lines"].([]any)[0].(map[string]any)["unit"].(float64) != 5000 || o["status"] != "paid" {
		t.Fatalf("agreement order = unit %v status %v", o["lines"].([]any)[0].(map[string]any)["unit"], o["status"])
	}
	used := func() float64 {
		for _, x := range e.must(200, "GET", "/v1/quote-templates?company="+co, e.adminA, nil)["items"].([]any) {
			if x.(map[string]any)["id"] == id {
				return x.(map[string]any)["used"].(float64)
			}
		}
		return -1
	}
	if u := used(); u != o["grandTotal"].(float64) {
		t.Fatalf("used = %v, want %v", u, o["grandTotal"])
	}
	e.must(200, "POST", "/v1/orders/"+oid+"/cancel", e.adminA, m{"reason": "prueba"})
	if u := used(); u != 0 {
		t.Fatalf("used after cancel = %v", u)
	}
	// A prepaid company gets a payment link; another company cannot use this agreement.
	if st, r := order(tokPre, preID, 3); st != 201 || r["payUrl"] == nil {
		t.Fatalf("prepaid agreement order = %d %v", st, r)
	}
	if st, _ := order(tokPre, id, 3); st != 404 {
		t.Fatalf("another company used the agreement: %d", st)
	}
	// A closed agreement takes no more orders.
	e.must(200, "PUT", "/v1/quote-templates/"+id, e.adminA, tpl(co, m{"status": "closed"}))
	if st, _ := order(tok, id, 3); st != 409 {
		t.Fatalf("closed agreement took an order: %d", st)
	}
}
