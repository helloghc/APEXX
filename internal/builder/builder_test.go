package builder

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func home() Template {
	return Template{ID: "t", Kind: "home", Handle: "index", SEO: SEO{Title: "Inicio", Description: "d"},
		Regions: map[string][]SectionInstance{"main": {
			{ID: "hero", Type: "hero", Settings: map[string]any{"heading": "Hola", "image": "/i.avif", "alt": "Una foto bonita"}},
			{ID: "txt", Type: "rich-text", Settings: map[string]any{"heading": "Novedades"}},
			{ID: "promo", Type: "rich-text", Settings: map[string]any{"heading": "Promociones"}},
		}}}
}

func newSite(t *testing.T) *Site {
	lib, err := NewLibrary(DefaultSections()...)
	if err != nil {
		t.Fatal(err)
	}
	return NewSite("s", lib, nil, home())
}

func ops(t *testing.T, s string) []Op {
	var o []Op
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func order(tp Template) string {
	var ids []string
	for _, s := range tp.Regions["main"] {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, ",")
}

func TestMoveModuleIsOnePatch(t *testing.T) {
	s := newSite(t)
	tp, rep, _, err := s.Patch("index", ops(t, `[{"op":"move","from":"/regions/main/2","path":"/regions/main/1"}]`), 0)
	if err != nil || rep.Blocking() {
		t.Fatalf("err=%v report=%+v", err, rep)
	}
	if got := order(tp); got != "hero,promo,txt" {
		t.Fatalf("order %s", got)
	}
}

func TestPublishRollbackAndLive(t *testing.T) {
	s := newSite(t)
	r1, _, err := s.Publish("ana", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Patch("index", ops(t, `[{"op":"replace","path":"/regions/main/0/settings/heading","value":"Black Friday"}]`), 0); err != nil {
		t.Fatal(err)
	}
	r2, _, err := s.Publish("ana", "v2")
	if err != nil || r2.RevisionID == r1.RevisionID {
		t.Fatalf("publish v2: %v %v", err, r2)
	}
	html, _, _ := s.Live("index")
	if !strings.Contains(string(html), ">Black Friday</h1>") {
		t.Fatalf("live html missing new heading: %s", html)
	}
	rb, err := s.Rollback("ana")
	if err != nil || rb.RevisionID != r1.RevisionID || rb.RolledBackFrom != r2.RevisionID {
		t.Fatalf("rollback %v %+v", err, rb)
	}
	html, _, _ = s.Live("index")
	if !strings.Contains(string(html), ">Hola</h1>") || strings.Count(string(html), "<h1") != 1 {
		t.Fatalf("rollback html wrong: %s", html)
	}
}

func TestInvalidSettingsBlockPublish(t *testing.T) {
	s := newSite(t)
	_, rep, _, err := s.Patch("index", ops(t, `[{"op":"remove","path":"/regions/main/0/settings/alt"},{"op":"add","path":"/regions/main/0/settings/color","value":"red"}]`), 0)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, i := range rep.Issues {
		codes[i.Code] = true
	}
	if !codes["required"] || !codes["unknown_setting"] {
		t.Fatalf("issues %+v", rep.Issues)
	}
	if _, _, err := s.Publish("ana", ""); !errors.Is(err, ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
	if _, _, err := s.Live("index"); err == nil {
		t.Fatal("blocked publish must not go live")
	}
}

func TestBudgetBlocksHeavySection(t *testing.T) {
	s := newSite(t)
	_, rep, _, err := s.Patch("index", ops(t, `[{"op":"add","path":"/regions/main/-","value":{"id":"vw","type":"video-wall","settings":{"heading":"Videos"}}}]`), 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range rep.Issues {
		found = found || (i.Code == "budget_js" && i.Blocking)
	}
	if !found {
		t.Fatalf("expected budget_js issue, got %+v", rep.Issues)
	}
}

func TestStaleDraftConflict(t *testing.T) {
	s := newSite(t)
	_, _, v1, _ := s.Patch("index", ops(t, `[{"op":"replace","path":"/name","value":"A"}]`), 0)
	_, _, _, _ = s.Patch("index", ops(t, `[{"op":"replace","path":"/name","value":"B"}]`), v1)
	if _, _, _, err := s.Patch("index", ops(t, `[{"op":"replace","path":"/name","value":"C"}]`), v1); !errors.Is(err, ErrStaleDraft) {
		t.Fatalf("want ErrStaleDraft, got %v", err)
	}
}

func TestPatchOps(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"a":[1,2,3],"b":{"c":1}}`), &doc)
	doc, err := ApplyPatch(doc, ops(t, `[{"op":"copy","from":"/b","path":"/d"},{"op":"test","path":"/d/c","value":1},{"op":"remove","path":"/a/0"},{"op":"add","path":"/a/-","value":4}]`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	if string(b) != `{"a":[2,3,4],"b":{"c":1},"d":{"c":1}}` {
		t.Fatalf("got %s", b)
	}
	if _, err := ApplyPatch(doc, ops(t, `[{"op":"move","from":"/b","path":"/b/x"}]`)); err == nil {
		t.Fatal("moving into itself must fail")
	}
}

func TestEscapesUserContent(t *testing.T) {
	s := newSite(t)
	_, _, _, _ = s.Patch("index", ops(t, `[{"op":"replace","path":"/regions/main/0/settings/heading","value":"<script>alert(1)</script>"}]`), 0)
	html, _, _ := s.Preview("index")
	if strings.Contains(string(html), "<script>alert") {
		t.Fatal("heading was not escaped")
	}
}

func BenchmarkPublish(b *testing.B) {
	lib, _ := NewLibrary(DefaultSections()...)
	s := NewSite("s", lib, nil, home())
	for i := 0; i < b.N; i++ {
		_, _, _, _ = s.Patch("index", []Op{{Op: "move", From: "/regions/main/2", Path: "/regions/main/1"}}, 0)
		if _, _, err := s.Publish("b", ""); err != nil {
			b.Fatal(err)
		}
	}
}
