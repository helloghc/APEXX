package api

import (
	"strings"
	"testing"
)

// Every provider's example passes its own validation (the form shows it as
// the placeholder), and every kind declares what it receives.
func TestTagKindsExamplesValidate(t *testing.T) {
	s := &Server{}
	for _, k := range tagKindList {
		if k.Receives == "" || k.Group == "" || k.Category == "" {
			t.Errorf("%s: missing receives, group or category", k.ID)
		}
		tag := Tag{Kind: k.ID, Active: true, Params: map[string]string{}}
		for _, f := range k.Fields {
			v := f.Example
			if len(f.Options) > 0 {
				v = f.Options[0]
			}
			if f.Key == "account" {
				tag.Account = v
			} else {
				tag.Params[f.Key] = v
			}
		}
		switch k.ID {
		case tagScript:
			tag.Src, tag.Fire = "https://cdn.example.com/p.js", []string{"purchase"}
		case tagCode:
			tag.Code, tag.Fire = "console.log(apexx.event)", []string{"page_view"}
		}
		c := TagsConfig{Tags: []Tag{tag}}
		if errs := s.validateTags(&c); len(errs) > 0 {
			t.Errorf("%s: its own example is rejected: %v", k.ID, errs)
		}
	}
}

func TestTagsChangesInWords(t *testing.T) {
	prev := TagsConfig{Tags: []Tag{
		{ID: "a", Kind: tagGA4, Name: "Google Analytics 4", Account: "G-OLD12345", Category: "analytics", Active: true},
		{ID: "b", Kind: tagMeta, Name: "Meta (Facebook e Instagram)", Account: "123456789", Category: "marketing", Active: true},
	}}
	next := TagsConfig{Tags: []Tag{
		{ID: "a", Kind: tagGA4, Name: "Google Analytics 4", Account: "G-NEW12345", Category: "analytics", Active: true},
		{ID: "c", Kind: tagWhatsApp, Name: "Botón de WhatsApp", Account: "5215512345678", Category: "necessary", Active: true},
	}, Meta: []MetaTag{{Kind: "google", Name: "google-site-verification", Content: "abc123"}}}
	got := strings.Join(tagsChanges(prev, next), " | ")
	for _, want := range []string{"Cambió Google Analytics 4 G-NEW12345: ID: «G-OLD12345» → «G-NEW12345»", "Agregó Botón de WhatsApp 5215512345678", "Quitó Meta (Facebook e Instagram) 123456789", "Agregó la verificación google-site-verification"} {
		if !strings.Contains(got, want) {
			t.Errorf("changes lack %q: %s", want, got)
		}
	}
	if c := tagsChanges(next, next); len(c) != 1 || c[0] != "Sin cambios" {
		t.Errorf("no changes = %v", c)
	}
}
