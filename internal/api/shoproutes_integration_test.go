package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Addresses a shopper types by hand: without a page name they reach the home
// page, and a page that does not exist answers with the store's HTML 404, not
// an API error.
func TestShopTypedAddresses(t *testing.T) {
	e := setup(t)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/s/store-a", "/s/store-a/", "/s/store-a/main", "/s/store-a/main/"} {
		res, err := noFollow.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/s/store-a/main/index" {
			t.Fatalf("%s = %d %q", p, res.StatusCode, res.Header.Get("Location"))
		}
	}
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	res, err := http.Get(e.srv.URL + "/s/store-a/main/no-existe-esta-pagina")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("unknown page = %d %s %s", res.StatusCode, res.Header.Get("Content-Type"), b)
	}
	if res, _ := http.Get(e.srv.URL + "/s/no-such-store/main/index"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown store = %d", res.StatusCode)
	}
}
