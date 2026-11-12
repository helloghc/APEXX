package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/helloghc/apexx/internal/ai"
)

// The panel copilots return the system's own structures, validated, and
// save nothing: segment rules with today's count, a draft promotion with
// the engine's summary, an order's summary and a reply by its state.
func TestPanelCopilots(t *testing.T) {
	e := setup(t)
	seg := e.must(200, "POST", "/v1/ai/segment", e.adminA, map[string]any{"q": "clientes que compraron más de 3 veces y aceptan correos"})
	if seg["understood"] != true || len(seg["explain"].([]any)) != 2 || seg["count"] == nil {
		t.Fatalf("segment: %v", seg)
	}
	// The rules are the ones the segment screen saves: the usual endpoint accepts them.
	e.must(200, "POST", "/v1/customer-groups/preview", e.adminA, map[string]any{"rules": seg["rules"]})
	if s := e.must(200, "POST", "/v1/ai/segment", e.adminA, map[string]any{"q": "los mejores"}); s["understood"] != false {
		t.Fatalf("nonsense segment: %v", s)
	}

	pr := e.must(200, "POST", "/v1/ai/promotion", e.adminA, map[string]any{"q": "envío gratis desde $999"})
	if pr["understood"] != true || len(pr["problems"].([]any)) != 0 || !strings.Contains(strings.ToLower(pr["summary"].(string)), "envío") {
		t.Fatalf("promotion: %v", pr)
	}
	// It is a draft: creating it through the usual endpoint keeps it inactive.
	p := pr["promotion"].(map[string]any)
	if p["status"] != "draft" {
		t.Fatalf("not a draft: %v", p)
	}
	created := e.must(201, "POST", "/v1/promotions", e.adminA, p)
	if st := created["status"]; st != "draft" {
		if pp, ok := created["promotion"].(map[string]any); !ok || pp["status"] != "draft" {
			t.Fatalf("created promotion is not a draft: %v", created)
		}
	}

	_, o := e.do("POST", "/v1/checkout", e.sfA, map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 2}},
		"customer": map[string]any{"email": "ana@cliente.example", "name": "Ana López"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "copilot-order-1")
	oid, _ := o["id"].(string)
	if oid == "" {
		t.Fatalf("checkout: %v", o)
	}
	a := e.must(200, "GET", "/v1/ai/orders/"+oid, e.adminA, nil)
	if !strings.Contains(a["summary"].(string), "2 × Producto 1") || !strings.HasPrefix(a["reply"].(string), "Hola, Ana.") {
		t.Fatalf("order copilot: %v", a)
	}
	rr := e.must(200, "POST", "/v1/ai/return-reason", e.adminA, map[string]any{"text": "Me quedó chica la talla"})
	if rr["code"] != "size" {
		t.Fatalf("return reason: %v", rr)
	}
	// A storefront key cannot use any of them; another store cannot read this order.
	for _, path := range []string{"/v1/ai/segment", "/v1/ai/promotion", "/v1/ai/return-reason"} {
		if c, _ := e.do("POST", path, e.sfA, map[string]any{"q": "x", "text": "x"}); c != 403 {
			t.Errorf("%s with a storefront key: %d", path, c)
		}
	}
	if c, _ := e.do("GET", "/v1/ai/orders/"+oid, e.adminB, nil); c != 404 {
		t.Fatalf("other store read the order: %d", c)
	}
}

// Writing needs a model: without one it says so; with one, the texts come
// back as options, numbers the merchant did not give are flagged, and the
// cost is recorded. Permission follows the area the text is for.
func TestWriteCopilot(t *testing.T) {
	e := setup(t)
	r := e.must(200, "POST", "/v1/ai/write", e.adminA, map[string]any{"kind": "email_subject", "about": "Nueva colección de otoño"})
	if r["available"] != false || !strings.Contains(r["message"].(string), "llave de Claude") {
		t.Fatalf("without a model: %v", r)
	}
	var reply atomic.Value
	reply.Store("Llegó el otoño\\nTu nuevo favorito te espera\\n30% en abrigos")
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "m", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
			"content": []map[string]any{{"type": "text", "text": strings.ReplaceAll(reply.Load().(string), "\\n", "\n")}}, "stop_reason": "end_turn",
			"usage": map[string]any{"input_tokens": 200, "output_tokens": 40}})
	}))
	defer fake.Close()
	e.server.LLM = ai.NewClaude("k", fake.URL, "claude-sonnet-5-5")
	r = e.must(200, "POST", "/v1/ai/write", e.adminA, map[string]any{"kind": "email_subject", "about": "Nueva colección de otoño"})
	if r["available"] != true || len(r["options"].([]any)) != 3 {
		t.Fatalf("with a model: %v", r)
	}
	if chk := r["check"].([]any); len(chk) != 1 || chk[0] != "30" {
		t.Fatalf("an invented discount must be flagged: %v", r["check"])
	}
	var n int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM ai_usage WHERE tenant_id='store-a' AND feature='write:email_subject'`).Scan(&n)
	if n != 1 {
		t.Fatalf("usage not recorded")
	}
	if c, _ := e.do("POST", "/v1/ai/write", e.adminA, map[string]any{"kind": "poema", "about": "x y z"}); c != 400 {
		t.Fatalf("unknown kind: %d", c)
	}
	if c, _ := e.do("POST", "/v1/ai/write", e.sfA, map[string]any{"kind": "email_subject", "about": "otoño"}); c != 403 {
		t.Fatalf("storefront key: %d", c)
	}
}
