package api

import (
	"strings"
	"testing"
)

// Image editor: edits are a recipe over the original (rotate swaps the
// sides, crop shrinks, undo brings the original back), a copy leaves the
// image untouched, the store's products see the new version, and another
// store can neither read nor edit it.
func TestImageEditor(t *testing.T) {
	e := setup(t)
	c, up := e.upload("/v1/catalog/media", e.adminA, "playera.png", testPNG(9), map[string]string{"alt": "Playera"})
	if c != 201 {
		t.Fatalf("upload = %d %v", c, up)
	}
	id := up["media"].(map[string]any)["id"].(string)
	edit := func(key string, body m) (int, map[string]any) {
		st, r := e.do("POST", "/v1/catalog/media/"+id+"/edit", key, body)
		if st >= 300 {
			return st, r
		}
		return st, r["media"].(map[string]any)
	}
	dims := func(v map[string]any) (float64, float64) { return v["width"].(float64), v["height"].(float64) }
	for _, bad := range []m{{"edits": m{"rotate": 45}}, {"edits": m{"brightness": 300}}, {"edits": m{"crop": m{"x": 0.8, "y": 0, "w": 0.5, "h": 1}}}} {
		if st, _ := edit(e.adminA, bad); st != 400 {
			t.Fatalf("accepted %v: %d", bad, st)
		}
	}
	st, v := edit(e.adminA, m{"edits": m{"rotate": 90, "crop": m{"x": 0, "y": 0, "w": 1, "h": 0.5}, "saturation": -100}})
	if w, h := dims(v); st != 200 || w != 600 || h != 400 {
		t.Fatalf("rotate+crop = %d %vx%v", st, w, h)
	}
	if !strings.Contains(toJSON(v["variants"]), "/e") || v["edits"] == nil {
		t.Fatalf("edited variants = %v", v)
	}
	// The library and the storefront read the new version.
	e.drain()
	tA, _ := e.server.Proj.Tenant("store-a")
	if mm, ok := tA.Taxo.Media(id); !ok || mm.Width != 600 {
		t.Fatalf("projection width = %v", mm.Width)
	}
	// Undo: no edits brings back the original size.
	if _, v = edit(e.adminA, m{"edits": m{}}); v["edits"] != nil {
		t.Fatalf("undo kept edits: %v", v["edits"])
	}
	if w, h := dims(v); w != 800 || h != 600 {
		t.Fatalf("undo = %vx%v", w, h)
	}
	// A copy is a new image; the first one stays as it is.
	st, cp := edit(e.adminA, m{"edits": m{"flipH": true, "brightness": 20}, "asCopy": true})
	if st != 201 || cp["id"] == id || !strings.Contains(cp["filename"].(string), "editada") {
		t.Fatalf("copy = %d %v", st, cp)
	}
	if st, _ := edit(e.adminA, m{"edits": m{}, "asCopy": true}); st != 400 {
		t.Fatalf("empty copy accepted: %d", st)
	}
	// Other stores and the storefront key.
	if st, _ := edit(e.adminB, m{"edits": m{"rotate": 90}}); st != 404 {
		t.Fatalf("store B edited store A's image: %d", st)
	}
	if st, _ := edit(e.sfA, m{"edits": m{"rotate": 90}}); st != 403 {
		t.Fatalf("storefront key edited an image: %d", st)
	}
}
