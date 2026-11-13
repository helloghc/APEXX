package sitecfg

import "testing"

// A link to a category or product the store no longer has is left out of
// the menu; its sub-links take its place. Free-text categories (older
// menus, a search) and other kinds are kept.
func TestResolveWithLeavesOutMissing(t *testing.T) {
	items := []MenuItem{
		{ID: "a", Type: "category", Ref: "tenis", Label: "Tenis", Children: []MenuItem{{ID: "a1", Type: "category", Ref: "running", Label: "Running"}}},
		{ID: "b", Type: "category", Ref: "Ropa de playa", Label: "Playa"},
		{ID: "c", Type: "product", Ref: "gorra-roja", Label: "Gorra"},
		{ID: "d", Type: "policy", Ref: "terminos", Label: "Términos"},
	}
	exists := func(kind, ref string) bool { return ref == "running" }
	got := ResolveWith(items, "/s/t/main", exists)
	labels := []string{}
	for _, l := range got {
		labels = append(labels, l.Label)
	}
	want := []string{"Running", "Playa", "Términos"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("labels = %v, want %v", labels, want)
		}
	}
	if got[0].URL != "/s/t/main/c/running" || got[1].URL != "/s/t/main/c?q=Ropa+de+playa" {
		t.Fatalf("urls = %q %q", got[0].URL, got[1].URL)
	}
	if n := len(Resolve(items, "/s/t/main")); n != 4 {
		t.Fatalf("Resolve without a check keeps every link: %d", n)
	}
}
