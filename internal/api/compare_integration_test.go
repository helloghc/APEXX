package api

import (
	"net/http"
	"strings"
	"testing"
)

// Comparison page: up to four active products of this store, unknown or
// repeated handles ignored, never indexed, and rows that differ marked.
func TestCompareProducts(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	get := func(path string) (int, string) {
		res, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		buf := make([]byte, 32<<10)
		for {
			n, err := res.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return res.StatusCode, b.String()
	}
	st, html := get("/s/store-a/main/comparar?p=p1,p2,p1,no-existe,%3Cscript%3E,p3")
	if st != 200 || strings.Count(html, "data-compare-del=") != 3 || !strings.Contains(html, "Producto 2") || !strings.Contains(html, "Precio") {
		t.Fatalf("compare page = %d, %d products", st, strings.Count(html, "data-compare-del="))
	}
	if !strings.Contains(html, `noindex`) || strings.Contains(html, "<script>") && strings.Contains(html, "data-compare-del=\"<script>") {
		t.Fatal("compare page is indexable or echoes input")
	}
	// Same price everywhere: the price row is marked as equal.
	if !strings.Contains(html, "<tr data-same><th scope=\"row\">Precio</th>") {
		t.Fatalf("equal rows not marked: %s", html[strings.Index(html, "<tbody>"):min(len(html), strings.Index(html, "<tbody>")+400)])
	}
	// Another store does not show store A's products.
	if _, html := get("/s/store-b/main/comparar?p=p1,p2"); strings.Contains(html, "data-compare-del=") {
		t.Fatal("store B compared store A's products")
	}
	// A single product asks for another one.
	if _, html := get("/s/store-a/main/comparar?p=p1"); !strings.Contains(html, "al menos dos productos") {
		t.Fatal("one product: no guidance")
	}
}
