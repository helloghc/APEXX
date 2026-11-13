package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tags and pixels: validated, published as a new version that every page
// points to at once (on this node and, through the outbox, on the others),
// allowed by the page's security policy only where they may run, never on
// checkout, and never in another store.
func TestStoreTags(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminB, nil)
	bad := []m{
		{"kind": "ga4", "account": "UA-1234-1", "active": true},
		{"kind": "meta", "account": "abc", "active": true},
		{"kind": "script", "src": "http://cdn.example/a.js", "fire": []string{"page_view"}, "active": true},
		{"kind": "script", "src": "https://cdn.example/a.js", "active": true}, // where it fires is missing
		{"kind": "code", "code": "<script>alert(1)</script>", "fire": []string{"purchase"}, "active": true},
		{"kind": "spyware", "active": true},
		{"kind": "ga4", "account": "G-ABC123DEF4", "category": "spy", "active": true},
	}
	for _, tag := range bad {
		if st, _ := e.do("PUT", "/v1/settings/tags", e.adminA, m{"tags": []m{tag}}); st != 400 {
			t.Fatalf("accepted %v: %d", tag, st)
		}
	}
	if st, _ := e.do("PUT", "/v1/settings/tags", e.sfA, m{"tags": []m{}}); st != 401 && st != 403 {
		t.Fatalf("storefront key changed tags: %d", st)
	}
	get := func(srv *httptest.Server, path string) (string, http.Header) {
		t.Helper()
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s = %d", path, res.StatusCode)
		}
		return string(b), res.Header
	}
	if html, _ := get(e.srv, "/s/store-a/main/p/p1"); strings.Contains(html, "tags.js") {
		t.Fatal("a store without tags loads tags.js")
	}
	pub := e.must(200, "PUT", "/v1/settings/tags", e.adminA, m{"bannerText": "Usamos herramientas de medición.", "tags": []m{
		{"kind": "ga4", "account": "g-abc123def4", "active": true},
		{"kind": "meta", "account": "1234 5678 9012", "active": true},
		{"kind": "gads", "account": "AW-123456789", "label": "AbC-123_x", "active": true},
		{"kind": "script", "name": "Píxel de afiliados", "src": "https://px.afiliados.example/p.js", "fire": []string{"purchase"}, "active": true},
		{"kind": "code", "name": "Mi código", "code": "window.fired = (window.fired || []).concat(apexx.event);", "fire": []string{"page_view", "purchase"}, "domains": []string{"https://api.mio.example"}, "active": true},
		{"kind": "tiktok", "account": "C4ABCDEFGH1234567890", "active": false},
	}})
	cfg := pub["config"].(map[string]any)
	if cfg["version"].(float64) != 1 {
		t.Fatalf("first version = %v", cfg["version"])
	}
	tags := cfg["tags"].([]any)
	if tags[0].(map[string]any)["account"] != "G-ABC123DEF4" || tags[0].(map[string]any)["category"] != "analytics" || tags[1].(map[string]any)["account"] != "123456789012" ||
		tags[2].(map[string]any)["fire"].([]any)[0] != "purchase" {
		t.Fatalf("normalized tags = %v", tags)
	}
	codeID := tags[4].(map[string]any)["id"].(string)

	// Product page: tags.js at version 1, and a policy with exactly the providers in use.
	html, h := get(e.srv, "/s/store-a/main/p/p1")
	if !strings.Contains(html, `/s/store-a/api/tags.js?v=1"`) {
		t.Fatalf("product page without tags.js v1")
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"https://www.googletagmanager.com", "https://connect.facebook.net", "https://www.googleadservices.com", "https://px.afiliados.example", "https://api.mio.example"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("product page policy lacks %s: %s", want, csp)
		}
	}
	if strings.Contains(csp, "tiktok") || strings.Contains(csp, "'unsafe-inline' https:") {
		t.Fatalf("policy allows more than the active tags: %s", csp)
	}
	// The home built with the builder carries it too, without publishing the site again.
	if home, _ := get(e.srv, "/s/store-a/main/index"); !strings.Contains(home, `/s/store-a/api/tags.js?v=1"`) {
		t.Fatal("builder home without tags.js")
	}
	// Checkout and the account: no tags, no providers.
	for _, p := range []string{"/s/store-a/main/checkout", "/s/store-a/main/cuenta"} {
		html, h := get(e.srv, p)
		if strings.Contains(html, "tags.js") || strings.Contains(h.Get("Content-Security-Policy"), "facebook") {
			t.Fatalf("%s loads tags: %s", p, h.Get("Content-Security-Policy"))
		}
	}
	// The thank-you page (where conversions fire) does.
	if html, _ := get(e.srv, "/s/store-a/main/gracias"); !strings.Contains(html, "tags.js?v=1") {
		t.Fatal("thank-you page without tags")
	}
	// Store B: nothing of store A.
	if html, h := get(e.srv, "/s/store-b/main/c"); strings.Contains(html, "tags.js") || strings.Contains(h.Get("Content-Security-Policy"), "facebook") {
		t.Fatal("store B loads store A's tags")
	}
	// tags.js: the configuration, immutable at its version.
	js, jh := get(e.srv, "/s/store-a/api/tags.js?v=1")
	if !strings.Contains(js, "G-ABC123DEF4") || !strings.Contains(js, "123456789012") || strings.Contains(js, "C4ABCDEFGH") || !strings.Contains(js, "Usamos herramientas de medición.") {
		t.Fatalf("tags.js content: %.300s", js)
	}
	if !strings.Contains(jh.Get("Cache-Control"), "immutable") {
		t.Fatalf("tags.js cache: %s", jh.Get("Cache-Control"))
	}
	if strings.Contains(js, "window.fired") {
		t.Fatal("the store's code is inside tags.js instead of its own file")
	}
	code, _ := get(e.srv, "/s/store-a/api/tags/"+codeID+".js?v=1")
	if !strings.Contains(code, "window.fired") || !strings.HasPrefix(code, "(window.apexxCode") {
		t.Fatalf("code file: %s", code)
	}
	if st, _ := e.do("GET", "/s/store-b/api/tags/"+codeID+".js", "", nil); st != 404 {
		t.Fatalf("store B serves store A's code: %d", st)
	}

	// A second node: it built version 1; after store A publishes on the first
	// node, the outbox event makes it rebuild and its pages point to version 2.
	srv1 := e.srv
	srv2 := e.newSrv()
	if html, _ := get(srv2, "/s/store-a/main/p/p1"); !strings.Contains(html, "tags.js?v=1") {
		t.Fatal("second node without version 1")
	}
	e.srv = srv1
	e.must(200, "PUT", "/v1/settings/tags", e.adminA, m{"tags": []m{{"kind": "gtm", "account": "GTM-ABC1234", "active": true}}})
	if html, _ := get(srv1, "/s/store-a/main/p/p1"); !strings.Contains(html, "tags.js?v=2") {
		t.Fatal("first node still on version 1")
	}
	e.drain() // the second node applies the event
	html, h = get(srv2, "/s/store-a/main/p/p1")
	if !strings.Contains(html, "tags.js?v=2") {
		t.Fatal("second node still on version 1 after the event")
	}
	// Tag Manager decides its tags in its own console: its pages allow https
	// and inline scripts; checkout still does not.
	if c := h.Get("Content-Security-Policy"); !strings.Contains(c, "https: 'unsafe-inline'") {
		t.Fatalf("Tag Manager policy: %s", c)
	}
	if _, h := get(srv2, "/s/store-a/main/checkout"); strings.Contains(h.Get("Content-Security-Policy"), "unsafe-inline' https:") || strings.Contains(h.Get("Content-Security-Policy"), "https: 'unsafe-inline'") {
		t.Fatal("checkout relaxed by Tag Manager")
	}

	// History: who published, when and what changed, in words.
	hist := e.must(200, "GET", "/v1/settings/tags/history", e.adminA, nil)
	items := hist["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["version"].(float64) != 2 || items[0].(map[string]any)["publisherName"] == "" {
		t.Fatalf("history = %v", hist)
	}
	if ch := strings.Join(toStrings(items[0].(map[string]any)["changes"]), " | "); !strings.Contains(ch, "Agregó Google Tag Manager GTM-ABC1234") || !strings.Contains(ch, "Quitó Google Analytics 4 G-ABC123DEF4") {
		t.Fatalf("changes of version 2: %s", ch)
	}
	r := e.must(200, "POST", "/v1/settings/tags/restore", e.adminA, m{"version": 1})
	if c := r["config"].(map[string]any); c["version"].(float64) != 3 || len(c["tags"].([]any)) != 6 {
		t.Fatalf("restore = %v", r)
	}
	if st, _ := e.do("POST", "/v1/settings/tags/restore", e.adminA, m{"version": 99}); st != 404 {
		t.Fatalf("restore of a missing version = %d", st)
	}
	if html, _ := get(srv1, "/s/store-a/main/p/p1"); !strings.Contains(html, "tags.js?v=3") {
		t.Fatal("restored version not served")
	}
	if ch := toStrings(r["config"].(map[string]any)["changes"]); len(ch) == 0 || ch[0] != "Restauró la versión 1" {
		t.Fatalf("restore changes: %v", ch)
	}

	// Chats, heat maps, the WhatsApp button, X with its event IDs and
	// verification tags.
	if st, b := e.do("PUT", "/v1/settings/tags", e.adminA, m{"tags": []m{{"kind": "x", "account": "o1a2b", "params": m{"ev_purchase": "compra"}, "active": true}}}); st != 400 {
		t.Fatalf("bad X event id accepted: %d %v", st, b)
	}
	if st, _ := e.do("PUT", "/v1/settings/tags", e.adminA, m{"meta": []m{{"kind": "other", "name": "robots", "content": "noindex"}}}); st != 400 {
		t.Fatalf("a robots meta accepted as verification: %d", st)
	}
	pub = e.must(200, "PUT", "/v1/settings/tags", e.adminA, m{
		"tags": []m{
			{"kind": "whatsapp", "account": "+52 55 1234 5678", "params": m{"message": "Hola", "position": "left"}, "active": true},
			{"kind": "crisp", "account": "A1B2C3D4-E5F6-7890-ABCD-EF1234567890", "active": true},
			{"kind": "hotjar", "account": "1234567", "active": true},
			{"kind": "x", "account": "o1a2b", "params": m{"ev_purchase": "tw-o1a2b-o1a2c"}, "active": true},
		},
		"meta": []m{
			{"kind": "google", "content": `<meta name="google-site-verification" content="AbC_123-xyz" />`},
			{"kind": "other", "name": "godaddy-verification", "content": "gd123456"},
		},
	})
	c4 := pub["config"].(map[string]any)
	t0 := c4["tags"].([]any)[0].(map[string]any)
	if t0["account"] != "525512345678" || t0["category"] != "necessary" || t0["params"].(map[string]any)["position"] != "left" {
		t.Fatalf("whatsapp normalized = %v", t0)
	}
	if c4["meta"].([]any)[0].(map[string]any)["content"] != "AbC_123-xyz" {
		t.Fatalf("pasted meta not read: %v", c4["meta"])
	}
	html, h = get(srv1, "/s/store-a/main/p/p1")
	if !strings.Contains(html, `<meta name="google-site-verification" content="AbC_123-xyz">`) || !strings.Contains(html, `<meta name="godaddy-verification" content="gd123456">`) {
		t.Fatal("verification tags missing in the head")
	}
	csp = h.Get("Content-Security-Policy")
	for _, want := range []string{"https://client.crisp.chat", "wss://client.relay.crisp.chat", "https://static.hotjar.com", "https://static.ads-twitter.com", "style-src 'self' 'unsafe-inline' https://client.crisp.chat", "font-src 'self' https://client.crisp.chat https://script.hotjar.com"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("policy lacks %s: %s", want, csp)
		}
	}
	// Verification tags also on the builder home and on checkout (they are not scripts).
	if home, _ := get(srv1, "/s/store-a/main/index"); !strings.Contains(home, "google-site-verification") {
		t.Fatal("builder home without verification")
	}
	if co, _ := get(srv1, "/s/store-a/main/checkout"); !strings.Contains(co, "google-site-verification") || strings.Contains(co, "tags.js") {
		t.Fatal("checkout: verification missing or tags present")
	}
	js, _ = get(srv1, "/s/store-a/api/tags.js?v=4")
	if !strings.Contains(js, `"ev_purchase":"tw-o1a2b-o1a2c"`) || !strings.Contains(js, "525512345678") {
		t.Fatalf("tags.js v4: %.400s", js)
	}
	// Consent mode: notice by default; an unknown mode is rejected; a change is in words.
	if !strings.Contains(js, `"mode":"notice"`) {
		t.Fatalf("tags.js without the default consent mode")
	}
	if st, _ := e.do("PUT", "/v1/settings/tags", e.adminA, m{"consentMode": "nunca"}); st != 400 {
		t.Fatalf("unknown consent mode accepted: %d", st)
	}
	pub = e.must(200, "PUT", "/v1/settings/tags", e.adminA, m{"consentMode": "opt_in", "tags": []m{{"kind": "ga4", "account": "G-ABC123DEF4", "active": true}}})
	if ch := strings.Join(toStrings(pub["config"].(map[string]any)["changes"]), " | "); !strings.Contains(ch, "Cambió el permiso de cookies: «solo aviso» → «todo con permiso»") {
		t.Fatalf("consent change: %s", ch)
	}
	if js, _ := get(srv1, "/s/store-a/api/tags.js?v=5"); !strings.Contains(js, `"mode":"opt_in"`) {
		t.Fatal("tags.js without opt_in")
	}
	// The audit log keeps who published and the whole configuration.
	audit := e.must(200, "GET", "/v1/ops/audit?action=store_tags", e.adminA, nil)
	if n := len(audit["items"].([]any)); n < 4 {
		t.Fatalf("audit entries of tags: %d", n)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
