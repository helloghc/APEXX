package api

import (
	"context"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// Payment methods per company and per branch: the server refuses a method
// the company (or the buyer's branch) does not use, and the store page hides
// it; a branch can pay differently from its company.
func TestCompanyPaymentMethods(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("POST", "/v1/companies", e.adminA, m{"name": "Mala", "allowedPayments": []string{"bitcoin"}}); st != 400 {
		t.Fatalf("unknown method accepted: %d", st)
	}
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Medios SA", "terms": "net30", "creditLimit": 5000000, "allowedPayments": []string{"credit", "spei"},
		"buyerEmails": []string{"matriz@medios.example", "planta@medios.example"}})["id"].(string)
	n := 0
	api := func(method string) (int, m) {
		n++
		return e.do("POST", "/v1/checkout", e.adminA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
			"customer": m{"email": "matriz@medios.example", "name": "Matriz"}, "paymentMethod": method, "companyId": co}, "Idempotency-Key", "medios-api-"+string(rune('a'+n)))
	}
	if st, r := api("pm_card_visa"); st != 409 || r["code"] != "payment_not_allowed" {
		t.Fatalf("card for a company without card = %d %v", st, r)
	}
	if st, r := api("credit"); st != 201 {
		t.Fatalf("credit = %d %v", st, r)
	}
	// SPEI passes the company's filter (the store itself may still not offer it).
	if _, r := api("spei"); r["code"] == "payment_not_allowed" {
		t.Fatalf("spei refused by the company filter: %v", r)
	}
	// A branch that pays only by card.
	e.must(200, "PUT", "/v1/companies/"+co+"/locations", e.adminA, m{"items": []m{{"id": "", "name": "Planta Norte", "allowedPayments": []string{"card"}}}})
	branch := e.must(200, "GET", "/v1/companies/"+co+"/locations", e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)
	e.must(200, "PUT", "/v1/companies/"+co+"/members", e.adminA, m{"items": []m{{"email": "matriz@medios.example"}, {"email": "planta@medios.example", "location": branch}}})
	email := "planta@medios.example"
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
	as := func(method, path string, body any, extra ...string) (int, m) {
		return e.do(method, path, "", body, append([]string{"Cookie", "apexx_shopper=" + tok, "Origin", e.srv.URL}, extra...)...)
	}
	_, sim := as("POST", "/s/store-a/api/simulate", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}})
	hidden := toJSON(sim["hiddenPayments"])
	if !contains3(hidden, "oxxo", "spei") || contains3(hidden, "card") {
		t.Fatalf("branch hidden payments = %s", hidden)
	}
	if _, cmp := as("GET", "/s/store-a/api/company", nil); cmp["canCredit"] != false {
		t.Fatalf("branch without credit can see credit: %v", cmp)
	}
	buy := func(method, key string) (int, m) {
		return as("POST", "/s/store-a/api/checkout", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
			"customer": m{"email": email, "name": "Planta"}, "paymentMethod": method}, "Idempotency-Key", key)
	}
	if st, r := buy("credit", "medios-branch-credit"); st != 409 || r["code"] != "payment_not_allowed" {
		t.Fatalf("branch on credit = %d %v", st, r)
	}
	if st, r := buy("pm_card_visa", "medios-branch-card"); st != 201 {
		t.Fatalf("branch by card = %d %v", st, r)
	}
}

func contains3(s string, subs ...string) bool {
	for _, x := range subs {
		if !containsStr(s, `"`+x+`"`) {
			return false
		}
	}
	return true
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
