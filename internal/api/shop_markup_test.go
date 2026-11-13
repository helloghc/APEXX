package api

import (
	"regexp"
	"strings"
	"testing"
)

// The compare feature listens for clicks on anything carrying data-compare and
// cancels them. A variant radio that carried the same attribute could never be
// selected, so shoppers were stuck on the first variant of any product on sale.
// Only compare buttons may carry data-compare, and the script only listens to
// buttons.
func TestOnlyCompareButtonsCarryDataCompare(t *testing.T) {
	attr := regexp.MustCompile(`<(\w+)[^>]*\sdata-compare="`)
	for _, name := range []string{"shop/product.tmpl", "shop/layout.tmpl", "shop/catalog.tmpl", "shop/compare.tmpl"} {
		b, err := shopFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range attr.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "button" {
				t.Errorf("%s: <%s> carries data-compare; only compare buttons may", name, m[1])
			}
		}
	}
	js, err := shopFS.ReadFile("shop/shop.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), `"[data-compare]"`) || strings.Contains(string(js), `closest("[data-compare]`) {
		t.Error("shop.js listens to every [data-compare]; scope it to button[data-compare]")
	}
	if strings.Contains(string(js), "dataset.compare ") || regexp.MustCompile(`r\.dataset\.compare\b[^A-Z]`).MatchString(string(js)) {
		t.Error("a variant radio writes dataset.compare; use dataset.compareAt")
	}
}
