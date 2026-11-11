package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestThemesAPIAndThemedStorefront(t *testing.T) {
	e := setup(t)
	c, m := e.do("GET", "/v1/builder/themes", e.adminA, nil)
	if c != 200 || len(m["items"].([]any)) != 4 || len(m["fonts"].([]any)) < 8 {
		t.Fatalf("themes: %d %v", c, m["items"])
	}
	// Start the site from the Voltio starter; the previous draft is kept.
	if c, _ := e.do("GET", "/v1/sites/main/templates/index/draft", e.adminA, nil); c != 200 {
		t.Fatalf("draft %d", c)
	}
	c, r := e.do("POST", "/v1/sites/main/theme", e.adminA, map[string]any{"themeId": "voltio"})
	if c != 200 || r["previousRevisionId"] == "" {
		t.Fatalf("apply theme %d %v", c, r)
	}
	if c, _ := e.do("POST", "/v1/sites/main/theme", e.adminA, map[string]any{"themeId": "nope"}); c != 400 {
		t.Fatalf("unknown theme %d", c)
	}
	// A low-contrast override blocks publishing.
	if c, _ := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, map[string]any{"ops": []map[string]any{{"op": "add", "path": "/theme/colors", "value": map[string]string{"text": "#20242E"}}}}); c != 200 {
		t.Fatalf("patch theme %d", c)
	}
	if c, b := e.do("POST", "/v1/sites/main/publish", e.adminA, nil); c != 422 || !strings.Contains(mustJSON(b), "low_contrast") {
		t.Fatalf("low contrast must block: %d %v", c, b)
	}
	if c, _ := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, map[string]any{"ops": []map[string]any{{"op": "remove", "path": "/theme/colors"}}}); c != 200 {
		t.Fatal("remove override")
	}
	if c, _ := e.do("POST", "/v1/sites/main/publish", e.adminA, nil); c != 200 {
		t.Fatalf("publish %d", c)
	}
	get := func(path string) (*http.Response, string) {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(b)
	}
	for _, p := range []string{"/s/store-a/main/index", "/s/store-a/main/c", "/s/store-a/main/p/p1", "/s/store-a/main/cart"} {
		resp, body := get(p)
		if resp.StatusCode != 200 || !strings.Contains(body, "t-voltio") || !strings.Contains(body, "/s/assets/theme-voltio.css") || strings.Contains(body, "ZgotmplZ") {
			t.Fatalf("%s: %d themed=%v", p, resp.StatusCode, strings.Contains(body, "t-voltio"))
		}
	}
	resp, _ := get("/s/fonts/sora.woff2")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "font/woff2" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("font: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := get("/s/fonts/../shop.js"); resp.StatusCode == 200 {
		t.Fatal("font path traversal")
	}
	if resp, css := get("/s/assets/theme-voltio.css"); resp.StatusCode != 200 || len(css) > 25_000 {
		t.Fatalf("theme css %d %d bytes", resp.StatusCode, len(css))
	}
	// Public routes never create sites.
	if resp, _ := get("/s/store-a/no-such-site/c"); resp.StatusCode != 404 {
		t.Fatalf("unknown site: %d", resp.StatusCode)
	}
	// Undo the theme switch from the saved revision.
	if c, _ := e.do("POST", "/v1/sites/main/restore", e.adminA, map[string]any{"revisionId": r["previousRevisionId"]}); c != 200 {
		t.Fatalf("restore %d", c)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
