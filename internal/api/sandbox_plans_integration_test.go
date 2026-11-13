package api

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/store"
)

// sandboxOwner creates store-a's owner and returns a session of them.
func sandboxOwner(t *testing.T, e *env) string {
	t.Helper()
	a := &auth.Service{DB: e.db}
	if _, err := a.CreateMember(context.Background(), "store-a", "test", "dueno@a.test", "Dueña", "owner-password-123", nil, true); err != nil {
		t.Fatal(err)
	}
	_, login := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "dueno@a.test", "password": "owner-password-123"})
	return login["token"].(string)
}

func newSandbox(t *testing.T, e *env, owner, name string) string {
	t.Helper()
	created := e.must(202, "POST", "/v1/sandboxes", owner, m{"name": name})
	id := created["sandbox"].(map[string]any)["id"].(string)
	e.server.WaitSandboxCopies()
	e.drain()
	if sb := e.must(200, "GET", "/v1/sandboxes/"+id, owner, nil)["sandbox"].(map[string]any); sb["status"] != "ready" || sb["name"] != name {
		t.Fatalf("sandbox %s = %v", name, sb)
	}
	return id
}

// TestSandboxPlans: what the plan includes is enforced (how many, how big,
// test data) and a store can have several named sandboxes.
func TestSandboxPlans(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := sandboxOwner(t, e)

	// Default plan: one sandbox.
	first := newSandbox(t, e, owner, "Pruebas")
	// The copy leaves the planner's statistics fresh (a revert's plan
	// depends on them) and the app role cannot run the analyze itself.
	var analyzed bool
	_ = e.db.System.QueryRow(ctx, `SELECT last_analyze IS NOT NULL FROM pg_stat_user_tables WHERE relname='sandbox_base'`).Scan(&analyzed)
	if !analyzed {
		t.Fatal("sandbox_base was not analyzed after the copy")
	}
	if _, err := e.db.App.Exec(ctx, `SELECT apexx_analyze_sandbox()`); err == nil {
		t.Fatal("the app role can run apexx_analyze_sandbox")
	}
	st, out := e.do("POST", "/v1/sandboxes", owner, m{"name": "Otro"})
	if st != 409 || out["code"] != "plan_limit" || !strings.Contains(out["detail"].(string), "1 sandbox") {
		t.Fatalf("second sandbox on the default plan = %d %v", st, out)
	}

	// The operator raises the plan: a second sandbox, named.
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Plan: "pro", Sandboxes: 2, SandboxDemoData: true, SandboxSchedule: true}); err != nil {
		t.Fatal(err)
	}
	if st, out := e.do("POST", "/v1/sandboxes", owner, m{"name": "pruebas"}); st != 409 || out["code"] != "sandbox_exists" {
		t.Fatalf("same name (other case) = %d %v", st, out)
	}
	if st, _ := e.do("POST", "/v1/sandboxes", owner, m{"name": strings.Repeat("x", 61)}); st != 400 {
		t.Fatalf("61-character name = %d", st)
	}
	second := newSandbox(t, e, owner, "Black Friday")
	list := e.must(200, "GET", "/v1/sandboxes", owner, nil)
	if list["used"].(float64) != 2 || len(list["items"].([]any)) != 2 || list["plan"].(map[string]any)["plan"] != "pro" {
		t.Fatalf("list = %v", list)
	}
	if st, out := e.do("POST", "/v1/sandboxes", owner, m{"name": "Tercero"}); st != 409 || out["code"] != "plan_limit" {
		t.Fatalf("third sandbox on a plan of 2 = %d %v", st, out)
	}

	// Rename; another store never sees these sandboxes.
	e.must(200, "PATCH", "/v1/sandboxes/"+second, owner, m{"name": "Navidad"})
	if st, out := e.do("PATCH", "/v1/sandboxes/"+second, owner, m{"name": "Pruebas"}); st != 409 || out["code"] != "sandbox_exists" {
		t.Fatalf("rename to a taken name = %d %v", st, out)
	}
	for _, path := range []string{"/v1/sandboxes/" + second, "/v1/sandboxes/" + second + "/diff"} {
		if st, _ := e.do("GET", path, e.adminB, nil); st != 404 {
			t.Fatalf("another store reads %s: %d", path, st)
		}
	}
	if st, _ := e.do("DELETE", "/v1/sandboxes/"+second, e.adminB, nil); st != 404 {
		t.Fatalf("another store deletes the sandbox: %d", st)
	}

	// Enter one, then move to the other without passing through production.
	in1 := e.must(200, "POST", "/v1/sandboxes/"+first+"/enter", owner, nil)["token"].(string)
	if me := e.must(200, "GET", "/v1/me", in1, nil); me["tenant"] != first {
		t.Fatalf("me = %v", me)
	}
	in2 := e.must(200, "POST", "/v1/sandboxes/"+second+"/enter", in1, nil)["token"].(string)
	if me := e.must(200, "GET", "/v1/me", in2, nil); me["tenant"] != second || me["parent"] != "store-a" {
		t.Fatalf("me in the second sandbox = %v", me)
	}
	if st, _ := e.do("GET", "/v1/me", in1, nil); st != 401 {
		t.Fatalf("the first sandbox's session survived the move: %d", st)
	}
	if l := e.must(200, "GET", "/v1/sandboxes", in2, nil); l["current"] != second || l["env"] != "sandbox" {
		t.Fatalf("list from inside = %v", l)
	}
	// The singular routes act on the sandbox the person is in.
	if v := e.must(200, "GET", "/v1/sandbox", in2, nil)["sandbox"].(map[string]any); v["id"] != second {
		t.Fatalf("GET /v1/sandbox inside the second = %v", v)
	}
	prod := e.must(200, "POST", "/v1/sandbox/exit", in2, nil)["token"].(string)

	// Size: a sandbox holds at most what the plan says.
	var products int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id='store-a'`).Scan(&products)
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 3, SandboxMaxProducts: products - 1, SandboxDemoData: true}); err != nil {
		t.Fatal(err)
	}
	if st, out := e.do("POST", "/v1/sandboxes", prod, m{"name": "Grande"}); st != 409 || out["code"] != "plan_limit" || !strings.Contains(out["detail"].(string), "productos") {
		t.Fatalf("a copy over the size limit = %d %v", st, out)
	}
	if st, out := e.do("POST", "/v1/sandboxes/"+first+"/refresh", prod, nil); st != 409 || out["code"] != "plan_limit" {
		t.Fatalf("refresh over the size limit = %d %v", st, out)
	}
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 3, SandboxMaxProducts: products + 1, SandboxDemoData: false}); err != nil {
		t.Fatal(err)
	}
	in1 = e.must(200, "POST", "/v1/sandboxes/"+first+"/enter", prod, nil)["token"].(string)
	e.must(201, "POST", "/v1/products", in1, m{"handle": "cupo-1", "title": "Cabe", "variants": []m{{"sku": "CUPO1", "title": "Única"}}})
	st, out = e.do("POST", "/v1/products", in1, m{"handle": "cupo-2", "title": "No cabe", "variants": []m{{"sku": "CUPO2", "title": "Única"}}})
	if st != 409 || out["code"] != "plan_limit" {
		t.Fatalf("a product over the size limit = %d %v", st, out)
	}
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id=$1`, first).Scan(&n)
	if n != products+1 {
		t.Fatalf("sandbox has %d products, want %d (the refused one rolled back)", n, products+1)
	}
	// Production is never limited by the sandbox plan.
	e.must(201, "POST", "/v1/products", e.adminA, m{"handle": "prod-x", "title": "Producción", "variants": []m{{"sku": "PRODX", "title": "Única"}}})
	prod = e.must(200, "POST", "/v1/sandbox/exit", in1, nil)["token"].(string)

	// Test data: refused when the plan does not include it.
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 3, SandboxDemoData: false}); err != nil {
		t.Fatal(err)
	}
	if st, out := e.do("POST", "/v1/sandboxes", prod, m{"name": "Demo", "dataMode": "demo"}); st != 409 || out["code"] != "plan_limit" {
		t.Fatalf("test data off the plan = %d %v", st, out)
	}

	// Plan 0: no sandbox at all, even with none yet.
	e.must(204, "DELETE", "/v1/sandboxes/"+second, prod, nil)
	e.must(204, "DELETE", "/v1/sandboxes/"+first, prod, nil)
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 0}); err != nil {
		t.Fatal(err)
	}
	if st, out := e.do("POST", "/v1/sandboxes", prod, m{"name": "X"}); st != 409 || !strings.Contains(out["detail"].(string), "no incluye sandbox") {
		t.Fatalf("plan without sandbox = %d %v", st, out)
	}
	if err := e.db.SetTenantPlan(ctx, first, "test", store.Plan{Sandboxes: 1}); err == nil {
		t.Fatal("a plan was set on a sandbox")
	}
	var left int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE parent_id='store-a'`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d sandbox tenants left", left)
	}
}

// TestSandboxSchedule: a publish set for later runs once, at its time, with
// the sandbox as it is then; it can be canceled; permissions count again.
func TestSandboxSchedule(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := sandboxOwner(t, e)
	sbx := newSandbox(t, e, owner, "Lanzamiento")
	inside := e.must(200, "POST", "/v1/sandboxes/"+sbx+"/enter", owner, nil)["token"].(string)
	e.must(200, "PUT", "/v1/products/P1", inside, m{"handle": "p1", "title": "Título programado", "variants": []m{{"sku": "S1", "title": "Única"}}})

	soon := time.Now().Add(20 * time.Second).UTC().Format(time.RFC3339)
	if st, _ := e.do("POST", "/v1/sandbox/publish", inside, m{"areas": []string{"catalog"}, "runAt": soon}); st != 400 {
		t.Fatalf("schedule in 20 s = %d (minimum is a minute)", st)
	}
	later := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if st, _ := e.do("POST", "/v1/sandbox/publish", e.adminA, m{"areas": []string{"catalog"}, "runAt": later}); st != 403 {
		t.Fatalf("an API key schedules = %d", st)
	}
	a := e.must(202, "POST", "/v1/sandboxes/"+sbx+"/publish", inside, m{"areas": []string{"catalog"}, "runAt": later})["schedule"].(map[string]any)
	b := e.must(202, "POST", "/v1/sandboxes/"+sbx+"/publish", inside, m{"areas": []string{"catalog"}, "runAt": later})["schedule"].(map[string]any)
	if a["status"] != "pending" || a["sandbox"] != "Lanzamiento" {
		t.Fatalf("schedule = %v", a)
	}
	// Nothing changes before its time.
	if err := e.server.RunSandboxSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	var title string
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id='store-a' AND id='P1'`).Scan(&title)
	if title == "Título programado" {
		t.Fatal("published before its time")
	}
	// Cancel one; canceling again says it is over.
	e.must(204, "DELETE", "/v1/sandbox/schedules/"+b["id"].(string), inside, nil)
	if st, out := e.do("DELETE", "/v1/sandbox/schedules/"+b["id"].(string), inside, nil); st != 409 || out["code"] != "already_ran" {
		t.Fatalf("cancel twice = %d %v", st, out)
	}
	if st, _ := e.do("DELETE", "/v1/sandbox/schedules/"+a["id"].(string), e.adminB, nil); st != 404 {
		t.Fatalf("another store cancels = %d", st)
	}

	// Its time comes: three nodes run the job at once; it publishes once.
	if _, err := e.db.System.Exec(ctx, `UPDATE sandbox_schedules SET run_at = now() - interval '1 second' WHERE id=$1`, a["id"]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.server.RunSandboxSchedules(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var pubs int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM sandbox_publishes WHERE sandbox_id=$1`, sbx).Scan(&pubs)
	if pubs != 1 {
		t.Fatalf("%d publishes for one scheduled publish", pubs)
	}
	e.drain()
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id='store-a' AND id='P1'`).Scan(&title)
	if title != "Título programado" {
		t.Fatalf("production title = %q", title)
	}
	items := e.must(200, "GET", "/v1/sandbox/schedules", inside, nil)["items"].([]any)
	var done map[string]any
	for _, x := range items {
		if x.(map[string]any)["id"] == a["id"] {
			done = x.(map[string]any)
		}
	}
	if done["status"] != "done" || done["publishId"] == "" || !strings.Contains(done["result"].(string), "publicados") {
		t.Fatalf("finished schedule = %v", done)
	}
	// It can be reverted like any publish.
	e.must(200, "POST", "/v1/sandbox/publishes/"+done["publishId"].(string)+"/revert", inside, nil)

	// The person who scheduled stops being an administrator: it does not run.
	c := e.must(202, "POST", "/v1/sandbox/publish", inside, m{"areas": []string{"catalog"}, "runAt": later})["schedule"].(map[string]any)
	var memberID string
	_ = e.db.System.QueryRow(ctx, `SELECT id FROM members WHERE tenant_id='store-a' AND email='dueno@a.test'`).Scan(&memberID)
	if _, err := e.db.System.Exec(ctx, `UPDATE members SET is_owner=false, permissions='{}' WHERE tenant_id='store-a' AND id=$1`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE sandbox_schedules SET run_at = now() WHERE id=$1`, c["id"]); err != nil {
		t.Fatal(err)
	}
	if err := e.server.RunSandboxSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	x, _ := e.db.ScheduleByID(ctx, "store-a", c["id"].(string))
	if x.Status != "failed" || !strings.Contains(x.Result, "ya no es administrador") {
		t.Fatalf("schedule of a demoted person = %+v", x)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE members SET is_owner=true WHERE tenant_id='store-a' AND id=$1`, memberID); err != nil {
		t.Fatal(err)
	}

	// A node stopped mid-publish: the schedule is closed, not left running.
	d := e.must(202, "POST", "/v1/sandbox/publish", inside, m{"areas": []string{"catalog"}, "runAt": later})["schedule"].(map[string]any)
	if _, err := e.db.System.Exec(ctx, `UPDATE sandbox_schedules SET status='running', ran_at = now() - interval '20 minutes' WHERE id=$1`, d["id"]); err != nil {
		t.Fatal(err)
	}
	if err := e.server.RunSandboxSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	if x, _ := e.db.ScheduleByID(ctx, "store-a", d["id"].(string)); x.Status != "failed" {
		t.Fatalf("stuck schedule = %+v", x)
	}

	// A plan without scheduling refuses it.
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 1, SandboxSchedule: false}); err != nil {
		t.Fatal(err)
	}
	if st, out := e.do("POST", "/v1/sandbox/publish", inside, m{"areas": []string{"catalog"}, "runAt": later}); st != 409 || out["code"] != "plan_limit" {
		t.Fatalf("schedule off the plan = %d %v", st, out)
	}
	// Deleting the sandbox takes its pending schedules with it.
	e.must(200, "POST", "/v1/sandbox/exit", inside, nil)
	_, _ = e.do("DELETE", "/v1/sandboxes/"+sbx, e.adminA, nil)
	var pending int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM sandbox_schedules WHERE sandbox_id=$1`, sbx).Scan(&pending)
	if pending != 0 {
		t.Fatalf("%d schedules left after deleting the sandbox", pending)
	}
}

// TestSandboxPassword: the storefront asks for a password when one is set;
// the right one opens it for 12 hours, until the password changes.
func TestSandboxPassword(t *testing.T) {
	e := setup(t)
	owner := sandboxOwner(t, e)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	sbx := newSandbox(t, e, owner, "Clientes")
	page := "/s/" + sbx + "/main/c"
	noFollow := func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	get := func(c *http.Client) (int, string) {
		t.Helper()
		res, err := c.Get(e.srv.URL + page)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}
	post := func(c *http.Client, pw string) (int, string) {
		t.Helper()
		res, err := c.PostForm(e.srv.URL+page, url.Values{"sbx_password": {pw}})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}

	// Without a password: no form.
	if st, html := get(http.DefaultClient); st != 403 || strings.Contains(html, "sbx_password") {
		t.Fatalf("private page without password = %d", st)
	}
	if st, _ := e.do("PUT", "/v1/sandboxes/"+sbx+"/password", owner, m{"password": "corta"}); st != 400 {
		t.Fatalf("7-character password = %d", st)
	}
	if v := e.must(200, "PUT", "/v1/sandboxes/"+sbx+"/password", owner, m{"password": "clave-del-sandbox"}); v["hasPassword"] != true {
		t.Fatalf("set password = %v", v)
	}
	if sb := e.must(200, "GET", "/v1/sandboxes/"+sbx, owner, nil)["sandbox"].(map[string]any); sb["hasPassword"] != true {
		t.Fatalf("view = %v", sb)
	}
	if st, html := get(http.DefaultClient); st != 403 || !strings.Contains(html, `name="sbx_password"`) || !strings.Contains(html, `<label for="sbx_password">`) {
		t.Fatalf("password page = %d", st)
	}
	jar, _ := cookiejar.New(nil)
	c := noFollow(jar)
	if st, html := post(c, "equivocada"); st != 403 || !strings.Contains(html, "no es correcta") || !strings.Contains(html, `role="alert"`) {
		t.Fatalf("wrong password = %d", st)
	}
	if st, _ := post(c, "clave-del-sandbox"); st != 303 {
		t.Fatalf("right password = %d", st)
	}
	if st, html := get(c); st != 200 || !strings.Contains(html, "sbx-bar") {
		t.Fatalf("with the password cookie = %d", st)
	}
	// A password cookie opens only its sandbox, and only with that password.
	e.must(200, "PUT", "/v1/sandboxes/"+sbx+"/password", owner, m{"password": "otra-clave-nueva"})
	if st, _ := get(c); st != 403 {
		t.Fatalf("old password's cookie after the change = %d", st)
	}
	// Removing it: the private page again, no form; links still work.
	e.must(200, "PUT", "/v1/sandboxes/"+sbx+"/password", owner, m{"password": ""})
	if st, html := get(http.DefaultClient); st != 403 || strings.Contains(html, "sbx_password") {
		t.Fatalf("after removing the password = %d", st)
	}

	// Guessing is limited: 10 tries a minute per address.
	e.must(200, "PUT", "/v1/sandboxes/"+sbx+"/password", owner, m{"password": "clave-del-sandbox"})
	limited := false
	for i := 0; i < 12; i++ {
		if st, _ := post(noFollow(nil), "intento"); st == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("password guessing was not limited")
	}
	if st, _ := e.do("PUT", "/v1/sandboxes/"+sbx+"/password", e.adminB, m{"password": "de-otra-tienda"}); st != 404 {
		t.Fatalf("another store sets the password = %d", st)
	}
}

// TestSandboxOpsMetrics: Operación › Estado shows each sandbox's numbers.
func TestSandboxOpsMetrics(t *testing.T) {
	e := setup(t)
	owner := sandboxOwner(t, e)
	sbx := newSandbox(t, e, owner, "Medido")
	for i := 0; i < 3; i++ {
		res, err := http.Get(e.srv.URL + "/s/" + sbx + "/main/c") // private: counted as denied
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	inside := e.must(200, "POST", "/v1/sandboxes/"+sbx+"/enter", owner, nil)["token"].(string)
	e.must(200, "GET", "/v1/products?limit=1", inside, nil)

	ops := e.must(200, "GET", "/v1/ops/sandboxes", e.adminA, nil)
	items := ops["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("ops sandboxes = %v", ops)
	}
	o := items[0].(map[string]any)
	node := o["node"].(map[string]any)
	if o["name"] != "Medido" || o["products"].(float64) < 1 || o["copyMs"].(float64) <= 0 || node["denied"].(float64) < 3 || node["requests"].(float64) < 4 || o["expiresAt"] == nil {
		t.Fatalf("sandbox metrics = %v", o)
	}
	// The same numbers from inside the sandbox, and in /metrics.
	if in := e.must(200, "GET", "/v1/ops/sandboxes", inside, nil)["items"].([]any); len(in) != 1 {
		t.Fatalf("ops from inside = %v", in)
	}
	rec := &strings.Builder{}
	e.server.writeSandboxMetrics(rec)
	if !strings.Contains(rec.String(), `apexx_sandbox_requests_total{sandbox="`+sbx+`"}`) {
		t.Fatalf("/metrics lacks the sandbox: %s", rec.String())
	}
	if st, _ := e.do("GET", "/v1/ops/sandboxes", e.sfA, nil); st != 401 && st != 403 {
		t.Fatalf("storefront key reads ops = %d", st)
	}
}

// TestSandboxShutdown: a node being replaced waits for its sandbox work and
// marks what could not end, so nothing stays "copying" or "running".
func TestSandboxShutdown(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := sandboxOwner(t, e)
	sbx := newSandbox(t, e, owner, "Apagado")

	// Work that ends in time is waited for.
	e.server.sbxWork.start("quick", "copy")
	go func() { time.Sleep(300 * time.Millisecond); e.server.sbxWork.done("quick") }()
	t0 := time.Now()
	stuck := &Server{DB: e.db, Log: e.server.Log}
	stuck.sbxWork.start(sbx, "copy") // a copy that never ends on a node shutting down
	if _, err := e.db.System.Exec(ctx, `UPDATE sandboxes SET status='copying' WHERE id=$1`, sbx); err != nil {
		t.Fatal(err)
	}
	e.server.FinishSandboxWork(5 * time.Second)
	if d := time.Since(t0); d < 250*time.Millisecond || d > 3*time.Second {
		t.Fatalf("waited %v for work that ends in 300 ms", d)
	}
	stuck.FinishSandboxWork(200 * time.Millisecond)
	sb, _ := e.db.SandboxByID(ctx, sbx)
	if sb.Status != "failed" || !strings.Contains(sb.Error, "se reinició") {
		t.Fatalf("interrupted copy = %s %q", sb.Status, sb.Error)
	}
	// After shutdown starts, no copy starts here and no schedule is claimed.
	if _, err := e.db.System.Exec(ctx, `UPDATE sandboxes SET status='ready' WHERE id=$1`, sbx); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.do("POST", "/v1/sandboxes/"+sbx+"/refresh", owner, nil); st != 202 {
		t.Fatalf("refresh = %d", st)
	}
	e.server.WaitSandboxCopies()
	if sb, _ := e.db.SandboxByID(ctx, sbx); sb.Status != "failed" || !strings.Contains(sb.Error, "reiniciando") {
		t.Fatalf("copy started on a node shutting down = %s %q", sb.Status, sb.Error)
	}
	x, err := e.db.CreateSchedule(ctx, store.Schedule{ParentID: "store-a", SandboxID: sbx, Areas: []string{"catalog"}, RunAt: time.Now().Add(-time.Second), CreatedBy: "member:x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.server.RunSandboxSchedules(ctx); err != nil {
		t.Fatal(err)
	}
	if y, _ := e.db.ScheduleByID(ctx, "store-a", x.ID); y.Status != "pending" {
		t.Fatalf("a node shutting down claimed a schedule: %s", y.Status)
	}
}

// TestSandboxPublishDerived: the knowledge graph is built on both sides.
// Only what a person decided travels; the same relation with another id
// replaces production's (a revert brings it back); an edge copied from
// production and approved in the sandbox is an update, never a duplicate.
func TestSandboxPublishDerived(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := sandboxOwner(t, e)
	edge := `INSERT INTO kg_edges (tenant_id, id, src_kind, src_id, rel, dst_kind, dst_id, status) VALUES ($1, $2, 'product', $3, $4, 'product', $5, $6)`
	exec := func(args ...any) {
		t.Helper()
		if _, err := e.db.System.Exec(ctx, edge, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("store-a", "edge_same", "P2", "compatible_with", "P3", "proposed") // copied with its id
	sbx := newSandbox(t, e, owner, "Grafo")
	// After the copy, each side builds the same relation with its own id.
	exec("store-a", "edge_prod", "P1", "accessory_of", "P2", "proposed")
	exec(sbx, "edge_sbx", "P1", "accessory_of", "P2", "approved")
	// The sandbox's graph proposes more; production proposes one of its own.
	for i := 0; i < 5; i++ {
		exec(sbx, "edge_p"+strconv.Itoa(i), "P1", "compatible_with", "X"+strconv.Itoa(i), "proposed")
	}
	exec("store-a", "edge_prod2", "P3", "substitute_of", "P1", "proposed")
	// A person approves the copied edge in the sandbox.
	if _, err := e.db.System.Exec(ctx, `UPDATE kg_edges SET status='approved', decided_by='member:x', decided_at=now() WHERE tenant_id=$1 AND id='edge_same'`, sbx); err != nil {
		t.Fatal(err)
	}

	inside := e.must(200, "POST", "/v1/sandboxes/"+sbx+"/enter", owner, nil)["token"].(string)
	diff := e.must(200, "GET", "/v1/sandbox/diff?areas=catalog", inside, nil)["areas"].([]any)[0].(map[string]any)
	if bt := diff["byTable"].(map[string]any); bt["kg_edges"] != 2.0 {
		t.Fatalf("kg_edges changes = %v (the two decisions, not the proposals)", bt)
	}
	pub := e.must(200, "POST", "/v1/sandbox/publish", inside, m{"areas": []string{"catalog"}})["publish"].(map[string]any)
	edgeOf := func(src, rel, dst string) (id, status string, n int) {
		_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM kg_edges WHERE tenant_id='store-a' AND src_id=$1 AND rel=$2 AND dst_id=$3`, src, rel, dst).Scan(&n)
		_ = e.db.System.QueryRow(ctx, `SELECT id, status FROM kg_edges WHERE tenant_id='store-a' AND src_id=$1 AND rel=$2 AND dst_id=$3`, src, rel, dst).Scan(&id, &status)
		return
	}
	if id, st, n := edgeOf("P1", "accessory_of", "P2"); n != 1 || id != "edge_sbx" || st != "approved" {
		t.Fatalf("same relation after publish = %d %s %s", n, id, st)
	}
	if id, st, n := edgeOf("P2", "compatible_with", "P3"); n != 1 || id != "edge_same" || st != "approved" {
		t.Fatalf("copied edge after publish = %d %s %s", n, id, st)
	}
	if _, _, n := edgeOf("P3", "substitute_of", "P1"); n != 1 {
		t.Fatal("a publish deleted a proposal production made on its own")
	}
	var proposals int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM kg_edges WHERE tenant_id='store-a' AND id LIKE 'edge_p_'`).Scan(&proposals)
	if proposals != 0 {
		t.Fatalf("%d sandbox proposals reached production", proposals)
	}

	e.must(200, "POST", "/v1/sandbox/publishes/"+pub["id"].(string)+"/revert", inside, nil)
	if id, st, n := edgeOf("P1", "accessory_of", "P2"); n != 1 || id != "edge_prod" || st != "proposed" {
		t.Fatalf("same relation after revert = %d %s %s", n, id, st)
	}
	if id, st, n := edgeOf("P2", "compatible_with", "P3"); n != 1 || id != "edge_same" || st != "proposed" {
		t.Fatalf("copied edge after revert = %d %s %s", n, id, st)
	}
}

// TestSandboxLight: a light sandbox copies the store without products; the
// team brings the ones it wants, edits them, sends them to another sandbox
// and publishes from either. Products never brought are never touched.
func TestSandboxLight(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := sandboxOwner(t, e)
	count := func(tenant, table string) int {
		var n int
		_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, tenant).Scan(&n)
		return n
	}
	if st, out := e.do("POST", "/v1/sandboxes", owner, m{"name": "Ligero", "scope": "light", "dataMode": "demo"}); st != 400 {
		t.Fatalf("light with test orders = %d %v", st, out)
	}
	created := e.must(202, "POST", "/v1/sandboxes", owner, m{"name": "Ligero", "scope": "light"})
	a := created["sandbox"].(map[string]any)["id"].(string)
	e.server.WaitSandboxCopies()
	e.drain()
	if sb := e.must(200, "GET", "/v1/sandboxes/"+a, owner, nil)["sandbox"].(map[string]any); sb["status"] != "ready" || sb["scope"] != "light" {
		t.Fatalf("light sandbox = %v", sb)
	}
	if count(a, "products") != 0 || count(a, "variants") != 0 || count(a, "prices") != 0 {
		t.Fatal("a light sandbox copied products")
	}
	if count(a, "store_settings") != count("store-a", "store_settings") || count(a, "channels") != count("store-a", "channels") {
		t.Fatal("a light sandbox did not copy the store's configuration")
	}

	// Pick from production; bring two products with everything they carry.
	pick := e.must(200, "GET", "/v1/sandboxes/"+a+"/pick?q=S1", owner, nil)
	if items := pick["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != "P1" || items[0].(map[string]any)["inSandbox"] != false {
		t.Fatalf("pick by SKU = %v", pick)
	}
	if st, out := e.do("POST", "/v1/sandboxes/"+a+"/products", owner, m{"products": []string{"P1", "NOPE"}}); st != 404 || out["code"] != "products_not_found" {
		t.Fatalf("bring a missing product = %d %v", st, out)
	}
	if count(a, "products") != 0 {
		t.Fatal("a failed bring left products")
	}
	res := e.must(200, "POST", "/v1/sandboxes/"+a+"/products", owner, m{"products": []string{"P1", "P2"}})
	if res["products"].(float64) != 2 {
		t.Fatalf("bring = %v", res)
	}
	var prodVariants, sbxVariants, prodPrices, sbxPrices int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM variants WHERE tenant_id='store-a' AND product_id IN ('P1','P2')`).Scan(&prodVariants)
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM variants WHERE tenant_id=$1`, a).Scan(&sbxVariants)
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM prices WHERE tenant_id='store-a' AND sku IN (SELECT sku FROM variants WHERE tenant_id='store-a' AND product_id IN ('P1','P2'))`).Scan(&prodPrices)
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM prices WHERE tenant_id=$1`, a).Scan(&sbxPrices)
	if count(a, "products") != 2 || sbxVariants != prodVariants || sbxPrices != prodPrices || sbxPrices == 0 {
		t.Fatalf("brought %d products, %d/%d variants, %d/%d prices", count(a, "products"), sbxVariants, prodVariants, sbxPrices, prodPrices)
	}
	e.drain()
	if _, ok := e.proj.Tenant(a); !ok {
		t.Fatal("light sandbox not in memory")
	}

	// Nothing changed yet: nothing to publish (products not brought are no
	// deletions).
	inside := e.must(200, "POST", "/v1/sandboxes/"+a+"/enter", owner, nil)["token"].(string)
	if st, out := e.do("POST", "/v1/sandbox/publish", inside, m{"areas": []string{"catalog", "prices", "stock"}}); st != 409 || out["code"] != "nothing_to_publish" {
		t.Fatalf("publish right after bringing = %d %v", st, out)
	}
	// Edit inside; bringing again keeps the edit; reset puts it back.
	e.must(200, "PUT", "/v1/products/P1", inside, m{"handle": "p1", "title": "P1 en prueba", "variants": []m{{"sku": "S1", "title": "Única"}}})
	again := e.must(200, "POST", "/v1/sandboxes/"+a+"/products", inside, m{"products": []string{"P1"}})
	if sk := again["skipped"].([]any); len(sk) != 1 || sk[0] != "P1" {
		t.Fatalf("bring again = %v", again)
	}
	var title string
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id=$1 AND id='P1'`, a).Scan(&title)
	if title != "P1 en prueba" {
		t.Fatalf("bring without reset overwrote the edit: %q", title)
	}
	e.must(200, "POST", "/v1/sandboxes/"+a+"/products", inside, m{"products": []string{"P1"}, "reset": true})
	var prodTitle string
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id='store-a' AND id='P1'`).Scan(&prodTitle)
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id=$1 AND id='P1'`, a).Scan(&title)
	if title != prodTitle {
		t.Fatalf("reset = %q, production %q", title, prodTitle)
	}
	e.must(200, "PUT", "/v1/products/P1", inside, m{"handle": "p1", "title": "P1 en prueba", "variants": []m{{"sku": "S1", "title": "Única"}}})
	e.must(204, "PUT", "/v1/prices/mx/S2", inside, m{"tiers": []m{{"minQty": 1, "unit": m{"amount": 12345, "currency": "MXN"}}}})
	diff := e.must(200, "GET", "/v1/sandbox/diff?areas=catalog,prices", inside, nil)["areas"].([]any)
	cat, pri := diff[0].(map[string]any), diff[1].(map[string]any)
	if cat["deletes"].(float64) != 0 || cat["updates"].(float64) < 1 || pri["updates"].(float64) != 1 || pri["deletes"].(float64) != 0 {
		t.Fatalf("diff of a light sandbox = catalog %v prices %v", cat, pri)
	}

	// A second light sandbox; send both products there as they are.
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 2, SandboxSchedule: true}); err != nil {
		t.Fatal(err)
	}
	owner = sandboxOwnerAgain(t, e)
	b := e.must(202, "POST", "/v1/sandboxes", owner, m{"name": "Segunda ronda", "scope": "light"})["sandbox"].(map[string]any)["id"].(string)
	e.server.WaitSandboxCopies()
	e.drain()
	if st, _ := e.do("POST", "/v1/sandboxes/"+a+"/send", inside, m{"to": "sbx-otra-tienda", "products": []string{"P1"}}); st != 404 {
		t.Fatalf("send to an unknown sandbox = %d", st)
	}
	e.must(200, "POST", "/v1/sandboxes/"+a+"/send", inside, m{"to": b, "products": []string{"P1", "P2"}})
	var bTitle string
	var bPrice int64
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id=$1 AND id='P1'`, b).Scan(&bTitle)
	_ = e.db.System.QueryRow(ctx, `SELECT amount FROM prices WHERE tenant_id=$1 AND sku='S2' AND min_qty=1`, b).Scan(&bPrice)
	if bTitle != "P1 en prueba" || bPrice != 12345 {
		t.Fatalf("sent: title %q price %d", bTitle, bPrice)
	}
	// From the second sandbox the same changes show and publish.
	inB := e.must(200, "POST", "/v1/sandboxes/"+b+"/enter", inside, nil)["token"].(string)
	diffB := e.must(200, "GET", "/v1/sandbox/diff?areas=prices", inB, nil)["areas"].([]any)[0].(map[string]any)
	if diffB["updates"].(float64) != 1 || diffB["deletes"].(float64) != 0 {
		t.Fatalf("diff of the second sandbox = %v", diffB)
	}
	e.must(200, "POST", "/v1/sandbox/publish", inB, m{"areas": []string{"prices"}})
	var prodPrice int64
	_ = e.db.System.QueryRow(ctx, `SELECT amount FROM prices WHERE tenant_id='store-a' AND sku='S2' AND min_qty=1`).Scan(&prodPrice)
	if prodPrice != 12345 {
		t.Fatalf("production price after publishing from the second sandbox = %d", prodPrice)
	}
	if count("store-a", "products") < 3 {
		t.Fatal("a light publish deleted products that were never brought")
	}

	// Refresh keeps the products a light sandbox had, as production has them.
	owner = sandboxOwnerAgain(t, e)
	e.must(202, "POST", "/v1/sandboxes/"+a+"/refresh", owner, nil)
	e.server.WaitSandboxCopies()
	_ = e.db.System.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id=$1 AND id='P1'`, a).Scan(&title)
	if count(a, "products") != 2 || title != prodTitle {
		t.Fatalf("refreshed light sandbox: %d products, title %q", count(a, "products"), title)
	}

	// Size limit: bringing past it is refused and leaves nothing.
	if err := e.db.SetTenantPlan(ctx, "store-a", "test", store.Plan{Sandboxes: 2, SandboxMaxProducts: 2}); err != nil {
		t.Fatal(err)
	}
	if st, out := e.do("POST", "/v1/sandboxes/"+a+"/products", owner, m{"products": []string{"P3"}}); st != 409 || out["code"] != "plan_limit" {
		t.Fatalf("bring past the size limit = %d %v", st, out)
	}
	if count(a, "products") != 2 {
		t.Fatal("a refused bring left products")
	}
	// Another store never reaches these sandboxes.
	if st, _ := e.do("POST", "/v1/sandboxes/"+a+"/products", e.adminB, m{"products": []string{"P1"}}); st != 404 {
		t.Fatalf("another store brings into the sandbox = %d", st)
	}
}

// sandboxOwnerAgain signs store-a's owner in again (entering a sandbox ends
// the session that entered).
func sandboxOwnerAgain(t *testing.T, e *env) string {
	t.Helper()
	_, login := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "dueno@a.test", "password": "owner-password-123"})
	return login["token"].(string)
}

// TestSandboxStockFromTestOrders: what test orders of a sandbox reserve or
// take is never published; what a person sets is, and production's own
// reservations are never touched by a publish or a revert.
func TestSandboxStockFromTestOrders(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := sandboxOwner(t, e)
	sbx := newSandbox(t, e, owner, "Existencias")
	inside := e.must(200, "POST", "/v1/sandboxes/"+sbx+"/enter", owner, nil)["token"].(string)
	level := func(tenant, sku string) (loc string, onHand, reserved int64) {
		_ = e.db.System.QueryRow(ctx, `SELECT location_id, on_hand, reserved FROM stock_levels WHERE tenant_id=$1 AND sku=$2 ORDER BY location_id LIMIT 1`, tenant, sku).Scan(&loc, &onHand, &reserved)
		return
	}
	stockDiff := func() map[string]any {
		return e.must(200, "GET", "/v1/sandbox/diff?areas=stock", inside, nil)["areas"].([]any)[0].(map[string]any)
	}
	loc1, before1, _ := level(sbx, "S1")

	// A test purchase: reserves, then (paid) takes the units.
	sf, _ := e.db.CreateAPIKey(ctx, sbx, "sf", "storefront")
	body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}}, "customer": m{"email": "prueba@example.com", "name": "Prueba"}, "paymentMethod": "pm_card_visa"}
	if c, o := e.do("POST", "/v1/checkout", sf, body, "Idempotency-Key", "stock-test-1"); c != 201 {
		t.Fatalf("test checkout = %d %v", c, o)
	}
	if d := stockDiff(); d["updates"].(float64)+d["inserts"].(float64)+d["deletes"].(float64) != 0 {
		t.Fatalf("a test order's reservation shows as a stock change: %v", d)
	}
	var holder string
	_ = e.db.System.QueryRow(ctx, `SELECT holder FROM reservations WHERE tenant_id=$1 LIMIT 1`, sbx).Scan(&holder)
	if holder != "" {
		if err := e.db.Tx(ctx, sbx, func(tx pgx.Tx) error { return store.CommitTx(ctx, tx, sbx, holder) }); err != nil {
			t.Fatal(err)
		}
	}
	if _, after, _ := level(sbx, "S1"); after != before1-2 {
		t.Fatalf("the test sale did not take the units in the sandbox: %d -> %d", before1, after)
	}
	if d := stockDiff(); d["updates"].(float64) != 0 {
		t.Fatalf("a test sale shows as a stock change: %v", d)
	}

	// A person sets stock: that is published, as written.
	loc2, _, _ := level(sbx, "S2")
	e.must(204, "PUT", "/v1/inventory/S2/"+loc2, inside, m{"onHand": 77})
	e.must(204, "PUT", "/v1/inventory/S1/"+loc1, inside, m{"onHand": 40}) // after the test sale
	if d := stockDiff(); d["updates"].(float64) != 2 {
		t.Fatalf("stock set by a person = %v", d)
	}
	// Production has live reservations of its own.
	if _, err := e.db.System.Exec(ctx, `UPDATE stock_levels SET reserved = 3 WHERE tenant_id='store-a' AND sku='S2' AND location_id=$1`, loc2); err != nil {
		t.Fatal(err)
	}
	pub := e.must(200, "POST", "/v1/sandbox/publish", inside, m{"areas": []string{"stock"}})["publish"].(map[string]any)
	_, on1, _ := level("store-a", "S1")
	_, on2, res2 := level("store-a", "S2")
	if on1 != 40 || on2 != 77 || res2 != 3 {
		t.Fatalf("production after publishing stock: S1 %d (want 40), S2 %d (want 77), reserved %d (want 3)", on1, on2, res2)
	}
	// Production reserves more meanwhile; a revert gives stock back and
	// leaves its reservations alone.
	if _, err := e.db.System.Exec(ctx, `UPDATE stock_levels SET reserved = 5 WHERE tenant_id='store-a' AND sku='S2' AND location_id=$1`, loc2); err != nil {
		t.Fatal(err)
	}
	e.must(200, "POST", "/v1/sandbox/publishes/"+pub["id"].(string)+"/revert", inside, nil)
	_, on2, res2 = level("store-a", "S2")
	_, on1, _ = level("store-a", "S1")
	if on1 != before1 || res2 != 5 {
		t.Fatalf("after revert: S1 %d (want %d), S2 reserved %d (want 5)", on1, before1, res2)
	}
	_ = on2
}
