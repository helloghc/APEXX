package engage

import (
	"reflect"
	"testing"
)

func TestFold(t *testing.T) {
	if got := Fold("  Ténis   ROJOS!! "); got != "tenis rojos" {
		t.Fatalf("Fold = %q", got)
	}
}

func TestExpandAndRules(t *testing.T) {
	c := &SearchConfig{
		Synonyms:  [][]string{{"Tenis", "zapatillas", "sneakers"}, {"playera", "camiseta"}, {"solo"}},
		Redirects: []TermRedirect{{Term: "Envíos", URL: "/politicas/envios"}, {Term: "x", URL: "javascript:alert(1)"}},
		Pins:      []TermPins{{Term: "Tenis", ProductIDs: []string{"p2", "p1"}}},
		Hidden:    []string{"p9"},
	}
	errs := c.Normalize()
	if len(errs) != 2 { // lone synonym + invalid redirect
		t.Fatalf("errs = %v", errs)
	}
	got := c.Expand("Ténis rojos")
	want := []string{"zapatillas rojos", "sneakers rojos"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand = %v", got)
	}
	if c.Expand("tenismesa") != nil {
		t.Fatal("must match whole words only")
	}
	if u, ok := c.RedirectFor("ENVIOS"); !ok || u != "/politicas/envios" {
		t.Fatalf("redirect = %q %v", u, ok)
	}
	if !reflect.DeepEqual(c.PinsFor("tenis"), []string{"p2", "p1"}) || !c.HiddenSet()["p9"] {
		t.Fatal("pins or hidden lost")
	}
	var nilCfg *SearchConfig
	if nilCfg.Expand("a") != nil || nilCfg.PinsFor("a") != nil || nilCfg.HiddenSet() != nil {
		t.Fatal("nil config must be inert")
	}
}
