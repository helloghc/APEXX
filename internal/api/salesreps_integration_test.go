package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// Sales representative: only the assigned representative, only with the
// company's consent and only for its buyers; the link works once; the
// session shows the banner data; the order and the audit log record both
// names; the buyer's consent can be withdrawn; API keys never act as buyers.
func TestSalesRepActsAsBuyer(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO members (tenant_id, id, email, name, is_owner, status, permissions, password_hash) VALUES ('store-a','mem_rep','rep@a.example','Rita Representante',false,'active','{"customers":"edit","orders":"read"}','x')`,
		`INSERT INTO members (tenant_id, id, email, name, is_owner, status, permissions, password_hash) VALUES ('store-a','mem_otro','otro@a.example','Otro Vendedor',false,'active','{"customers":"edit"}','x')`,
	} {
		if _, err := e.db.System.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	login := func(id, email string) string {
		_ = e.server.Auth.SetPassword(ctx, "store-a", "test", id, "", "Contrasena-Larga-2026!")
		sess, _, err := e.server.Auth.Login(ctx, "store-a", email, "Contrasena-Larga-2026!", "", "127.0.0.1", "test")
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}
	rep, other := login("mem_rep", "rep@a.example"), login("mem_otro", "otro@a.example")
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Atendida SA", "terms": "net30", "creditLimit": 5000000, "salesRep": "mem_rep",
		"buyerEmails": []string{"compras@atendida.example"}})["id"].(string)
	act := func(key, email string) (int, m) {
		return e.do("POST", "/v1/companies/"+co+"/act-as", key, m{"email": email})
	}
	if st, r := act(rep, "compras@atendida.example"); st != 403 || r["code"] != "no_consent" {
		t.Fatalf("without consent = %d %v", st, r)
	}
	e.must(200, "PUT", "/v1/companies/"+co, e.adminA, m{"name": "Atendida SA", "terms": "net30", "creditLimit": 5000000, "salesRep": "mem_rep", "repAccess": true})
	if st, r := act(other, "compras@atendida.example"); st != 403 || r["code"] != "not_rep" {
		t.Fatalf("another seller = %d %v", st, r)
	}
	if st, r := act(e.adminA, "compras@atendida.example"); st != 403 || r["code"] != "member_only" {
		t.Fatalf("API key = %d %v", st, r)
	}
	if st, _ := act(rep, "intruso@otra.example"); st != 400 {
		t.Fatalf("not a buyer: %d", st)
	}
	st, h := act(rep, "compras@atendida.example")
	if st != 201 || !strings.Contains(h["url"].(string), "/representante/") {
		t.Fatalf("handoff = %d %v", st, h)
	}
	// The link opens a session once.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(e.srv.URL + h["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	var session string
	marker := false
	for _, c := range resp.Cookies() {
		if c.Name == "apexx_shopper" {
			session = c.Value
		}
		marker = marker || (c.Name == "apexx_rep" && c.Value == "1" && !c.HttpOnly)
	}
	if resp.StatusCode != 303 || session == "" || !marker {
		t.Fatalf("handoff open = %d session=%v marker=%v", resp.StatusCode, session != "", marker)
	}
	if again, _ := client.Get(e.srv.URL + h["url"].(string)); again.StatusCode != 410 {
		t.Fatalf("link reused: %d", again.StatusCode)
	}
	as := func(method, path string, body any, extra ...string) (int, m) {
		return e.do(method, path, "", body, append([]string{"Cookie", "apexx_shopper=" + session, "Origin", e.srv.URL}, extra...)...)
	}
	if _, a := as("GET", "/s/store-a/api/account/acting", nil); a["acting"] != true || a["representative"] != "Rita Representante" || a["buyer"] != "compras@atendida.example" {
		t.Fatalf("acting = %v", a)
	}
	// An order by the representative records both.
	st, o := as("POST", "/s/store-a/api/checkout", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "compras@atendida.example", "name": "Compras"}, "paymentMethod": "credit"}, "Idempotency-Key", "rep-order-0001")
	if st != 201 {
		t.Fatalf("rep order = %d %v", st, o)
	}
	full := e.must(200, "GET", "/v1/orders/"+o["id"].(string), e.adminA, nil)
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='store-a' AND action='order.by_representative' AND target=$1`, o["id"]).Scan(&n)
	if n != 1 || !strings.Contains(toJSON(full), "Rita Representante") {
		t.Fatalf("order attribution: audit=%d notes=%v", n, full["notes"])
	}
	// The representative cannot give or take the company's consent.
	if st, _ := as("PUT", "/s/store-a/api/account/company/rep-access", m{"on": false}); st != 403 {
		t.Fatalf("representative changed the consent: %d", st)
	}
	// Exit closes the session.
	as("POST", "/s/store-a/api/account/acting/exit", nil)
	if st, _ := as("GET", "/s/store-a/api/account/acting", nil); st != 401 {
		t.Fatalf("session after exit: %d", st)
	}
	// Withdrawn consent stops new links; store B never reaches the company.
	e.must(200, "PUT", "/v1/companies/"+co, e.adminA, m{"name": "Atendida SA", "terms": "net30", "creditLimit": 5000000, "salesRep": "mem_rep", "repAccess": false})
	if st, _ := act(rep, "compras@atendida.example"); st != 403 {
		t.Fatalf("after consent withdrawn: %d", st)
	}
	if st, _ := e.do("POST", "/v1/companies/"+co+"/act-as", e.adminB, m{"email": "compras@atendida.example"}); st != 403 && st != 404 {
		t.Fatalf("store B acted as a buyer of store A: %d", st)
	}
	if _, _, _, err := e.server.Customers.ConsumeHandoff(ctx, "store-b", strings.TrimPrefix(h["url"].(string), "/s/store-a/main/representante/")); err != customers.ErrHandoff {
		t.Fatalf("handoff crossed stores: %v", err)
	}
}
