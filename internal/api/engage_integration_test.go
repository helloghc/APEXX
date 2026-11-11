package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Search rules apply at once, are private to each store, and reviews only go
// public after moderation.
func TestSearchRulesAndReviews(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	e.drain()
	suggest := func(q string) map[string]any {
		st, body := e.do("GET", "/s/store-a/api/suggest?q="+q, "", nil)
		if st != 200 {
			t.Fatalf("suggest %s = %d %v", q, st, body)
		}
		return body
	}
	if n := suggest("articulo")["total"].(float64); n != 0 {
		t.Fatalf("unknown word found %v products", n)
	}
	// Invalid rules are refused with a reason.
	if st, body := e.do("PUT", "/v1/search/config", e.adminA, m{"redirects": []m{{"term": "x", "url": "javascript:alert(1)"}}}); st != 400 {
		t.Fatalf("invalid redirect accepted: %d %v", st, body)
	}
	e.must(200, "PUT", "/v1/search/config", e.adminA, m{
		"synonyms":  [][]string{{"producto", "Artículo"}},
		"redirects": []m{{"term": "Envíos", "url": "/politicas/envios"}},
		"pins":      []m{{"term": "producto", "productIds": []string{"P3"}}},
		"hidden":    []string{"P2"},
	})
	e.drain()
	got := suggest("articulo")
	if got["total"].(float64) != 2 { // P2 is hidden
		t.Fatalf("synonym + hidden: total = %v", got["total"])
	}
	if first := got["products"].([]any)[0].(map[string]any)["id"]; first != "P3" {
		t.Fatalf("pinned product must come first, got %v", first)
	}
	// The other store keeps its own (empty) rules.
	if cfg := e.must(200, "GET", "/v1/search/config", e.adminB, nil)["config"].(map[string]any); len(cfg["synonyms"].([]any)) != 0 {
		t.Fatalf("search rules leaked to another store: %v", cfg)
	}
	// A redirected term leaves the results page; searches are counted.
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(e.srv.URL + "/s/store-a/main/c?q=ENVIOS")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/s/store-a/main/politicas/envios") {
		t.Fatalf("search redirect = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	for _, q := range []string{"nada-de-esto", "articulo", "Artículo"} {
		r, err := http.Get(e.srv.URL + "/s/store-a/main/c?q=" + q)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
	terms := e.must(200, "GET", "/v1/search/terms", e.adminA, nil)
	if zero := terms["noResults"].([]any); len(zero) != 1 || zero[0].(map[string]any)["term"] != "nada de esto" {
		t.Fatalf("no-result terms = %v", zero)
	}
	if top := terms["top"].([]any)[0].(map[string]any); top["term"] != "articulo" || top["searches"].(float64) != 2 {
		t.Fatalf("top term = %v", top)
	}

	// Reviews: validated, pending until approved, one per shopper and product.
	if st, _ := e.do("POST", "/s/store-a/api/reviews/p1", "", m{"rating": 7, "body": "corto", "name": "", "email": "x"}); st != 400 {
		t.Fatalf("invalid review = %d", st)
	}
	rv := m{"rating": 4, "title": "Bien", "body": "Cumple con lo que promete.", "name": "Ana", "email": "ana@example.com"}
	if st, body := e.do("POST", "/s/store-a/api/reviews/p1", "", rv); st != 201 || body["status"] != "pending" || body["verified"] != false {
		t.Fatalf("create review = %d %v", st, body)
	}
	rv["rating"] = 5
	e.do("POST", "/s/store-a/api/reviews/p1", "", rv) // same shopper again: replaces
	if pub := e.must(200, "GET", "/s/store-a/api/reviews/p1", "", nil); pub["count"].(float64) != 0 {
		t.Fatalf("pending review is public: %v", pub)
	}
	list := e.must(200, "GET", "/v1/reviews?status=pending", e.adminA, nil)
	items := list["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["rating"].(float64) != 5 || items[0].(map[string]any)["productTitle"] != "Producto 1" {
		t.Fatalf("moderation queue = %v", items)
	}
	id := items[0].(map[string]any)["id"].(string)
	if st, _ := e.do("POST", "/v1/reviews/"+id+"/moderate", e.adminB, m{"status": "approved"}); st != 404 {
		t.Fatalf("another store moderated the review: %d", st)
	}
	e.must(200, "POST", "/v1/reviews/"+id+"/moderate", e.adminA, m{"status": "approved", "reply": "¡Gracias!"})
	pub := e.must(200, "GET", "/s/store-a/api/reviews/p1", "", nil)
	if pub["count"].(float64) != 1 || pub["average"].(float64) != 5 {
		t.Fatalf("approved review not public: %v", pub)
	}
	page, err := http.Get(e.srv.URL + "/s/store-a/main/p/p1")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	raw, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if !strings.Contains(html, "Cumple con lo que promete.") || !strings.Contains(html, "aggregateRating") || !strings.Contains(html, "Respuesta de la tienda") {
		t.Fatal("product page does not show the approved review")
	}
	if strings.Contains(html, "ana@example.com") {
		t.Fatal("the reviewer's email must never be public")
	}
	var stored string
	if err := e.db.System.QueryRow(context.Background(), `SELECT email_hash FROM reviews WHERE id=$1`, id).Scan(&stored); err != nil || strings.Contains(stored, "@") {
		t.Fatalf("email stored in clear: %q %v", stored, err)
	}
	e.must(204, "DELETE", "/v1/reviews/"+id, e.adminA, nil)
	if pub := e.must(200, "GET", "/s/store-a/api/reviews/p1", "", nil); pub["count"].(float64) != 0 {
		t.Fatalf("deleted review still public: %v", pub)
	}
}
