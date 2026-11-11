package builder

import (
	"strings"
	"testing"
)

type fakeData struct{}

func (fakeData) Products(limit int) []Card {
	out := make([]Card, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, Card{Title: "Producto de prueba", URL: "p/x", Price: "$199.00 MXN", CompareAt: "$249.00 MXN", Brand: "Norte", Initial: "P", Tone: i%6 + 1, SKU: "S"})
	}
	return out
}

func TestThemesMeetContrast(t *testing.T) {
	for _, th := range ThemeList() {
		var issues []Issue
		ValidateTheme(&ThemeSettings{ID: th.ID}, &issues)
		for _, i := range issues {
			t.Errorf("%s: %s %s", th.ID, i.Path, i.Message)
		}
		for _, k := range ColorTokens {
			if !validHex(th.Colors[k]) {
				t.Errorf("%s: token %s missing or invalid", th.ID, k)
			}
		}
		if Fonts[th.Display].ID == "" || Fonts[th.Body].ID == "" {
			t.Errorf("%s: unknown font", th.ID)
		}
	}
}

func TestStartersRenderClean(t *testing.T) {
	lib, err := NewLibrary(DefaultSections()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range ThemeList() {
		tpl := Starter(th.ID)
		s := NewSite("s", lib, fakeData{}, tpl)
		html, rep, _ := s.Preview("index")
		for _, i := range rep.Issues {
			if i.Blocking {
				t.Errorf("%s: blocking issue %s %s", th.ID, i.Path, i.Message)
			}
		}
		n := len(tpl.Regions["main"])
		if n < 6 || n > 9 {
			t.Errorf("%s: %d main sections, want 6-9", th.ID, n)
		}
		page := string(html)
		if strings.Contains(page, "ZgotmplZ") {
			t.Errorf("%s: html/template sanitized a value (ZgotmplZ): broken URL or image", th.ID)
		}
		for _, want := range []string{"t-" + th.ID, "/s/assets/theme-" + th.ID + ".css", `data-cart-count`, `<h1 class="display">`, "sx-footer"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: page missing %q", th.ID, want)
			}
		}
		if strings.Count(page, "<h1") != 1 {
			t.Errorf("%s: %d h1", th.ID, strings.Count(page, "<h1"))
		}
		if rep.HTMLSize > rep.Limit.HTMLBytes {
			t.Errorf("%s: html %d > %d", th.ID, rep.HTMLSize, rep.Limit.HTMLBytes)
		}
		if _, _, err := s.Publish("t", "starter"); err != nil {
			t.Errorf("%s: publish: %v", th.ID, err)
		}
		ch := s.LiveChrome()
		if !strings.Contains(string(ch.Header), "sx-header") || !strings.Contains(ch.BodyClass, "t-"+th.ID) {
			t.Errorf("%s: chrome missing header/theme", th.ID)
		}
	}
}

func TestLowContrastBlocksPublish(t *testing.T) {
	lib, _ := NewLibrary(DefaultSections()...)
	tpl := Starter("lienzo")
	tpl.Theme = &ThemeSettings{ID: "lienzo", Colors: map[string]string{"text": "#EDEBE7"}}
	s := NewSite("s", lib, fakeData{}, tpl)
	_, rep, _ := s.Preview("index")
	found := false
	for _, i := range rep.Issues {
		found = found || (i.Code == "low_contrast" && i.Blocking && i.Path == "/theme/colors/text")
	}
	if !found {
		t.Fatalf("expected low_contrast issue, got %+v", rep.Issues)
	}
	if _, _, err := s.Publish("t", ""); err != ErrBlocked {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
	// Overrides apply when valid.
	r := 4
	rt := Resolve(&ThemeSettings{ID: "voltio", Colors: map[string]string{"accent": "#ff00aa"}, Display: "cormorant", Radius: &r})
	if rt.Colors["accent"] != "#FF00AA" || rt.DisplayFont.ID != "cormorant" || rt.Radius != 4 {
		t.Fatalf("overrides not applied: %+v", rt.Theme)
	}
	if Contrast("#000000", "#FFFFFF") < 20.9 {
		t.Fatal("contrast formula wrong")
	}
}

func TestApplyStarterAndRestore(t *testing.T) {
	lib, _ := NewLibrary(DefaultSections()...)
	s := NewSite("s", lib, nil, home())
	prev, _, err := s.ApplyStarter("index", Starter("voltio"), "ana")
	if err != nil || prev == "" {
		t.Fatalf("apply: %v %q", err, prev)
	}
	d, _, _ := s.Draft("index")
	if d.Theme == nil || d.Theme.ID != "voltio" {
		t.Fatal("starter not applied")
	}
	if _, err := s.RestoreRevision(prev); err != nil {
		t.Fatal(err)
	}
	d, _, _ = s.Draft("index")
	if d.Theme != nil {
		t.Fatal("restore did not bring back the previous draft")
	}
}
