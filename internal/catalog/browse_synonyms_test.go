package catalog

import "testing"

func TestBrowseSynonymsPinsAndHidden(t *testing.T) {
	ps := []*Product{
		{ID: "a", Handle: "playera-roja", Title: "Playera roja", Status: "active", Variants: []Variant{{SKU: "a1"}}},
		{ID: "b", Handle: "playera-azul", Title: "Playera azul", Status: "active", Variants: []Variant{{SKU: "b1"}}},
		{ID: "c", Handle: "gorra", Title: "Gorra", Status: "active", Variants: []Variant{{SKU: "c1"}}},
	}
	for _, p := range ps {
		p.hay = words(p.Title)
	}
	tx := NewTaxonomy()
	offer := func(*Product) Offer { return Offer{HasPrice: true, Price: 100, InStock: true} }
	ids := func(r BrowseResult) string {
		out := ""
		for _, p := range r.Items {
			out += p.ID
		}
		return out
	}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Q: "remera", Limit: 10})); got != "" {
		t.Fatalf("without synonyms = %q", got)
	}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Q: "remera", Alt: []string{"playera"}, Limit: 10})); len(got) != 2 {
		t.Fatalf("with synonyms = %q", got)
	}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Q: "playera", Pinned: []string{"c", "b"}, Limit: 10})); got != "cba" {
		t.Fatalf("pinned = %q", got)
	}
	if got := ids(Browse(ps, tx, offer, BrowseQuery{Q: "playera", Exclude: map[string]bool{"a": true}, Limit: 10})); got != "b" {
		t.Fatalf("hidden = %q", got)
	}
}
