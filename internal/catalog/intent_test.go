package catalog

import (
	"reflect"
	"testing"
)

func TestParseIntent(t *testing.T) {
	tx := NewTaxonomy()
	tx.SetAttribute(AttributeDef{ID: "color", Name: "Color", Kind: AttrSelect, Options: []string{"Rojo", "Azul marino", "Negro"}, Filterable: true})
	tx.SetAttribute(AttributeDef{ID: "talla", Name: "Talla", Kind: AttrNumber, Filterable: true})
	tx.SetAttribute(AttributeDef{ID: "nota", Name: "Nota", Kind: AttrText})
	tx.SetBrand(Brand{ID: "b1", Handle: "rio-bravo", Name: "Río Bravo", Active: true})

	it := ParseIntent("Tenis rojos para correr talla 27 baratos", tx)
	if it.Q != "tenis para correr" || !reflect.DeepEqual(it.Attrs, map[string][]string{"color": {"Rojo"}, "talla": {"27"}}) || it.Sort != SortPriceAsc {
		t.Fatalf("%+v", it)
	}
	it = ParseIntent("chamarra azul marino rio bravo menos de 1,500", tx)
	if it.Q != "chamarra" || it.Attrs["color"][0] != "Azul marino" || len(it.Brands) != 1 || it.Brands[0] != "rio-bravo" || it.PriceMax == nil || *it.PriceMax != 150000 {
		t.Fatalf("%+v", it)
	}
	if it = ParseIntent("playeras rojas", tx); it.Q != "playeras" || it.Attrs["color"][0] != "Rojo" {
		t.Fatalf("%+v", it)
	}
	it = ParseIntent("playera desde 300", tx)
	if it.Q != "playera" || it.PriceMin == nil || *it.PriceMin != 30000 {
		t.Fatalf("%+v", it)
	}
	// Nothing to understand: the query is untouched.
	it = ParseIntent("taladro percutor", tx)
	if it.Any() || it.Q != "taladro percutor" {
		t.Fatalf("%+v", it)
	}
	// Words that only look like a price stay text.
	it = ParseIntent("menos es mas", tx)
	if it.Any() || it.Q != "menos es mas" {
		t.Fatalf("%+v", it)
	}
}
