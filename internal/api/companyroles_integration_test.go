package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// Company roles: what a role allows is checked by the server on every credit
// purchase and quote; editing a role changes its members at once; a company
// administrator manages roles from the store but cannot remove their own
// power; deleting a role gives its people back the switches they had.
func TestCompanyRoles(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Roles SA", "terms": "net30", "creditLimit": 5000000})["id"].(string)
	role := func(key string, body m) (int, m) { return e.do("POST", "/v1/companies/"+co+"/roles", key, body) }
	if st, _ := role(e.adminA, m{"name": "Malo", "perms": m{"approve": true, "level": 9}}); st != 400 {
		t.Fatalf("level 9 accepted: %d", st)
	}
	quoteOnly := e.must(201, "POST", "/v1/companies/"+co+"/roles", e.adminA, m{"name": "Solo cotiza", "perms": m{"quote": true}})["id"].(string)
	buyer := e.must(201, "POST", "/v1/companies/"+co+"/roles", e.adminA, m{"name": "Comprador", "perms": m{"buyCredit": true, "viewCredit": true, "quote": true}})["id"].(string)
	boss := e.must(201, "POST", "/v1/companies/"+co+"/roles", e.adminA, m{"name": "Jefe", "perms": m{"buyCredit": true, "viewCredit": true, "quote": true, "approve": true, "level": 1, "manage": true}})["id"].(string)
	watcher := e.must(201, "POST", "/v1/companies/"+co+"/roles", e.adminA, m{"name": "Mirón", "perms": m{"viewCredit": true}})["id"].(string)
	if st, _ := role(e.adminA, m{"name": "Jefe", "perms": m{}}); st != 400 {
		t.Fatalf("duplicate role name: %d", st)
	}
	if st, _ := role(e.adminB, m{"name": "Intruso", "perms": m{}}); st != 404 {
		t.Fatalf("store B created a role in store A's company: %d", st)
	}
	members := e.must(200, "PUT", "/v1/companies/"+co+"/members", e.adminA, m{"items": []m{
		{"email": "cotiza@roles.example", "role": quoteOnly}, {"email": "compra@roles.example", "role": buyer},
		{"email": "jefe@roles.example", "role": boss}, {"email": "mira@roles.example", "role": watcher}}})["items"].([]any)
	for _, x := range members {
		mm := x.(map[string]any)
		if mm["email"] == "jefe@roles.example" && (mm["admin"] != true || mm["approver"] != true || mm["role"] != boss) {
			t.Fatalf("boss = %v", mm)
		}
	}
	if st, _ := e.do("PUT", "/v1/companies/"+co+"/members", e.adminA, m{"items": []m{{"email": "x@roles.example", "role": "crl_nope"}}}); st != 400 {
		t.Fatalf("unknown role accepted: %d", st)
	}
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
	as := func(tok, method, path string, body any, extra ...string) (int, m) {
		return e.do(method, path, "", body, append([]string{"Cookie", "apexx_shopper=" + tok, "Origin", e.srv.URL}, extra...)...)
	}
	n := 0
	buy := func(tok, email string) (int, m) {
		n++
		return as(tok, "POST", "/s/store-a/api/checkout", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
			"customer": m{"email": email, "name": "Persona"}, "paymentMethod": "credit"}, "Idempotency-Key", fmt.Sprintf("roles-order-%d", n))
	}
	tq, tb, tj, tw := login("cotiza@roles.example"), login("compra@roles.example"), login("jefe@roles.example"), login("mira@roles.example")
	if _, c := as(tq, "GET", "/s/store-a/api/company", nil); c["canCredit"] != false || c["available"].(float64) != -1 {
		t.Fatalf("quote-only company view = %v", c)
	}
	if st, r := buy(tq, "cotiza@roles.example"); st != 409 || r["code"] != "role_forbidden" {
		t.Fatalf("quote-only bought on credit: %d %v", st, r)
	}
	if st, r := buy(tb, "compra@roles.example"); st != 201 {
		t.Fatalf("buyer on credit = %d %v", st, r)
	}
	if st, r := as(tw, "POST", "/s/store-a/api/quotes", m{"channel": "mx", "items": []m{{"sku": "S1", "qty": 50}}, "customer": m{"email": "mira@roles.example", "name": "Mira"}}); st != 403 || r["code"] != "role_forbidden" {
		t.Fatalf("watcher asked for a quote: %d %v", st, r)
	}
	// Editing a role changes its members at once.
	e.must(200, "PUT", "/v1/companies/"+co+"/roles/"+buyer, e.adminA, m{"name": "Comprador", "perms": m{"viewCredit": true, "quote": true}})
	if st, r := buy(tb, "compra@roles.example"); st != 409 || r["code"] != "role_forbidden" {
		t.Fatalf("after removing credit from the role: %d %v", st, r)
	}
	// The company's administrator manages roles from the store, but not to lock themselves out.
	if st, l := as(tj, "GET", "/s/store-a/api/account/company/roles", nil); st != 200 || len(l["items"].([]any)) != 4 {
		t.Fatalf("admin roles = %d %v", st, l)
	}
	if st, _ := as(tq, "GET", "/s/store-a/api/account/company/roles", nil); st != 403 {
		t.Fatalf("non-admin read the roles: %d", st)
	}
	if st, r := as(tj, "PUT", "/s/store-a/api/account/company/roles/"+boss, m{"name": "Jefe", "perms": m{"buyCredit": true}}); st != 400 || !strings.Contains(toJSON(r), "administrar") {
		t.Fatalf("admin removed their own power: %d %v", st, r)
	}
	if st, _ := as(tj, "POST", "/s/store-a/api/account/company/roles", m{"name": "Practicante", "perms": m{"quote": true}}); st != 201 {
		t.Fatalf("admin created a role: %d", st)
	}
	// Deleting a role: its people go back to full buying permissions.
	e.must(204, "DELETE", "/v1/companies/"+co+"/roles/"+quoteOnly, e.adminA, nil)
	if st, r := buy(tq, "cotiza@roles.example"); st != 201 {
		t.Fatalf("after deleting the role = %d %v", st, r)
	}
}
