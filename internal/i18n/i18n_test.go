package i18n

import (
	"strings"
	"testing"
)

func TestEveryLanguageHasEveryPhrase(t *testing.T) {
	base := builtin["en"]
	if len(base) < 300 {
		t.Fatalf("diccionario en inglés con %d frases", len(base))
	}
	for _, l := range Languages[1:] {
		for k := range base {
			if v := builtin[l.Code][k]; v == "" {
				t.Errorf("%s: falta %q", l.Code, k)
			}
		}
		for k, v := range builtin[l.Code] {
			if strings.Count(k, "#") != strings.Count(v, "#") {
				t.Errorf("%s: %q no conserva los números", l.Code, k)
			}
			if strings.Count(k, "{}") != strings.Count(v, "{}") {
				t.Errorf("%s: %q no conserva sus partes variables", l.Code, k)
			}
		}
	}
}

func TestMarkup(t *testing.T) {
	look := Builtin("en")
	src := `<a class="skip" href="/s/t/main/c/ropa" title="Carrito">Agregar al carrito</a><script>var x="Carrito"</script><p>{{.N}} disponibles</p><p>3 disponibles</p><p>Tom &amp; Ana</p><input placeholder="Buscar productos"><form action="/s/t/main/buscar"><p>Hola, {{.Name}}</p>`
	got := Markup(src, look, Options{Href: func(h string) string { return h + "?hl=en" }, FormParam: "en"})
	for _, want := range []string{`href="/s/t/main/c/ropa?hl=en"`, `title="Cart"`, `>Add to cart<`, `var x="Carrito"`, `{{.N}} available`, `>3 available<`, `Tom &amp; Ana`, `placeholder="Search products"`, `<input type="hidden" name="hl" value="en">`, `Hi, {{.Name}}`} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en\n%s", want, got)
		}
	}
}

func TestScriptNeverBreaksQuotes(t *testing.T) {
	src := `var a='Tu carrito está vacío.', b="<p class=\"muted\">Agregado al carrito.</p>", c=x>3&&y<4; el.textContent='Un momento…';`
	got := Script(src, Builtin("fr"))
	if !strings.Contains(got, `'Votre panier est vide.'`) || !strings.Contains(got, `Ajouté au panier.`) || !strings.Contains(got, "c=x>3&&y<4") {
		t.Fatal(got)
	}
	// A translation with an apostrophe must stay inside its string.
	got = Script(`s='Escribir una opinión'`, Builtin("fr"))
	if got != `s='Écrire un avis'` {
		t.Fatal(got)
	}
	got = Script(`s='Rastrear envío'`, Builtin("fr"))
	if got != `s='Suivre l\'envoi'` {
		t.Fatal(got)
	}
}

// Messages built by the server (delivery dates, holidays, promotion progress)
// are translated by pattern; names inside stay as the store wrote them.
func TestPhrasesWithVariableParts(t *testing.T) {
	cases := []struct{ lang, in, want string }{
		{"en", "Llega entre el vie 13 nov y el mié 18 nov", "Arrives between Fri Nov 13 and Wed Nov 18"},
		{"ja", "No cuenta el feriado Día de la Revolución (lun 16 nov)", "祝日（革命記念日、11月16日(月)）は日数に含みません"},
		{"fr", "Zona Norte: el código postal 22000 es de Baja California", "Zone Norte : le code postal 22000 est à Baja California"},
		{"en", "Agrega $2,094 más para obtener esta promoción (mínimo $3,000).", "Add $2,094 more to get this promotion (minimum $3,000)."},
		{"pt", "Pedido antes de las 14:00: sale hoy", "Pedido antes das 14:00: sai hoje"},
	}
	for _, c := range cases {
		if got := Phrase(c.in, Builtin(c.lang)); got != c.want {
			t.Errorf("%s: %q = %q, quería %q", c.lang, c.in, got, c.want)
		}
	}
	// In a page, a store's own sentence is never touched by a pattern.
	if got := Markup("<p>Llega el otoño a Sierra Madre</p>", Builtin("en"), Options{}); got != "<p>Llega el otoño a Sierra Madre</p>" {
		t.Errorf("texto de la tienda: %q", got)
	}
}
