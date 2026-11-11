package api

import (
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/i18n"
)

// Every fixed phrase of the storefront templates has a translation in every
// language, so a new phrase cannot ship in Spanish only without failing here.
func TestStorefrontPhrasesAreTranslated(t *testing.T) {
	files, _ := fs.Glob(shopFS, "shop/*.tmpl")
	if len(files) < 10 {
		t.Fatalf("plantillas encontradas: %d", len(files))
	}
	for _, l := range i18n.Languages[1:] {
		look := i18n.Builtin(l.Code)
		for _, f := range files {
			b, _ := shopFS.ReadFile(f)
			for _, p := range i18n.MarkupPhrases(string(b)) {
				if p == "XXXX-XXXX-XXXX-XXXX" {
					continue
				}
				if _, ok := look(p); !ok {
					t.Errorf("%s: %s: falta traducir %q", l.Code, f, p)
				}
			}
		}
	}
}

// The translated script must still be the same program: same length of code
// outside the phrases, and no Spanish left in the messages shoppers see.
func TestStorefrontScriptIsTranslated(t *testing.T) {
	src, _ := shopFS.ReadFile("shop/shop.js")
	for _, l := range i18n.Languages[1:] {
		out := string(shopScript(l.Code))
		for _, es := range []string{"Tu carrito está vacío.", "Agregado al carrito.", "No pudimos procesar el pago. Intenta de nuevo."} {
			if !strings.Contains(string(src), es) {
				t.Fatalf("la frase %q ya no está en el guion", es)
			}
			if strings.Contains(out, es) {
				t.Errorf("%s: quedó en español %q", l.Code, es)
			}
		}
		if strings.Count(out, "\n") != strings.Count(string(src), "\n") {
			t.Errorf("%s: el guion cambió de líneas", l.Code)
		}
	}
}

func TestStorefrontLanguages(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	home := "/s/store-a/main/c"
	get := func(path string) string {
		_, b, _ := e.raw(http.MethodGet, path, "", nil, "")
		return string(b)
	}
	// Only Spanish: ?hl is ignored and the page is untouched.
	body := get(home + "?hl=id")
	if strings.Contains(body, "hreflang") || !strings.Contains(body, `<html lang="es">`) {
		t.Fatalf("una tienda solo en español no debe anunciar otros idiomas: %.300s", body)
	}
	e.must(http.StatusBadRequest, http.MethodPut, "/v1/settings/languages", e.adminA, m{"enabled": []string{"id", "ja", "xx"}, "own": m{"id": m{"Camiseta Básica": "Kaus Dasar"}}})
	e.must(http.StatusOK, http.MethodPut, "/v1/settings/languages", e.adminA, m{"enabled": []string{"id", "ja"}, "own": m{"id": m{"Todos los productos": "Semua barang kami"}}})
	e.drain()

	es := get(home)
	for _, want := range []string{`<html lang="es">`, `hreflang="id"`, `hreflang="ja"`, `hreflang="x-default"`, `class="langs"`} {
		if !strings.Contains(es, want) {
			t.Errorf("español: falta %s", want)
		}
	}
	if strings.Contains(es, `hreflang="fr"`) {
		t.Error("anuncia un idioma que la tienda no activó")
	}
	id := get(home + "?hl=id")
	for _, want := range []string{`<html lang="id" data-hl="id">`, `shop.js?hl=id`, "Lompat ke konten", "Semua barang kami", `?hl=id"`} {
		if !strings.Contains(id, want) {
			t.Errorf("indonesio: falta %s", want)
		}
	}
	if strings.Contains(id, "Saltar al contenido") {
		t.Error("indonesio: quedó texto en español")
	}
	// A language the store did not turn on falls back to Spanish.
	fr := get(home + "?hl=fr")
	if !strings.Contains(fr, `<html lang="es">`) {
		t.Error("francés no está activo: debe verse en español")
	}
	// The other store is not affected.
	other := get("/s/store-b/main/c?hl=id")
	if strings.Contains(other, "hreflang") {
		t.Error("los idiomas de una tienda se filtraron a otra")
	}
	// Messages from the API come in the shopper's language when the script asks.
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/s/store-a/api/simulate", strings.NewReader(`{"channel":"mx","items":[{"sku":"p1-a","quantity":1}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Apexx-Lang", "id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "Ingresa un código postal") {
		t.Errorf("el mensaje del carrito llegó en español: %.200s", raw)
	}
	// The script is served translated.
	js := get("/s/assets/shop.js?hl=ja&v=12")
	if !strings.Contains(js, "カート") {
		t.Error("el guion en japonés no llegó traducido")
	}
}

// A product translated on its own screen shows translated in the store, and
// the panel flags the translation once the original changes.
func TestProductTranslations(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	e.must(200, "PUT", "/v1/settings/languages", e.adminA, m{"enabled": []string{"en"}})
	if st, _ := e.do("PUT", "/v1/products/P1/translations", e.adminA, m{"xx": m{"title": "X"}}); st != 400 {
		t.Fatalf("unknown language accepted: %d", st)
	}
	p := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	title := p["title"].(string)
	e.must(204, "PUT", "/v1/products/P1/translations", e.adminA, m{"en": m{"title": "Translated product"}})
	e.drain()
	_, b, _ := e.raw(http.MethodGet, "/s/store-a/main/p/"+p["handle"].(string)+"?hl=en", "", nil, "")
	if !strings.Contains(string(b), "Translated product") {
		t.Fatal("the product title was not translated in the store")
	}
	got := e.must(200, "GET", "/v1/products/P1/translations", e.adminA, nil)
	en := got["texts"].(map[string]any)["en"].(map[string]any)["title"].(map[string]any)
	if en["text"] != "Translated product" || en["outdated"] != false {
		t.Fatalf("translation read back: %v", en)
	}
	// The original changes: the translation is marked outdated.
	e.must(200, "PUT", "/v1/products/P1", e.adminA, m{"handle": p["handle"], "title": title + " nuevo", "variants": []m{{"sku": "S1", "title": "Única"}}})
	e.drain()
	got = e.must(200, "GET", "/v1/products/P1/translations", e.adminA, nil)
	if got["texts"].(map[string]any)["en"].(map[string]any)["title"].(map[string]any)["outdated"] != true {
		t.Fatal("a translation of a changed title is not flagged")
	}
	// The storefront key cannot read or write translations; another store sees nothing.
	if st, _ := e.do("GET", "/v1/products/P1/translations", e.sfA, nil); st != 403 && st != 401 {
		t.Fatalf("storefront key read translations: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/products/P1/translations", e.adminB, m{"en": m{"title": "Hack"}}); st != 404 {
		t.Fatalf("another store wrote translations: %d", st)
	}
}
