package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/seed"
)

func newServer(n int) http.Handler {
	cat, inv, pr := catalog.New(nil), inventory.New(nil), pricing.New()
	seed.Load(n, cat, inv, pr)
	return (&Server{Catalog: cat, Inventory: inv, Cart: &cart.Service{
		Catalog: cat, Inventory: inv, Pricing: pr, Promos: seed.Promotions(), Channels: seed.Channels()}}).Routes()
}

func do(t testing.TB, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, &b))
	return rec
}

func TestCursorWalksWholeCatalog(t *testing.T) {
	h := newServer(3000) // more than VTEX's 2,500 ceiling
	seen, cursor := 0, ""
	for {
		rec := do(t, h, "GET", "/v1/products?limit=250&fields=id&cursor="+cursor, nil)
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var page struct {
			Items      []map[string]any `json:"items"`
			NextCursor string           `json:"nextCursor"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &page)
		seen += len(page.Items)
		if len(page.Items[0]) != 1 {
			t.Fatalf("projection kept %d fields, want 1", len(page.Items[0]))
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if seen != 3000 {
		t.Fatalf("walked %d products, want 3000", seen)
	}
}

func TestErrorsAreProblemJSON(t *testing.T) {
	h := newServer(1)
	for _, p := range []string{"/v1/products/nope", "/v1/inventory/nope", "/v1/nope"} {
		rec := do(t, h, "GET", p, nil)
		if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	rec := do(t, h, "POST", "/v1/carts/simulate", map[string]any{"items": []any{}})
	if rec.Code != 400 || !bytes.Contains(rec.Body.Bytes(), []byte(`"validation_failed"`)) {
		t.Fatalf("simulate validation: %d %s", rec.Code, rec.Body)
	}
}

func TestSimulateTotalsAddUp(t *testing.T) {
	h := newServer(10)
	rec := do(t, h, "POST", "/v1/carts/simulate", cart.Request{Channel: "mx", PostalCode: "53129",
		Items: []cart.Item{{SKU: "SKU-000002-1", Qty: 3}, {SKU: "SKU-000001-1", Qty: 500}, {SKU: "missing", Qty: 1}}})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var r cart.Result
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	tt := r.Totals
	if tt.Grand.Amount != tt.Items.Amount-tt.Discounts.Amount+tt.Shipping.Amount {
		t.Fatalf("totals do not add up: %+v", tt)
	}
	codes := map[string]bool{}
	for _, m := range r.Messages {
		codes[m.Code] = true
	}
	for _, c := range []string{"max_qty_per_item", "insufficient_stock", "item_not_found"} {
		if !codes[c] {
			t.Errorf("missing message %q in %+v", c, r.Messages)
		}
	}
	applied := false
	for _, p := range r.Promotions {
		if p.ID == "3x2-calcetas" && p.Applied {
			applied = true
		}
		if p.Reason == "" {
			t.Errorf("promotion %s has no reason", p.ID)
		}
	}
	if !applied {
		t.Fatalf("3x2 not applied: %+v", r.Promotions)
	}
}

func TestETagNotModified(t *testing.T) {
	h := newServer(1)
	rec := do(t, h, "GET", "/v1/products/P-000001", nil)
	req := httptest.NewRequest("GET", "/v1/products/P-000001", nil)
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("want 304, got %d", rec2.Code)
	}
}

func BenchmarkSimulate(b *testing.B) {
	h := newServer(18500)
	body, _ := json.Marshal(cart.Request{Channel: "mx", PostalCode: "53129",
		Items: []cart.Item{{SKU: "SKU-000002-1", Qty: 3}, {SKU: "SKU-001873-1", Qty: 1}, {SKU: "SKU-001871-2", Qty: 3}}})
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/carts/simulate", bytes.NewReader(body)))
			if rec.Code != 200 {
				b.Fatal(rec.Code)
			}
		}
	})
}

func BenchmarkListProducts50(b *testing.B) {
	h := newServer(18500)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/products?limit=50&fields=id,title,available", nil))
		}
	})
}
