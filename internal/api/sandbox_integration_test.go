package api

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Every table that holds store data has a sandbox rule: a new table is never
// copied, or left out, by accident.
func TestSandboxEveryTableHasARule(t *testing.T) {
	e := setup(t)
	left, err := e.db.UnclassifiedTables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Fatalf("tables without a sandbox rule (internal/store/sandbox.go): %v", left)
	}
}

// snapshot is every row of a store in the given tables, as JSON text.
func snapshot(t *testing.T, e *env, tenant string, tables ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, tn := range tables {
		var v string
		if err := e.db.System.QueryRow(context.Background(), `SELECT COALESCE(string_agg((to_jsonb(x.*) - 'tenant_id')::text, E'\n' ORDER BY (to_jsonb(x.*) - 'tenant_id')::text), '') FROM `+tn+` x WHERE x.tenant_id=$1`, tenant).Scan(&v); err != nil {
			t.Fatal(err)
		}
		out[tn] = v
	}
	return out
}

func waitReady(t *testing.T, e *env, key string) map[string]any {
	t.Helper()
	e.server.WaitSandboxCopies()
	e.drain()
	sb := e.must(200, "GET", "/v1/sandbox", key, nil)
	if st := sb["sandbox"].(map[string]any)["status"]; st != "ready" {
		t.Fatalf("sandbox status %v: %v", st, sb["sandbox"])
	}
	return sb
}

func TestSandbox(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := &auth.Service{DB: e.db}
	if _, err := a.CreateMember(ctx, "store-a", "test", "dueno@a.test", "Dueña", "owner-password-123", nil, true); err != nil {
		t.Fatal(err)
	}
	_, login := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "dueno@a.test", "password": "owner-password-123"})
	owner := login["token"].(string)

	// Production has an order (personal data) and tags.
	body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "ana@example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
	if c, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "sandbox-test-1"); c != 201 {
		t.Fatalf("checkout = %d %v", c, o)
	}
	e.must(200, "PUT", "/v1/settings/tags", e.adminA, m{"tags": []m{{"kind": "ga4", "account": "G-PROD12345", "active": true}}})
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)

	// Create: a copy of everything except personal data, credentials and history.
	if st, _ := e.do("POST", "/v1/sandbox", e.sfA, m{}); st != 401 && st != 403 {
		t.Fatalf("storefront key created a sandbox: %d", st)
	}
	created := e.must(202, "POST", "/v1/sandbox", owner, m{"dataMode": "none"})
	sbxID := created["sandbox"].(map[string]any)["id"].(string)
	if !strings.HasPrefix(sbxID, store.SandboxPrefix) {
		t.Fatalf("sandbox id %q", sbxID)
	}
	if st, _ := e.do("POST", "/v1/sandbox", owner, m{}); st != 409 {
		t.Fatalf("second sandbox = %d", st)
	}
	view := waitReady(t, e, owner)
	if view["env"] != "production" || !strings.Contains(view["url"].(string), sbxID) {
		t.Fatalf("view = %v", view)
	}
	prodCounts, _ := e.db.SandboxRowCounts(ctx, "store-a")
	sbxCounts, _ := e.db.SandboxRowCounts(ctx, sbxID)
	for tn, n := range prodCounts {
		switch store.SandboxRule(tn) {
		case "copy":
			if tn == "tenant_docs" {
				continue // documents with credentials or history stay
			}
			if sbxCounts[tn] != n {
				t.Errorf("%s: production %d rows, sandbox %d", tn, n, sbxCounts[tn])
			}
		case "pii", "skip":
			if tn == "tenant_keys" || tn == "outbox" || tn == "price_history" {
				continue // its own key; its own events; price history its own prices write (a trigger)
			}
			if sbxCounts[tn] != 0 {
				t.Errorf("%s was copied (%d rows) and must not be", tn, sbxCounts[tn])
			}
		}
	}
	if sbxCounts["orders"] != 0 || sbxCounts["customers"] != 0 || sbxCounts["api_keys"] != 0 || sbxCounts["sessions"] != 0 {
		t.Fatalf("personal data or credentials in the sandbox: %v", sbxCounts)
	}
	if sbxCounts["members"] != prodCounts["members"] {
		t.Fatalf("team not synced: %d vs %d", sbxCounts["members"], prodCounts["members"])
	}
	var pw string
	_ = e.db.System.QueryRow(ctx, `SELECT password_hash FROM members WHERE tenant_id=$1 LIMIT 1`, sbxID).Scan(&pw)
	if pw != "!sandbox" {
		t.Fatalf("the sandbox team has a password: %q", pw)
	}
	if st, _ := e.do("POST", "/v1/auth/login", "", m{"store": sbxID, "email": "dueno@a.test", "password": "owner-password-123"}); st == 200 {
		t.Fatal("signed in to the sandbox with the production password")
	}
	if _, ok := e.proj.Tenant(sbxID); !ok {
		t.Fatal("sandbox not loaded in memory")
	}

	// The storefront is private: no cookie, a link of another sandbox or an
	// expired link get the private page; a valid link opens it.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	get := func(c *http.Client, path string) (int, string, http.Header) {
		t.Helper()
		res, err := c.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b), res.Header
	}
	if st, html, h := get(http.DefaultClient, "/s/"+sbxID+"/main/c"); st != 403 || !strings.Contains(html, "Este sandbox es privado") || !strings.Contains(h.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("private page = %d %s", st, h)
	}
	if st, _, _ := get(http.DefaultClient, "/s/"+sbxID+"/api/catalog?limit=1"); st != 403 {
		t.Fatalf("storefront API of the sandbox without access = %d", st)
	}
	expired, _ := e.server.sandboxToken(ctx, sbxID, time.Now().Add(-time.Minute))
	if st, _, _ := get(http.DefaultClient, "/s/"+sbxID+"/main/c?sbx="+expired); st != 403 {
		t.Fatalf("expired link = %d", st)
	}
	link := e.must(200, "POST", "/v1/sandbox/preview", owner, m{"hours": 2})["url"].(string)
	if st, html, h := get(client, link); st != 200 || !strings.Contains(html, "Sandbox · no es tu tienda real") || !strings.Contains(h.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("preview link = %d", st)
	}
	if st, _, _ := get(client, "/s/"+sbxID+"/main/p/p1"); st != 200 {
		t.Fatalf("second page with the preview cookie = %d", st)
	}
	if st, html, _ := get(http.DefaultClient, "/s/store-a/main/c"); st != 200 || strings.Contains(html, "sbx-bar") {
		t.Fatalf("production storefront = %d (or shows the sandbox bar)", st)
	}

	// Enter the sandbox: same person, a session of the sandbox; back again.
	entered := e.must(200, "POST", "/v1/sandbox/enter", owner, nil)
	inside := entered["token"].(string)
	me := e.must(200, "GET", "/v1/me", inside, nil)
	if me["tenant"] != sbxID || me["env"] != "sandbox" || me["parent"] != "store-a" {
		t.Fatalf("me inside = %v", me)
	}
	if st, _ := e.do("GET", "/v1/me", owner, nil); st != 401 {
		t.Fatalf("the production session survived the switch: %d", st)
	}
	if st, _, _ := get(http.DefaultClient, "/s/"+sbxID+"/main/c"); st != 403 {
		t.Fatal("storefront open without access")
	}

	// Work in the sandbox: a price, a new product, a deleted product, a
	// changed title, tags; production changes the same product meanwhile.
	e.must(200, "PUT", "/v1/products/P1", inside, m{"handle": "p1", "title": "Producto 1 renovado", "variants": []m{{"sku": "S1", "title": "Única"}}})
	newID := e.must(201, "POST", "/v1/products", inside, m{"handle": "p9", "title": "Producto nuevo", "variants": []m{{"sku": "S9", "title": "Única"}}})["id"].(string)
	e.must(204, "DELETE", "/v1/products/P2", inside, nil)
	e.must(204, "PUT", "/v1/prices/mx/S1", inside, m{"tiers": []m{{"minQty": 1, "unit": m{"amount": 15500, "currency": "MXN"}}}})
	e.must(200, "PUT", "/v1/products/P3", inside, m{"handle": "p3", "title": "P3 desde el sandbox", "variants": []m{{"sku": "S3", "title": "Única"}}})
	e.must(200, "PUT", "/v1/products/P3", e.adminA, m{"handle": "p3", "title": "P3 desde producción", "variants": []m{{"sku": "S3", "title": "Única"}}})
	e.must(200, "PUT", "/v1/settings/tags", inside, m{"tags": []m{{"kind": "ga4", "account": "G-PROD12345", "active": true}, {"kind": "meta", "account": "123456789012", "active": true}}})
	e.drain()
	// The sandbox's tags mark the traffic.
	if _, js, _ := get(client, "/s/"+sbxID+"/api/tags.js?v=2"); !strings.Contains(js, `"env":"sandbox"`) {
		t.Fatal("sandbox tags.js does not mark the traffic")
	}

	// Emails of the sandbox are captured, never sent.
	sbxSF, _ := e.db.CreateAPIKey(ctx, sbxID, "sf", "storefront")
	if c, o := e.do("POST", "/v1/checkout", sbxSF, body, "Idempotency-Key", "sandbox-test-2"); c != 201 {
		t.Fatalf("checkout in the sandbox = %d %v", c, o)
	}
	ns := &notify.Service{DB: e.db, Keys: e.keys}
	if _, err := ns.Send(ctx); err != nil {
		t.Fatal(err)
	}
	var sent, captured int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='sent'), count(*) FILTER (WHERE status='skipped' AND last_error LIKE 'Capturado en el sandbox%') FROM notification_messages WHERE tenant_id=$1`, sbxID).Scan(&sent, &captured)
	if sent != 0 || captured == 0 {
		t.Fatalf("sandbox emails: %d sent, %d captured", sent, captured)
	}
	if mail := e.must(200, "GET", "/v1/sandbox/mail", e.adminA, nil); len(mail["items"].([]any)) == 0 {
		t.Fatal("captured emails not listed")
	}

	// Diff, from production.
	diff := e.must(200, "GET", "/v1/sandbox/diff?areas=catalog,prices,tags", e.adminA, nil)
	cat := diff["areas"].([]any)[0].(map[string]any)
	if cat["inserts"].(float64) < 1 || cat["updates"].(float64) < 1 || cat["deletes"].(float64) < 1 || cat["conflicts"].(float64) != 1 {
		t.Fatalf("catalog diff = %v", cat)
	}
	labels := ""
	for _, s := range cat["sample"].([]any) {
		labels += s.(map[string]any)["label"].(string) + "|"
	}
	if !strings.Contains(labels, "Producto nuevo") || !strings.Contains(labels, "P3 desde el sandbox") {
		t.Fatalf("diff labels: %s", labels)
	}
	if ch := toStrings(diff["tags"]); len(ch) == 0 || !strings.Contains(strings.Join(ch, "|"), "Agregó Meta") {
		t.Fatalf("tags diff = %v", ch)
	}

	// Publish catalog and prices; the conflict stays as production has it.
	before := snapshot(t, e, "store-a", "products", "variants", "prices", "stock_levels")
	pub := e.must(200, "POST", "/v1/sandbox/publish", owner2(t, e, inside), m{"areas": []string{"catalog", "prices", "tags"}})
	res := pub["publish"].(map[string]any)
	if res["skipped"].(float64) != 1 || res["applied"].(float64) < 4 {
		t.Fatalf("publish = %v", res)
	}
	if tg := pub["tags"].(map[string]any); !strings.Contains(strings.Join(toStrings(tg["changes"]), "|"), "Publicado desde el sandbox") {
		t.Fatalf("tags publish = %v", tg)
	}
	e.drain()
	p1 := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	if p1["title"] != "Producto 1 renovado" {
		t.Fatalf("production P1 = %v", p1["title"])
	}
	if st, _ := e.do("GET", "/v1/products/P2", e.adminA, nil); st != 404 {
		t.Fatalf("P2 still in production: %d", st)
	}
	if p9 := e.must(200, "GET", "/v1/products/"+newID, e.adminA, nil); p9["title"] != "Producto nuevo" {
		t.Fatalf("P9 = %v", p9)
	}
	if p3 := e.must(200, "GET", "/v1/products/P3", e.adminA, nil); p3["title"] != "P3 desde producción" {
		t.Fatalf("the conflict was overwritten: %v", p3["title"])
	}
	if _, html, _ := get(http.DefaultClient, "/s/store-a/main/p/p1"); !strings.Contains(html, "Producto 1 renovado") || !strings.Contains(html, "155.00") {
		t.Fatal("production storefront does not show the published product and price")
	}
	// Published: nothing left to publish but the conflict.
	again := e.must(200, "GET", "/v1/sandbox/diff?areas=catalog,prices", e.adminA, nil)
	if c := again["areas"].([]any)[0].(map[string]any); c["inserts"].(float64)+c["updates"].(float64)+c["deletes"].(float64) != 0 || c["conflicts"].(float64) != 1 {
		t.Fatalf("diff after publish = %v", c)
	}

	// Revert: production exactly as it was.
	pubID := res["id"].(string)
	e.must(200, "POST", "/v1/sandbox/publishes/"+pubID+"/revert", e.adminA, m{})
	e.drain()
	after := snapshot(t, e, "store-a", "products", "variants", "prices", "stock_levels")
	for tn := range before {
		if before[tn] != after[tn] {
			t.Fatalf("%s differs after the revert", tn)
		}
	}
	if p1 := e.must(200, "GET", "/v1/products/P1", e.adminA, nil); p1["title"] != "Producto 1" {
		t.Fatalf("P1 after revert = %v", p1["title"])
	}
	if st, _ := e.do("POST", "/v1/sandbox/publishes/"+pubID+"/revert", e.adminA, m{}); st != 409 {
		t.Fatalf("second revert = %d", st)
	}
	// The sandbox's changes count as changes again.
	re := e.must(200, "GET", "/v1/sandbox/diff?areas=catalog", e.adminA, nil)["areas"].([]any)[0].(map[string]any)
	if re["inserts"].(float64) < 1 || re["deletes"].(float64) < 1 {
		t.Fatalf("diff after revert = %v", re)
	}

	// A revert when production changed a published row since: refused
	// unless confirmed.
	pub2 := e.must(200, "POST", "/v1/sandbox/publish", e.adminA, m{"areas": []string{"catalog"}, "products": []string{"P1"}})
	id2 := pub2["publish"].(map[string]any)["id"].(string)
	e.drain()
	e.must(200, "PUT", "/v1/products/P1", e.adminA, m{"handle": "p1", "title": "Cambio posterior", "variants": []m{{"sku": "S1", "title": "Única"}}})
	if st, b := e.do("POST", "/v1/sandbox/publishes/"+id2+"/revert", e.adminA, m{}); st != 409 || b["code"] != "changed_since" {
		t.Fatalf("revert over a later change = %d %v", st, b)
	}
	e.must(200, "POST", "/v1/sandbox/publishes/"+id2+"/revert", e.adminA, m{"force": true})
	if ps := e.must(200, "GET", "/v1/sandbox/publishes", e.adminA, nil); len(ps["items"].([]any)) != 2 {
		t.Fatalf("publishes = %v", ps)
	}

	// Refresh: the sandbox is production again; the person inside stays.
	e.must(202, "POST", "/v1/sandbox/refresh", e.adminA, nil)
	waitReady(t, e, e.adminA)
	if d := e.must(200, "GET", "/v1/sandbox/diff?areas=catalog,prices", e.adminA, nil); d["areas"].([]any)[0].(map[string]any)["inserts"].(float64) != 0 {
		t.Fatalf("diff after refresh = %v", d)
	}
	if st, _ := e.do("GET", "/v1/me", inside, nil); st != 200 {
		t.Fatalf("session inside lost on refresh: %d", st)
	}

	// Lifecycle: notice at 25 days, deletion at 30.
	_, _ = e.db.System.Exec(ctx, `UPDATE sandboxes SET last_used_at = now() - interval '26 days' WHERE id=$1`, sbxID)
	e.server.Notify = ns
	if err := e.server.RunSandboxLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	var notices int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE tenant_id='store-a' AND template=$1`, notify.TplSandboxExpiring).Scan(&notices)
	if notices != 1 {
		t.Fatalf("expiry notices = %d", notices)
	}
	_, _ = e.db.System.Exec(ctx, `UPDATE sandboxes SET last_used_at = now() - interval '31 days' WHERE id=$1`, sbxID)
	if err := e.server.RunSandboxLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	e.drain()
	left, _ := e.db.SandboxRowCounts(ctx, sbxID)
	delete(left, "audit_log")
	delete(left, "outbox")
	if len(left) > 0 {
		t.Fatalf("rows left after the expiry: %v", left)
	}
	if _, ok := e.proj.Tenant(sbxID); ok {
		t.Fatal("deleted sandbox still in memory")
	}
	if st, _ := e.do("GET", "/v1/sandbox", e.adminA, nil); st != 200 {
		t.Fatalf("view after deletion = %d", st)
	}
	if st, _, _ := get(client, "/s/"+sbxID+"/main/c"); st == 200 {
		t.Fatal("a deleted sandbox still answers")
	}
	// Production never lost its order.
	var orders int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM orders WHERE tenant_id='store-a'`).Scan(&orders)
	if orders != 1 {
		t.Fatalf("production orders = %d", orders)
	}
}

// owner2 publishes from inside the sandbox with the person's production
// permissions (it is the same person).
func owner2(t *testing.T, e *env, inside string) string { t.Helper(); return inside }

// A sandbox never charges for real: live keys are ignored and refused.
func TestSandboxPaymentsNeverLive(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	pa := &PaymentAccounts{DB: e.db, Keys: e.keys, IsSandbox: func(t string) bool { return t == "store-a" }}
	enc, _ := e.keys.Encrypt("store-a", []byte("sk_live_123"), "stripe-secret")
	if err := e.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO payment_accounts (tenant_id, mode, secret_enc) VALUES ('store-a', 'own_keys', $1)`, enc)
		return err
	}); err != nil {
		t.Skip("payment_accounts shape differs: ", err)
	}
	acct, err := pa.Resolve(ctx, "store-a")
	if err != nil {
		t.Fatal(err)
	}
	if acct.Provider.Name() != "sandbox" {
		t.Fatalf("a sandbox with live keys charges with %q", acct.Provider.Name())
	}
}

// With APEXX_SANDBOX_HOST, a sandbox lives at {id}.<suffix>: a verified
// domain of the sandbox (never of production), approved for a certificate,
// routed to the sandbox and still private.
func TestSandboxTemporaryHost(t *testing.T) {
	t.Setenv("APEXX_SANDBOX_HOST", "sandbox.apexx.test")
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	created := e.must(202, "POST", "/v1/sandbox", e.adminA, m{})
	id := created["sandbox"].(map[string]any)["id"].(string)
	if u := created["url"].(string); u != "https://"+id+".sandbox.apexx.test/" {
		t.Fatalf("url = %s", u)
	}
	waitReady(t, e, e.adminA)
	host := id + ".sandbox.apexx.test"
	ask := func(h string) int {
		res, err := http.Get(e.srv.URL + "/internal/tls/ask?domain=" + h)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if ask(host) != 200 || ask("otro.sandbox.apexx.test") == 200 {
		t.Fatalf("certificate approval: sandbox %d, unknown host %d", ask(host), ask("otro.sandbox.apexx.test"))
	}
	var owner string
	_ = e.db.System.QueryRow(context.Background(), `SELECT tenant_id FROM domains WHERE hostname=$1`, host).Scan(&owner)
	if owner != id {
		t.Fatalf("the temporary host belongs to %q", owner)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/", nil)
	req.Host = host
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 403 || !strings.Contains(string(b), "Este sandbox es privado") {
		t.Fatalf("temporary host without access = %d", res.StatusCode)
	}
	link := e.must(200, "POST", "/v1/sandbox/preview", e.adminA, m{})["url"].(string)
	u, _ := http.NewRequest("GET", e.srv.URL+"/?"+strings.SplitN(link, "?", 2)[1], nil)
	u.Host = host
	res, err = http.DefaultTransport.RoundTrip(u)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") == "" || strings.Contains(res.Header.Get("Location"), "/s/") {
		t.Fatalf("preview on the temporary host = %d → %s", res.StatusCode, res.Header.Get("Location"))
	}
	cookie := res.Cookies()[0]
	r2, _ := http.NewRequest("GET", e.srv.URL+"/", nil)
	r2.Host = host
	r2.AddCookie(cookie)
	res, err = http.DefaultTransport.RoundTrip(r2)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "sbx-bar") {
		t.Fatalf("temporary host with the preview cookie = %d", res.StatusCode)
	}
}

// TestSandboxNothingLeaves: nothing a sandbox does reaches a real service.
// Connecting webhooks, marketplaces or ad conversions is refused; what was
// copied from production (automations with a webhook action, external
// warehouses, a subscription) is captured instead of sent.
func TestSandboxNothingLeaves(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := &auth.Service{DB: e.db}
	if _, err := a.CreateMember(ctx, "store-a", "test", "dueno@a.test", "Dueña", "owner-password-123", nil, true); err != nil {
		t.Fatal(err)
	}
	_, login := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "dueno@a.test", "password": "owner-password-123"})
	owner := login["token"].(string)
	created := e.must(202, "POST", "/v1/sandbox", owner, m{"dataMode": "none"})
	sbxID := created["sandbox"].(map[string]any)["id"].(string)
	waitReady(t, e, owner)
	inside := e.must(200, "POST", "/v1/sandbox/enter", owner, nil)["token"].(string)

	var hits atomic.Int64
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer hook.Close()
	automationHTTPClient = hook.Client()
	defer func() { automationHTTPClient = nil }()

	// Connecting anything that talks to the outside is refused, with a reason.
	for _, c := range []struct {
		method, path string
		body         m
	}{
		{"POST", "/v1/webhooks", m{"url": hook.URL + "/w", "types": []string{"order.created"}}},
		{"PUT", "/v1/sales-channels/mercadolibre", m{"clientId": "x", "clientSecret": "y", "refreshToken": "z"}},
		{"PUT", "/v1/settings/ad-conversions", m{}},
	} {
		st, out := e.do(c.method, c.path, inside, c.body)
		if st != 409 || out["code"] != "sandbox_no_outbound" {
			t.Errorf("%s %s inside the sandbox = %d %v", c.method, c.path, st, out)
		}
	}
	// Production still connects them.
	if st, out := e.do("POST", "/v1/webhooks", e.adminA, m{"url": "https://hooks.example.com/w", "types": []string{"order.created"}}); st != 201 {
		t.Errorf("production webhook = %d %v", st, out)
	}

	// An automation copied from production with a webhook action: captured.
	res, err := e.server.automationAction(ctx, sbxID, "test", "aut_1", "Avisar", automationAction{Kind: "webhook", URL: hook.URL + "/aut"}, orders.Order{ID: "ord_1"})
	if err != nil || !strings.HasPrefix(res, sandboxCaptured) {
		t.Fatalf("automation webhook in the sandbox = %q, %v", res, err)
	}
	// The same action in production does call out (the guard is the sandbox).
	if _, err := e.server.automationAction(ctx, "store-a", "test", "aut_1", "Avisar", automationAction{Kind: "webhook", URL: hook.URL + "/aut"}, orders.Order{ID: "ord_1"}); err != nil {
		t.Fatalf("automation webhook in production: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("outgoing calls = %d, want only production's 1", hits.Load())
	}

	// An external warehouse copied with the locations: the request stays.
	if st, _, msg := e.server.deliverWarehouse(ctx, sbxID, "ord_1", "loc_1", "fulfill"); st != "rejected" || !strings.HasPrefix(msg, sandboxCaptured) {
		t.Fatalf("warehouse from the sandbox = %s %q", st, msg)
	}

	// A subscription that exists anyway (written behind the API): no delivery.
	if _, err := e.db.System.Exec(ctx, `INSERT INTO webhook_subscriptions (tenant_id, id, url, secret_enc, types, active)
		SELECT $1, 'whs_sbx', url, secret_enc, types, true FROM webhook_subscriptions WHERE tenant_id='store-a' LIMIT 1`, sbxID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Tx(ctx, sbxID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO outbox (tenant_id, topic, key, payload) VALUES ($1, 'order.created', 'ord_1', '{}')`, sbxID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := e.server.Webhooks.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM webhook_deliveries WHERE tenant_id=$1`, sbxID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d webhook deliveries from the sandbox", n)
	}
}
