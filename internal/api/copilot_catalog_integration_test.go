package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sugsOf(m map[string]any, feature string) []map[string]any {
	var out []map[string]any
	for _, it := range m["items"].([]any) {
		x := it.(map[string]any)
		if x["feature"] == feature {
			out = append(out, x)
		}
	}
	return out
}

func (e *env) productJSON(id string) map[string]any {
	_, m := e.do("GET", "/v1/products/"+id, e.adminA, nil)
	if p, ok := m["product"].(map[string]any); ok {
		return p
	}
	return m
}

// A suggestion changes only its field, keeps the value before, can be
// undone (unless someone edited since), is audited, and a rejected one is
// not proposed again.
func TestCatalogCopilot(t *testing.T) {
	e := setup(t)
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, map[string]any{"id": "color", "name": "Color", "kind": "select", "options": []string{"Rojo", "Azul"}, "filterable": true})
	e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": "playera", "title": "Playera básica roja", "description": "Algodón peinado. Cuello redondo.", "status": "active", "brand": "Urbana",
		"variants": []map[string]any{{"sku": "PL-1", "title": "Única"}}})
	e.drain()
	id := e.idOf("playera")
	list := e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil)
	attr := sugsOf(list, "attributes")
	seo := sugsOf(list, "seo")
	if len(attr) != 1 || !strings.Contains(string(asJSON(attr[0]["proposal"])), `"color":"Rojo"`) || !strings.Contains(string(asJSON(attr[0]["sources"])), "roja") {
		t.Fatalf("attribute suggestion: %v", attr)
	}
	if len(seo) != 1 || !strings.Contains(string(asJSON(seo[0]["proposal"])), "Playera básica roja · Urbana") {
		t.Fatalf("seo suggestion: %v", seo)
	}
	// Asking again does not duplicate them.
	if again := e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil); len(again["items"].([]any)) != len(list["items"].([]any)) {
		t.Fatal("suggestions duplicated")
	}
	// Apply: only the attribute changes, visible at once in the storefront read model.
	e.must(200, "POST", "/v1/ai/suggestions/"+attr[0]["id"].(string)+"/apply", e.adminA, nil)
	e.drain()
	if p := e.productJSON(id); p["attributes"].(map[string]any)["color"] != "Rojo" || p["title"] != "Playera básica roja" {
		t.Fatalf("after apply: %v", p)
	}
	// Undo: back to no color.
	e.must(200, "POST", "/v1/ai/suggestions/"+attr[0]["id"].(string)+"/undo", e.adminA, nil)
	e.drain()
	if p := e.productJSON(id); p["attributes"] != nil && p["attributes"].(map[string]any)["color"] != nil {
		t.Fatalf("after undo: %v", p["attributes"])
	}
	// SEO: apply, then the merchant edits it by hand; undo must not erase that edit.
	sid := seo[0]["id"].(string)
	e.must(200, "POST", "/v1/ai/suggestions/"+sid+"/apply", e.adminA, nil)
	e.drain()
	_, _ = e.db.System.Exec(context.Background(), `UPDATE products SET seo_title='Mi título a mano', version=version+1 WHERE tenant_id='store-a' AND id=$1`, id)
	_ = e.db.EmitAI(context.Background(), "store-a", "catalog.product.changed", id)
	e.drain()
	if c, m := e.do("POST", "/v1/ai/suggestions/"+sid+"/undo", e.adminA, nil); c != 409 {
		t.Fatalf("undo over a manual edit: %d %v", c, m)
	}
	// Reject: never proposed again.
	list = e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil)
	if re := sugsOf(list, "attributes"); len(re) != 1 || re[0]["status"] != "undone" {
		t.Fatalf("undone suggestion state: %v", re)
	}
	// Audit: apply, undo, apply — chained and whole.
	var n int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id='store-a' AND action LIKE 'ai.suggestion.%'`).Scan(&n)
	if n != 3 {
		t.Fatalf("audited steps = %d", n)
	}
	if broken, err := e.db.VerifyAudit(context.Background(), "store-a"); err != nil || broken != 0 {
		t.Fatalf("audit chain broken at %d (%v)", broken, err)
	}
	// Permissions: a storefront key sees nothing; another store sees nothing.
	if c, _ := e.do("GET", "/v1/products/"+id+"/suggestions", e.sfA, nil); c != 403 {
		t.Fatalf("storefront key: %d", c)
	}
	if c, _ := e.do("POST", "/v1/ai/suggestions/"+sid+"/apply", e.adminB, nil); c != 404 {
		t.Fatalf("other store applied a suggestion: %d", c)
	}
	// Deleting the product deletes its suggestions; the audit keeps the trail.
	e.must(204, "DELETE", "/v1/products/"+id, e.adminA, nil)
	var left int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM ai_suggestions WHERE tenant_id='store-a' AND target_id=$1`, id).Scan(&left)
	if left != 0 {
		t.Fatalf("%d suggestions left after deleting the product", left)
	}
}

// Rejected suggestions are not proposed again; near duplicates are flagged
// and cannot be "applied".
func TestCatalogCopilotRejectAndDuplicates(t *testing.T) {
	e := setup(t)
	for _, h := range []string{"taza-blanca", "taza-blanca-2"} {
		e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": h, "title": "Taza de cerámica blanca 350 ml", "description": "Apta para microondas.", "status": "active", "seoTitle": "x", "seoDescription": "y",
			"variants": []map[string]any{{"sku": "SKU-" + h, "title": "Única"}}})
	}
	e.embed()
	id := e.idOf("taza-blanca")
	list := e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil)
	dup := sugsOf(list, "duplicate")
	if len(dup) != 1 || !strings.Contains(string(asJSON(dup[0]["sources"])), "casi igual") {
		t.Fatalf("duplicate: %v", list)
	}
	if c, _ := e.do("POST", "/v1/ai/suggestions/"+dup[0]["id"].(string)+"/apply", e.adminA, nil); c != 400 {
		t.Fatalf("a duplicate notice was applied: %d", c)
	}
	e.must(204, "POST", "/v1/ai/suggestions/"+dup[0]["id"].(string)+"/reject", e.adminA, nil)
	list = e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil)
	if d := sugsOf(list, "duplicate"); len(d) != 1 || d[0]["status"] != "rejected" {
		t.Fatalf("rejected duplicate proposed again: %v", d)
	}
	// Scanning the catalog proposes for every product, once.
	scan := e.must(200, "POST", "/v1/ai/suggestions/scan", e.adminA, nil)
	if scan["new"].(float64) < 1 || scan["byFeature"].(map[string]any)["duplicate"].(float64) != 2 {
		t.Fatalf("scan: %v", scan)
	}
	if again := e.must(200, "POST", "/v1/ai/suggestions/scan", e.adminA, nil); again["new"].(float64) != 0 {
		t.Fatalf("second scan proposed again: %v", again)
	}
}

func asJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// A product without category gets the category its closest products share.
func TestCatalogCopilotCategory(t *testing.T) {
	e := setup(t)
	c := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Tazas", "handle": "tazas"})
	cid, _ := c["id"].(string)
	if cid == "" {
		cid = c["category"].(map[string]any)["id"].(string)
	}
	for i, title := range []string{"Taza de cerámica azul", "Taza de cerámica verde", "Taza de cerámica negra", "Taza de cerámica roja"} {
		e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": "taza-" + string(rune('a'+i)), "title": title, "description": "Taza de cerámica de 350 ml.", "status": "active", "categories": []string{cid},
			"variants": []map[string]any{{"sku": "TZ-" + string(rune('A'+i)), "title": "Única"}}})
	}
	e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": "taza-sin", "title": "Taza de cerámica amarilla", "description": "Taza de cerámica de 350 ml.", "status": "active",
		"variants": []map[string]any{{"sku": "TZ-X", "title": "Única"}}})
	e.embed()
	id := e.idOf("taza-sin")
	cat := sugsOf(e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil), "category")
	if len(cat) != 1 || !strings.Contains(string(asJSON(cat[0]["proposal"])), cid) || !strings.Contains(string(asJSON(cat[0]["sources"])), "«Tazas»") {
		t.Fatalf("category suggestion: %v", cat)
	}
	e.must(200, "POST", "/v1/ai/suggestions/"+cat[0]["id"].(string)+"/apply", e.adminA, nil)
	e.drain()
	if p := e.productJSON(id); len(p["categories"].([]any)) != 1 || p["categories"].([]any)[0] != cid {
		t.Fatalf("category not applied: %v", p["categories"])
	}
}

// Images whose alt text says nothing get one from the product; applying
// writes it to the media library, undo restores the text before.
func TestCatalogCopilotAltText(t *testing.T) {
	e := setup(t)
	var ids []string
	for i := uint8(1); i <= 2; i++ {
		c, m := e.upload("/v1/catalog/media", e.adminA, "foto.png", testPNG(i), map[string]string{"alt": "x"})
		if c != 201 && c != 200 {
			t.Fatalf("upload: %d %v", c, m)
		}
		md, _ := m["media"].(map[string]any)
		ids = append(ids, md["id"].(string))
	}
	// Alt text is required; imports fill it with whatever they have, like a
	// file name: that is what this simulates.
	for i, mid := range ids {
		_, _ = e.db.System.Exec(context.Background(), `UPDATE media SET alt=$2 WHERE tenant_id='store-a' AND id=$1`, mid, []string{"IMG_4021.JPG", "foto"}[i])
		_ = e.db.EmitAI(context.Background(), "store-a", "catalog.media.changed", mid)
	}
	e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": "lampara", "title": "Lámpara de escritorio LED", "status": "active", "media": ids, "seoTitle": "x", "seoDescription": "y",
		"variants": []map[string]any{{"sku": "LMP-1", "title": "Única"}}})
	e.drain()
	id := e.idOf("lampara")
	alt := sugsOf(e.must(200, "GET", "/v1/products/"+id+"/suggestions", e.adminA, nil), "alt")
	if len(alt) != 1 || !strings.Contains(string(asJSON(alt[0]["proposal"])), "Lámpara de escritorio LED, vista 2") {
		t.Fatalf("alt suggestion: %v", alt)
	}
	sid := alt[0]["id"].(string)
	e.must(200, "POST", "/v1/ai/suggestions/"+sid+"/apply", e.adminA, nil)
	e.drain()
	if m := e.must(200, "GET", "/v1/catalog/media/"+ids[0], e.adminA, nil); !strings.Contains(string(asJSON(m)), `"alt":"Lámpara de escritorio LED"`) {
		t.Fatalf("alt not applied: %v", m)
	}
	e.must(200, "POST", "/v1/ai/suggestions/"+sid+"/undo", e.adminA, nil)
	e.drain()
	if m := e.must(200, "GET", "/v1/catalog/media/"+ids[1], e.adminA, nil); !strings.Contains(string(asJSON(m)), `"alt":"foto"`) {
		t.Fatalf("alt not undone: %v", m)
	}
}
