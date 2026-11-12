package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Storefront GraphQL: catalog and cart with exactly the fields asked, limits
// on depth, size and page, and each store answering only for itself.
func TestStorefrontGraphQL(t *testing.T) {
	e := setup(t)
	gql := func(store, q string, vars m) m {
		return e.must(200, "POST", "/s/"+store+"/api/graphql", "", m{"query": q, "variables": vars})
	}
	r := gql("store-a", `query ($h: String!) { shop { currency } product(handle: $h) { title url available price { amount formatted } variants { sku available price { amount } } } }`, m{"h": "p1"})
	if r["errors"] != nil {
		t.Fatalf("errors: %v", r["errors"])
	}
	if d, _ := r["data"].(map[string]any); d == nil || d["product"] == nil {
		t.Fatalf("no product: %v", r)
	}
	p := r["data"].(map[string]any)["product"].(map[string]any)
	if p["title"] != "Producto 1" || p["url"] != "/s/store-a/main/p/p1" || p["price"].(map[string]any)["amount"].(float64) != 10000 || len(p["variants"].([]any)) != 1 {
		t.Fatalf("product = %v", p)
	}
	if _, has := p["description"]; has {
		t.Fatal("returned a field that was not asked for")
	}
	// Pages.
	r = gql("store-a", `{ products(first: 2) { nodes { handle } endCursor hasNextPage } }`, nil)
	c := r["data"].(map[string]any)["products"].(map[string]any)
	if len(c["nodes"].([]any)) != 2 || c["hasNextPage"] != true {
		t.Fatalf("page 1 = %v", c)
	}
	r = gql("store-a", `query ($a: String) { products(first: 2, after: $a) { nodes { handle } hasNextPage } }`, m{"a": c["endCursor"]})
	if c2 := r["data"].(map[string]any)["products"].(map[string]any); len(c2["nodes"].([]any)) != 1 || c2["hasNextPage"] != false {
		t.Fatalf("page 2 = %v", c2)
	}
	if r = gql("store-a", `{ products(first: 500) { nodes { handle } } }`, nil); r["errors"] == nil {
		t.Fatal("a page of 500 was allowed")
	}
	// Cart priced like the storefront.
	r = gql("store-a", `{ cart(items: [{sku: "S1", qty: 2}], postalCode: "53129") { lines { sku qty total { amount } } items { amount } total { amount } } }`, nil)
	ct := r["data"].(map[string]any)["cart"].(map[string]any)
	if ct["items"].(map[string]any)["amount"].(float64) != 20000 || len(ct["lines"].([]any)) != 1 {
		t.Fatalf("cart = %v", ct)
	}
	// Limits: a query too deep or too big is refused.
	deep := "{ shop { name } " + strings.Repeat("product(handle: \"p1\") { variants { price { amount } } } ", 1) + "}"
	if r = gql("store-a", deep, nil); r["errors"] != nil {
		t.Fatalf("normal query refused: %v", r["errors"])
	}
	if st, _ := e.do("POST", "/s/store-a/api/graphql", "", m{"query": "{ shop { name } }" + strings.Repeat(" ", 17<<10)}); st != 400 {
		t.Fatalf("a 17 KB query was accepted: %d", st)
	}
	if r = gql("store-a", `{ __schema { types { fields { type { fields { type { fields { type { name } } } } } } } } }`, nil); r["errors"] == nil {
		t.Fatal("a query deeper than 7 levels was allowed")
	}
	// Another store answers for itself only.
	if r = gql("store-b", `{ product(handle: "p1") { title } }`, nil); r["data"].(map[string]any)["product"] != nil {
		t.Fatalf("store B served store A's product: %v", r)
	}
	if st, _ := e.do("POST", "/s/nope/api/graphql", "", m{"query": "{ shop { name } }"}); st != 404 {
		t.Fatalf("unknown store = %d", st)
	}
	_ = fmt.Sprint()
}

// Headless stores on another domain may call the public storefront API.
func TestHeadlessCORS(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest("OPTIONS", e.srv.URL+"/s/store-a/api/graphql", nil)
	req.Header.Set("Origin", "https://otra-tienda.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Fatalf("preflight = %d %v", res.StatusCode, res.Header)
	}
	req, _ = http.NewRequest("POST", e.srv.URL+"/s/store-a/api/graphql", strings.NewReader(`{"query":"{ shop { currency } }"}`))
	req.Header.Set("Content-Type", "application/json")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "*" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("graphql CORS = %v", res.Header)
	}
	// Management stays same-origin only.
	req, _ = http.NewRequest("GET", e.srv.URL+"/v1/products", nil)
	req.Header.Set("Authorization", "Bearer "+e.adminA)
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("the management API allows other origins")
	}
}
