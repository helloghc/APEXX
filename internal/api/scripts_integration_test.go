package api

import (
	"net/http"
	"strings"
	"testing"
)

// Script manager: declared origins are allowed by the page's security policy
// only on pages that may load them, never on checkout, and never in another store.
func TestStoreScriptsAndConsent(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminB, nil)
	if st, _ := e.do("PUT", "/v1/settings/scripts", e.adminA, m{"items": []m{{"name": "Malo", "category": "analytics", "src": "http://cdn.example/a.js", "active": true}}}); st != 400 {
		t.Fatalf("plain http script accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/scripts", e.adminA, m{"items": []m{{"name": "Raro", "category": "spyware", "src": "https://cdn.example/a.js", "active": true}}}); st != 400 {
		t.Fatalf("unknown category accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/scripts", e.sfA, m{"items": []m{}}); st != 401 && st != 403 {
		t.Fatalf("storefront key changed scripts: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/scripts", e.adminA, m{"bannerText": "Usamos herramientas de medición.", "items": []m{
		{"name": "Analítica", "category": "analytics", "src": "https://cdn.analitica.example/a.js", "active": true},
		{"name": "Píxel", "category": "marketing", "src": "https://px.ads.example/p.js", "active": true},
		{"name": "Apagado", "category": "marketing", "src": "https://off.example/x.js", "active": false},
	}})
	pub := e.must(200, "GET", "/s/store-a/api/scripts", "", nil)
	if len(pub["items"].([]any)) != 2 || pub["bannerText"] != "Usamos herramientas de medición." {
		t.Fatalf("public scripts = %v", pub)
	}
	csp := func(path string) string {
		res, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s = %d", path, res.StatusCode)
		}
		return res.Header.Get("Content-Security-Policy")
	}
	if c := csp("/s/store-a/main/p/p1"); !strings.Contains(c, "https://cdn.analitica.example") || !strings.Contains(c, "https://px.ads.example") || strings.Contains(c, "off.example") {
		t.Fatalf("product page policy: %s", c)
	}
	if c := csp("/s/store-a/main/checkout"); strings.Contains(c, "analitica") || strings.Contains(c, "ads.example") {
		t.Fatalf("checkout allows third-party scripts: %s", c)
	}
	if c := csp("/s/store-b/main/c"); strings.Contains(c, "analitica") {
		t.Fatalf("store B allows store A's scripts: %s", c)
	}
	if b := e.must(200, "GET", "/s/store-b/api/scripts", "", nil); len(b["items"].([]any)) != 0 {
		t.Fatalf("store B lists store A's scripts: %v", b)
	}
}
