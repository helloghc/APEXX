package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (e *env) shop(path string) string {
	e.t.Helper()
	r, err := http.Get(e.srv.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func (e *env) search(q string) string { return e.shop("/s/store-a/main/c?q=" + url.QueryEscape(q)) }

func (e *env) publish() {
	e.t.Helper()
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
}

// The storefront finds products by meaning, not only by their words, and
// stays lexical when the store turns vectors off.
func TestHybridSearchInStore(t *testing.T) {
	e := setup(t)
	e.product("chamarra", "Chamarra impermeable", "Capucha y costuras selladas para lluvia intensa")
	e.product("botas", "Botas de hule", "No dejan pasar el agua en días de lluvia")
	e.product("taladro", "Taladro percutor", "Para concreto y madera")
	e.embed()
	e.publish()
	page := e.search("algo para la lluvia")
	t.Logf("chamarra=%v botas=%v taladro=%v", strings.Contains(page, "Chamarra impermeable"), strings.Contains(page, "Botas de hule"), strings.Contains(page, "Taladro percutor"))
	if !strings.Contains(page, "Chamarra impermeable") || !strings.Contains(page, "Botas de hule") || strings.Contains(page, "Taladro percutor") {
		t.Fatalf("hybrid search: %s", snippet(page, "browse-main"))
	}
	// Exact words still win: the product named first.
	page = e.search("taladro percutor")
	if i, j := strings.Index(page, "Taladro percutor"), strings.Index(page, "Chamarra impermeable"); i < 0 || (j >= 0 && j < i) {
		t.Fatal("lexical match is not first")
	}
	// Nonsense finds nothing.
	if !strings.Contains(e.search("zzqxv"), "No encontramos productos") {
		t.Fatal("nonsense found products")
	}
	// Turned off: lexical only, at once (the cached page is not reused).
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, map[string]any{"semanticOff": true})
	if _, err := e.server.RunLearning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if page := e.search("algo para la lluvia"); !strings.Contains(page, "No encontramos productos") {
		t.Fatal("vectors off but the search still used them")
	}
	// Zero-result terms get a suggestion once vectors are back on.
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, map[string]any{"semanticOff": false})
	_, _ = e.server.RunLearning(context.Background())
	terms := e.must(200, "GET", "/v1/search/terms", e.adminA, nil)
	var sug map[string]any
	for _, z := range terms["noResults"].([]any) {
		if z.(map[string]any)["term"] == "algo para la lluvia" {
			sug, _ = z.(map[string]any)["suggest"].(map[string]any)
		}
	}
	if sug == nil || len(sug["products"].([]any)) == 0 {
		t.Fatalf("no suggestion for a zero-result term: %v", terms["noResults"])
	}
}

// "playeras rojas baratas" is read as text, a color and an order.
func TestIntentFiltersInStore(t *testing.T) {
	e := setup(t)
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, map[string]any{"id": "color", "name": "Color", "kind": "select", "options": []string{"Rojo", "Azul"}, "filterable": true})
	for _, p := range []struct{ h, title, color string }{{"roja", "Playera básica roja", "Rojo"}, {"azul", "Playera básica azul", "Azul"}} {
		e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": p.h, "title": p.title, "status": "active", "attributes": map[string]string{"color": p.color},
			"variants": []map[string]any{{"sku": "SKU-" + p.h, "title": "Única"}}})
	}
	e.drain()
	e.publish()
	page := e.search("playeras rojas baratas")
	if !strings.Contains(page, "Playera básica roja") || strings.Contains(page, "Playera básica azul") || !strings.Contains(page, "Entendimos tu búsqueda con:") || !strings.Contains(snippet(page, "data-understood"), "Rojo") {
		t.Fatalf("intent not applied: %s", snippet(page, "Entendimos"))
	}
	// A color the store does not have leaves the plain search.
	if page := e.search("playera verde"); strings.Contains(page, "Entendimos") {
		t.Fatal("intent applied without a match")
	}
}

// A new product with no sales or visits still recommends related products.
func TestSimilarRecommendations(t *testing.T) {
	e := setup(t)
	e.product("chamarra", "Chamarra impermeable", "Para lluvia intensa")
	e.product("poncho", "Poncho impermeable para lluvia", "Cubre la mochila")
	e.product("taladro", "Taladro percutor", "Para concreto")
	e.embed()
	if _, err := e.server.RunLearning(context.Background()); err != nil {
		t.Fatal(err)
	}
	body := e.shop("/s/store-a/api/recommendations?p=chamarra")
	if !strings.Contains(body, "Poncho impermeable") || strings.Contains(body, "Taladro") {
		t.Fatalf("similar products: %s", body)
	}
}

func snippet(s, near string) string {
	i := strings.Index(s, near)
	if i < 0 {
		if len(s) > 400 {
			return s[:400]
		}
		return s
	}
	return s[max(0, i-200):min(len(s), i+300)]
}
