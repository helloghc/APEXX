package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/store"
)

// totpNow computes the current code of a base32 secret (an authenticator app).
func totpNow(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	h := hmac.New(sha1.New, raw)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// TestOperators: the superadmin is its own identity, with two-step
// verification always on; it sets each store's plan, and the store sees it.
func TestOperators(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, _, _, err := CreateOperator(ctx, e.db, e.keys, "ops@apexx.test", "Ops", "corta"); err == nil {
		t.Fatal("an operator with a short password was created")
	}
	op, secret, uri, err := CreateOperator(ctx, e.db, e.keys, "Ops@Apexx.test", "Ops", "contraseña-operador-1")
	if err != nil || op.Email != "ops@apexx.test" || !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("create operator = %v %q %v", op, uri, err)
	}
	if _, _, _, err := CreateOperator(ctx, e.db, e.keys, "ops@apexx.test", "Otra", "contraseña-operador-2"); err == nil {
		t.Fatal("two operators with the same email")
	}

	// A store's keys and sessions are not operators, whatever they can do.
	owner := sandboxOwner(t, e)
	for _, k := range []string{e.adminA, e.sfA, owner} {
		if st, _ := e.do("GET", "/v1/operator/stores", k, nil); st != 403 {
			t.Fatalf("store credential reads the superadmin: %d", st)
		}
	}
	if st, _ := e.do("GET", "/v1/operator/stores", "", nil); st != 401 {
		t.Fatalf("no credential = %d", st)
	}
	// Sign in: wrong password, wrong code, right; the same code twice no.
	now := time.Now()
	login := func(pw, code string) (int, map[string]any) {
		return e.do("POST", "/v1/operator/login", "", m{"email": "ops@apexx.test", "password": pw, "code": code})
	}
	if st, _ := login("otra-contraseña", totpNow(t, secret, now)); st != 401 {
		t.Fatalf("wrong password = %d", st)
	}
	if st, _ := login("contraseña-operador-1", "000000"); st != 401 {
		t.Fatalf("wrong code = %d", st)
	}
	code := totpNow(t, secret, now)
	st, out := login("contraseña-operador-1", code)
	if st != 200 {
		t.Fatalf("operator sign-in = %d %v", st, out)
	}
	tok := out["token"].(string)
	if st, _ := login("contraseña-operador-1", code); st != 401 {
		t.Fatalf("the same code worked twice: %d", st)
	}
	if me := e.must(200, "GET", "/v1/operator/me", tok, nil)["operator"].(map[string]any); me["email"] != "ops@apexx.test" || me["passwordHash"] != nil {
		t.Fatalf("me = %v", me)
	}
	// The operator's token is not a store credential either.
	if st, _ := e.do("GET", "/v1/products", tok, nil); st != 401 {
		t.Fatalf("operator token reads a store: %d", st)
	}

	// Stores with plan, team, products and sandboxes (with their size).
	created := e.must(202, "POST", "/v1/sandboxes", owner, m{"name": "Medida", "scope": "light"})
	sbx := created["sandbox"].(map[string]any)["id"].(string)
	e.server.WaitSandboxCopies()
	e.must(200, "POST", "/v1/sandboxes/"+sbx+"/products", owner, m{"products": []string{"P1"}})
	list := e.must(200, "GET", "/v1/operator/stores?q=store-a", tok, nil)["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("stores = %v", list)
	}
	sa := list[0].(map[string]any)
	sbxs := sa["sandboxes"].([]any)
	if sa["plan"].(map[string]any)["sandboxes"] != 1.0 || sa["products"].(float64) < 3 || sa["members"].(float64) < 1 || len(sbxs) != 1 {
		t.Fatalf("store-a for operators = %v", sa)
	}
	if s0 := sbxs[0].(map[string]any); s0["name"] != "Medida" || s0["scope"] != "light" || s0["products"] != 1.0 || s0["sizeMB"] == nil {
		t.Fatalf("sandbox for operators = %v", s0)
	}

	// Set the plan: the store gets it and sees who changed it.
	if st, _ := e.do("PUT", "/v1/operator/stores/store-a/plan", tok, m{"sandboxes": 99}); st != 400 {
		t.Fatalf("99 sandboxes = %d", st)
	}
	if st, _ := e.do("PUT", "/v1/operator/stores/"+sbx+"/plan", tok, m{"sandboxes": 2}); st != 404 {
		t.Fatalf("plan on a sandbox = %d", st)
	}
	e.must(200, "PUT", "/v1/operator/stores/store-a/plan", tok, m{"plan": "pro", "sandboxes": 3, "sandboxMaxProducts": 1000, "sandboxDemoData": true, "sandboxSchedule": true})
	if p, _ := e.db.TenantPlan(ctx, "store-a"); p.Plan != "pro" || p.Sandboxes != 3 || p.SandboxMaxProducts != 1000 {
		t.Fatalf("plan = %+v", p)
	}
	var who string
	_ = e.db.System.QueryRow(ctx, `SELECT actor FROM audit_log WHERE tenant_id='store-a' AND action='plan.change' ORDER BY seq DESC LIMIT 1`).Scan(&who)
	if who != "operator:ops@apexx.test" {
		t.Fatalf("store's activity for the plan change = %q", who)
	}
	if lg := e.must(200, "GET", "/v1/operator/log", tok, nil)["items"].([]any); len(lg) < 2 {
		t.Fatalf("operator log = %v", lg)
	}
	// The store sees its new plan.
	owner = sandboxOwnerAgain(t, e)
	if p := e.must(200, "GET", "/v1/sandboxes", owner, nil)["plan"].(map[string]any); p["sandboxes"] != 3.0 {
		t.Fatalf("store's plan = %v", p)
	}

	// Sign out; a disabled operator's sessions end.
	e.must(204, "POST", "/v1/operator/logout", tok, nil)
	if st, _ := e.do("GET", "/v1/operator/me", tok, nil); st != 401 {
		t.Fatalf("after sign-out = %d", st)
	}
	_, out = login("contraseña-operador-1", totpNow(t, secret, now.Add(30*time.Second)))
	tok2, _ := out["token"].(string)
	if err := e.db.DisableOperator(ctx, "ops@apexx.test"); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.do("GET", "/v1/operator/me", tok2, nil); st != 401 {
		t.Fatalf("disabled operator's session = %d", st)
	}
	// Guessing is limited.
	limited := false
	for i := 0; i < 8; i++ {
		if st, _ := e.do("POST", "/v1/operator/login", "", m{"email": "nadie@apexx.test", "password": "x", "code": "123456"}); st == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("operator sign-in guessing was not limited")
	}
	_ = store.DefaultPlan
}
