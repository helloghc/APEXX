package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/helloghc/apexx/internal/carriers"
)

// Automations: queued with the order event, conditions respected, actions
// done once (a failure resumes at the failed action), webhook signed and
// free of personal data, and nothing visible to another store.
func TestAutomations(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var mu sync.Mutex
	fail := true
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		got = append(got, r.Header.Get("X-Apexx-Timestamp")+"|"+r.Header.Get("X-Apexx-Signature")+"|"+string(b))
		if fail {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	defer hook.Close()
	automationHTTPClient = hook.Client()
	defer func() { automationHTTPClient = nil }()

	for _, bad := range []m{
		{"name": "x", "trigger": "order.exploded", "actions": []m{{"kind": "note", "text": "a"}}},
		{"name": "x", "trigger": "payment.captured", "actions": []m{}},
		{"name": "x", "trigger": "payment.captured", "actions": []m{{"kind": "rm_rf"}}},
	} {
		if st, _ := e.do("POST", "/v1/automations", e.adminA, bad); st != 400 {
			t.Fatalf("invalid automation %v accepted: %d", bad, st)
		}
	}
	a := e.must(200, "POST", "/v1/automations", e.adminA, m{"name": "Clientes VIP", "trigger": "payment.captured", "active": true,
		"conditions": m{"minTotal": 25000},
		"actions":    []m{{"kind": "note", "text": "Pedido VIP {{pedido.numero}}"}, {"kind": "tag_customer", "tag": "vip"}, {"kind": "webhook", "url": hook.URL + "/vip"}}})
	aid := a["id"].(string)
	if l := e.must(200, "GET", "/v1/automations", e.adminB, nil); len(l["items"].([]any)) != 0 {
		t.Fatalf("store B sees store A's automations: %v", l)
	}
	n := 0
	buy := func(qty int) string {
		n++
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": qty}},
			"customer": m{"email": "vip@cliente.example", "name": "Cliente VIP"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("automation-%d", n))
		if st != 201 {
			t.Fatalf("checkout = %d %v", st, o)
		}
		return o["id"].(string)
	}
	small, big := buy(1), buy(3)
	if _, err := e.server.RunAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	runs := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, x := range e.must(200, "GET", "/v1/automations/"+aid+"/runs", e.adminA, nil)["items"].([]any) {
			r := x.(map[string]any)
			out[r["orderId"].(string)] = r
		}
		return out
	}
	rs := runs()
	if len(rs) != 2 || rs[small]["status"] != "skipped" {
		t.Fatalf("runs = %v", rs)
	}
	// The webhook failed: note and tag are done, the run waits to retry at the webhook.
	if rs[big]["status"] != "pending" || rs[big]["step"].(float64) != 2 || !strings.Contains(rs[big]["lastError"].(string), "503") {
		t.Fatalf("big run = %v", rs[big])
	}
	notes := func(id string) int {
		var c int
		_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM order_notes WHERE tenant_id='store-a' AND order_id=$1`, id).Scan(&c)
		return c
	}
	if notes(big) != 1 || notes(small) != 0 {
		t.Fatalf("notes big=%d small=%d", notes(big), notes(small))
	}
	// The receiver comes back: the retry does only the webhook.
	mu.Lock()
	fail = false
	mu.Unlock()
	_, _ = e.db.System.Exec(ctx, `UPDATE automation_runs SET next_attempt=now() WHERE tenant_id='store-a'`)
	if _, err := e.server.RunAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if r := runs()[big]; r["status"] != "done" || notes(big) != 1 {
		t.Fatalf("after retry: %v notes=%d", r, notes(big))
	}
	// The customer got the tag; the webhook was signed and carried no personal data.
	var tags string
	_ = e.db.System.QueryRow(ctx, `SELECT array_to_string(tags, ',') FROM customers WHERE tenant_id='store-a' AND id=(SELECT customer_id FROM orders WHERE tenant_id='store-a' AND id=$1)`, big).Scan(&tags)
	if !strings.Contains(tags, "vip") {
		t.Fatalf("customer tags = %q", tags)
	}
	mu.Lock()
	last := got[len(got)-1]
	mu.Unlock()
	parts := strings.SplitN(last, "|", 3)
	key, _ := e.keys.Blind("store-a", "automation-signing", aid)
	if carriers.Sign([]byte(hex.EncodeToString(key)), parts[0], []byte(parts[2])) != parts[1] {
		t.Fatal("webhook signature does not verify")
	}
	if strings.Contains(parts[2], "vip@cliente.example") || strings.Contains(parts[2], "Cliente VIP") {
		t.Fatalf("webhook leaked personal data: %s", parts[2])
	}
	// The automation's own note did not start another run, and nothing ran twice.
	var total int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE tenant_id='store-a'`).Scan(&total)
	if total != 2 {
		t.Fatalf("runs total = %d", total)
	}
	// Store B cannot read the runs or retry them.
	if l := e.must(200, "GET", "/v1/automations/"+aid+"/runs", e.adminB, nil); len(l["items"].([]any)) != 0 {
		t.Fatalf("store B reads store A's runs: %v", l)
	}
	// Pausing it: new orders do not run it.
	a["active"] = false
	e.must(200, "PUT", "/v1/automations/"+aid, e.adminA, a)
	buy(3)
	_, _ = e.server.RunAutomations(ctx)
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE tenant_id='store-a'`).Scan(&total)
	if total != 2 {
		t.Fatalf("a paused automation ran: %d runs", total)
	}
}
