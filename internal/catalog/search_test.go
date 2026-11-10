package catalog

import "testing"

func TestSearchTyposAccentsAndPaging(t *testing.T) {
	s := New(nil)
	s.Upsert(Product{ID: "1", Handle: "tenis-converse", Title: "Tenis Converse Chuck Taylor", Brand: "Converse", Variants: []Variant{{SKU: "C-1"}}})
	s.Upsert(Product{ID: "2", Handle: "vestido", Title: "Vestido corto", Brand: "Pacífico"})
	s.Upsert(Product{ID: "3", Handle: "oculto", Title: "Tenis ocultos", Status: "draft"})
	cases := map[string]int{"convrse": 1, "CONVERSE chuck": 1, "pacifico": 1, "tenis": 1, "ten": 1, "zzz": 0, "": 2, "c-1": 1}
	for q, want := range cases {
		if got, _ := s.Search(q, "", 10); len(got) != want {
			t.Errorf("%q: %d results, want %d", q, len(got), want)
		}
	}
	page1, next := s.Search("", "", 1)
	page2, _ := s.Search("", next, 1)
	if len(page1) != 1 || len(page2) != 1 || page1[0].ID == page2[0].ID {
		t.Fatalf("paging broken: %v %v", page1, page2)
	}
	if _, ok := s.ByHandle("oculto"); ok {
		t.Fatal("draft product visible by handle")
	}
}
