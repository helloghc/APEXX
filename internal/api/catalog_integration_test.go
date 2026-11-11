package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func testPNG(seed uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{seed, uint8(x / 4), uint8(y / 3), 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// upload posts a multipart form and returns status and body.
func (e *env) upload(path, key, filename string, data []byte, fields map[string]string) (int, map[string]any) {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if data != nil {
		fw, _ := mw.CreateFormFile("file", filename)
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	return e.do2("POST", path, key, &body, mw.FormDataContentType())
}

func (e *env) do2(method, path, key string, body io.Reader, ctype string) (int, map[string]any) {
	e.t.Helper()
	c, raw, _ := e.raw(method, path, key, body, ctype)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return c, m
}

func (e *env) raw(method, path, key string, body io.Reader, ctype string) (int, []byte, http.Header) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func items(m map[string]any) []map[string]any {
	raw, _ := m["items"].([]any)
	out := make([]map[string]any, len(raw))
	for i, x := range raw {
		out[i], _ = x.(map[string]any)
	}
	return out
}

func (e *env) must(want int, method, path, key string, body any) map[string]any {
	e.t.Helper()
	c, m := e.do(method, path, key, body)
	if c != want {
		e.t.Fatalf("%s %s = %d, want %d: %v", method, path, c, want, m)
	}
	return m
}

// TestCatalogRLSIsolation proves that every new table is closed to other
// tenants at the database level, not only in the API.
func TestCatalogRLSIsolation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cat := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Ropa"})
	e.must(201, "POST", "/v1/catalog/brands", e.adminA, map[string]any{"name": "Norte"})
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, map[string]any{"name": "Material", "kind": "text"})
	e.must(201, "POST", "/v1/catalog/collections", e.adminA, map[string]any{"title": "Destacados", "kind": "manual", "productIds": []string{"P1"}})
	e.must(201, "POST", "/v1/catalog/terms/tags", e.adminA, map[string]any{"name": "verano"})
	if c, m := e.upload("/v1/catalog/media", e.adminA, "a.png", testPNG(1), map[string]string{"alt": "Foto"}); c != 201 {
		t.Fatalf("upload: %d %v", c, m)
	}
	if c, m := e.do2("POST", "/v1/catalog/imports", e.adminA, strings.NewReader("handle,title,sku\nx,X,X-1\n"), "text/csv"); c != 201 {
		t.Fatalf("import preview: %d %v", c, m)
	}

	tables := []string{"categories", "brands", "collections", "attribute_defs", "catalog_terms", "media", "import_jobs"}
	for _, tbl := range tables {
		var n int
		// 1. Without a tenant the application role sees nothing.
		if err := e.db.App.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: app role without tenant saw %d rows (err %v)", tbl, n, err)
		}
		// 2. Tenant A sees its row; tenant B sees none of A's rows.
		_ = e.db.Tx(ctx, "store-a", func(tx pgx.Tx) error { return tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n) })
		if n == 0 {
			t.Errorf("%s: store-a does not see its own row; the test would prove nothing", tbl)
		}
		_ = e.db.Tx(ctx, "store-b", func(tx pgx.Tx) error { return tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n) })
		if n != 0 {
			t.Errorf("%s: store-b sees %d rows of store-a", tbl, n)
		}
		// 3. Tenant B cannot move, update or delete A's rows, nor write rows as A.
		err := e.db.Tx(ctx, "store-b", func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, "UPDATE "+tbl+" SET tenant_id='store-b' WHERE tenant_id='store-a'")
			if err == nil && ct.RowsAffected() != 0 {
				return fmt.Errorf("updated %d rows", ct.RowsAffected())
			}
			ct, err = tx.Exec(ctx, "DELETE FROM "+tbl+" WHERE tenant_id='store-a'")
			if err == nil && ct.RowsAffected() != 0 {
				return fmt.Errorf("deleted %d rows", ct.RowsAffected())
			}
			return nil
		})
		if err != nil {
			t.Errorf("%s: %v", tbl, err)
		}
		var forced bool
		_ = e.db.System.QueryRow(ctx, "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname=$1", tbl).Scan(&forced)
		if !forced {
			t.Errorf("%s: row level security is not enabled and forced", tbl)
		}
	}
	// WITH CHECK: B cannot insert a row that belongs to A.
	err := e.db.Tx(ctx, "store-b", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO categories (tenant_id, id, handle, name) VALUES ('store-a','x','intruso','Intruso')`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("store-b inserted into store-a: %v", err)
	}

	// API level: B sees none of it and cannot touch it.
	for _, p := range []string{"categories", "brands", "collections", "attributes", "media", "imports", "terms/tags"} {
		if got := items(e.must(200, "GET", "/v1/catalog/"+p, e.adminB, nil)); len(got) != 0 {
			t.Errorf("store-b lists %d %s of store-a", len(got), p)
		}
	}
	id := cat["id"].(string)
	if c, _ := e.do("PUT", "/v1/catalog/categories/"+id, e.adminB, map[string]any{"name": "Hackeada"}); c != 404 {
		t.Fatalf("store-b updated a category of store-a: %d", c)
	}
	if c, _ := e.do("DELETE", "/v1/catalog/categories/"+id, e.adminB, nil); c != 404 {
		t.Fatalf("store-b deleted a category of store-a: %d", c)
	}
	// A read-only member of the same store cannot write.
	if c, _ := e.do("POST", "/v1/catalog/categories", e.sfA, map[string]any{"name": "X"}); c != 403 {
		t.Fatalf("storefront key created a category: %d", c)
	}
}

func TestProductEditorSaveAndVariants(t *testing.T) {
	e := setup(t)
	brand := e.must(201, "POST", "/v1/catalog/brands", e.adminA, map[string]any{"name": "Norte"})["id"].(string)
	ropa := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Ropa"})["id"].(string)
	play := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Playeras", "parentId": ropa})["id"].(string)
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, map[string]any{"name": "Material", "kind": "select", "options": []string{"Algodón", "Lino"}, "filterable": true})
	_, up := e.upload("/v1/catalog/media", e.adminA, "playera.png", testPNG(2), map[string]string{"alt": "Playera azul de frente"})
	mediaID := up["media"].(map[string]any)["id"].(string)

	// The matrix generator creates every combination and keeps existing variants.
	opts := []map[string]any{{"name": "Color", "values": []string{"Azul", "Rojo"}}, {"name": "Talla", "values": []string{"S", "M"}}}
	gen := e.must(200, "POST", "/v1/catalog/variants/generate", e.adminA, map[string]any{"skuBase": "Playera", "options": opts})
	variants := gen["variants"].([]any)
	if len(variants) != 4 || len(gen["created"].([]any)) != 4 {
		t.Fatalf("matrix: %v", gen)
	}
	if c, m := e.do("POST", "/v1/catalog/variants/generate", e.adminA, map[string]any{"skuBase": "x", "options": []map[string]any{{"name": "Talla", "values": []string{"S", "S"}}}}); c != 400 {
		t.Fatalf("duplicate option value accepted: %d %v", c, m)
	}
	variants[0].(map[string]any)["mediaId"] = mediaID
	prices := map[string]any{}
	stock := map[string]any{}
	for i, v := range variants {
		sku := v.(map[string]any)["sku"].(string)
		prices[sku] = map[string]any{"amount": 29900 + i*1000, "compareAt": 39900}
		stock[sku] = map[string]any{"w1": 5 + i}
	}
	body := map[string]any{
		"handle": "playera-norte", "title": "Playera Norte", "status": "draft", "brandId": brand, "categories": []string{play},
		"tags": []string{"verano", "Verano", "básicos"}, "type": "Playera", "options": opts, "variants": variants,
		"attributes": map[string]string{"material": "algodón"}, "seoTitle": "Playera Norte de algodón", "media": []string{mediaID},
		"related": map[string]any{"similar": []string{"P1"}, "accessories": []string{"P2"}}, "prices": prices, "stock": stock,
	}
	created := e.must(201, "POST", "/v1/products", e.adminA, body)
	id := created["id"].(string)

	d := e.must(200, "GET", "/v1/products/"+id, e.adminA, nil)
	if d["brand"] != "Norte" || d["status"] != "draft" || len(d["variants"].([]any)) != 4 || d["variantCount"].(float64) != 4 {
		t.Fatalf("detail: %v", d)
	}
	if tags := d["tags"].([]any); len(tags) != 2 {
		t.Fatalf("tags must be de-duplicated ignoring case: %v", tags)
	}
	if d["attributes"].(map[string]any)["material"] != "Algodón" {
		t.Fatalf("select attribute not stored canonically: %v", d["attributes"])
	}
	firstSKU := variants[0].(map[string]any)["sku"].(string)
	if p := d["pricing"].(map[string]any)[firstSKU].(map[string]any); p["amount"].(float64) != 29900 || p["compareAt"].(float64) != 39900 || p["currency"] != "MXN" {
		t.Fatalf("pricing: %v", p)
	}
	if lv := d["levels"].(map[string]any)[firstSKU].([]any)[0].(map[string]any); lv["locationId"] != "w1" || lv["onHand"].(float64) != 5 {
		t.Fatalf("levels: %v", lv)
	}
	if d["stock"].(float64) != 5+6+7+8 || d["priceMin"].(float64) != 29900 || d["priceMax"].(float64) != 32900 {
		t.Fatalf("row summary: stock=%v min=%v max=%v", d["stock"], d["priceMin"], d["priceMax"])
	}
	if d["thumb"].(map[string]any)["alt"] != "Playera azul de frente" || len(d["relatedItems"].(map[string]any)) != 2 {
		t.Fatalf("thumb/related: %v %v", d["thumb"], d["relatedItems"])
	}
	if d["variants"].([]any)[0].(map[string]any)["title"] != "Azul / S" || d["variants"].([]any)[0].(map[string]any)["mediaId"] != mediaID {
		t.Fatalf("variant: %v", d["variants"].([]any)[0])
	}

	// Validation with field-level errors.
	bad := map[string]any{"handle": "Mal Handle", "title": "", "variants": []map[string]any{{"sku": "S1"}, {"sku": "con espacio"}},
		"brandId": "br_nope", "media": []string{"m_nope"}, "attributes": map[string]string{"material": "Seda"}}
	c, m := e.do("POST", "/v1/products", e.adminA, bad)
	if c != 400 {
		t.Fatalf("invalid product accepted: %d", c)
	}
	got := map[string]bool{}
	for _, x := range m["errors"].([]any) {
		got[x.(map[string]any)["field"].(string)] = true
	}
	for _, f := range []string{"title", "handle", "variants[0].sku", "variants[1].sku", "brandId", "media[0]", "attributes.material"} {
		if !got[f] {
			t.Errorf("missing field error %q in %v", f, got)
		}
	}
	// A duplicated handle is a conflict.
	clash := map[string]any{"handle": "playera-norte", "title": "Otra", "variants": []map[string]any{{"sku": "OTRA-1"}}}
	if c, _ := e.do("POST", "/v1/products", e.adminA, clash); c != 409 {
		t.Fatalf("duplicate handle: %d", c)
	}

	// Optimistic concurrency and removal of a variant (its price and stock go with it).
	body["variants"] = variants[:3]
	delete(prices, variants[3].(map[string]any)["sku"].(string))
	delete(stock, variants[3].(map[string]any)["sku"].(string))
	if c, _ := e.do("PUT", "/v1/products/"+id, e.adminA, body, "If-Match", `"99"`); c != 412 {
		t.Fatalf("stale If-Match: %d", c)
	}
	// A missing combination is allowed (not every colour comes in every size).
	if c, m := e.do("PUT", "/v1/products/"+id, e.adminA, body, "If-Match", `"1"`); c != 200 {
		t.Fatalf("save without one combination: %d %v", c, m)
	}
	d = e.must(200, "GET", "/v1/products/"+id, e.adminA, nil)
	gone := variants[3].(map[string]any)["sku"].(string)
	if _, still := d["pricing"].(map[string]any)[gone]; still || len(d["variants"].([]any)) != 3 {
		t.Fatalf("removed variant left data behind: %v", d["pricing"])
	}
	if c, _ := e.do("GET", "/v1/inventory/"+gone, e.adminA, nil); c != 404 {
		var n int
		_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM stock_levels WHERE sku=$1`, gone).Scan(&n)
		if n != 0 {
			t.Fatalf("stock rows of a removed SKU remain: %d", n)
		}
	}

	// Renaming the brand renames it on its products.
	e.must(200, "PUT", "/v1/catalog/brands/"+brand, e.adminA, map[string]any{"name": "Norte MX", "handle": "norte", "active": true})
	if d = e.must(200, "GET", "/v1/products/"+id, e.adminA, nil); d["brand"] != "Norte MX" {
		t.Fatalf("brand rename did not reach the product: %v", d["brand"])
	}

	// List filters, search and sort.
	list := e.must(200, "GET", "/v1/products?status=draft&category="+ropa+"&brand="+brand+"&q=playra", e.adminA, nil)
	if list["total"].(float64) != 1 || items(list)[0]["id"] != id {
		t.Fatalf("filters (category includes subcategories, typo tolerant search): %v", list)
	}
	sorted := items(e.must(200, "GET", "/v1/products?sort=-price&fields=id,priceMin", e.adminA, nil))
	if sorted[0]["id"] != id {
		t.Fatalf("sort by price desc: %v", sorted)
	}
	if c, _ := e.do("GET", "/v1/products?sort=color", e.adminA, nil); c != 400 {
		t.Fatalf("unknown sort accepted: %d", c)
	}
	page1 := e.must(200, "GET", "/v1/products?limit=2&sort=title", e.adminA, nil)
	page2 := e.must(200, "GET", "/v1/products?limit=2&sort=title&cursor="+page1["nextCursor"].(string), e.adminA, nil)
	if len(items(page1)) != 2 || len(items(page2)) != 2 || items(page1)[1]["id"] == items(page2)[0]["id"] {
		t.Fatalf("cursor pagination: %v | %v", items(page1), items(page2))
	}

	// Deleting a category with children is refused; deleting the leaf detaches products.
	if c, m := e.do("DELETE", "/v1/catalog/categories/"+ropa, e.adminA, nil); c != 409 || m["code"] != "has_children" {
		t.Fatalf("delete parent: %d %v", c, m)
	}
	if c, m := e.do("PUT", "/v1/catalog/categories/"+ropa, e.adminA, map[string]any{"name": "Ropa", "parentId": play}); c != 400 {
		t.Fatalf("cycle accepted: %d %v", c, m)
	}
	e.must(204, "DELETE", "/v1/catalog/categories/"+play, e.adminA, nil)
	if d = e.must(200, "GET", "/v1/products/"+id, e.adminA, nil); d["categories"] != nil {
		t.Fatalf("deleted category still on the product: %v", d["categories"])
	}

	// Delete needs admin; afterwards the product is gone from API and projection.
	e.must(204, "DELETE", "/v1/products/"+id, e.adminA, nil)
	if c, _ := e.do("GET", "/v1/products/"+id, e.adminA, nil); c != 404 {
		t.Fatalf("deleted product still readable: %d", c)
	}
}

func TestBulkEditAndScheduledPublish(t *testing.T) {
	e := setup(t)
	cat := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Ofertas"})["id"].(string)
	brand := e.must(201, "POST", "/v1/catalog/brands", e.adminA, map[string]any{"name": "Sierra"})["id"].(string)
	ids := []string{"P1", "P2"}
	if r := e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": ids, "action": "status", "status": "archived"}); r["changed"].(float64) != 2 {
		t.Fatalf("bulk status: %v", r)
	}
	if r := e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": ids, "action": "status", "status": "archived"}); r["changed"].(float64) != 0 {
		t.Fatalf("bulk status twice should change nothing: %v", r)
	}
	e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": ids, "action": "category_add", "categoryId": cat})
	e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": ids, "action": "brand", "brandId": brand})
	// -10 % keeping the old price as the "before" price: 100.00 → 90.00.
	if r := e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": ids, "action": "price_percent", "percent": -10, "keepOldPrice": true}); r["changed"].(float64) != 2 {
		t.Fatalf("bulk price: %v", r)
	}
	d := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	if d["status"] != "archived" || d["brand"] != "Sierra" || d["categories"].([]any)[0] != cat {
		t.Fatalf("bulk result: %v", d)
	}
	if p := d["pricing"].(map[string]any)["S1"].(map[string]any); p["amount"].(float64) != 9000 || p["compareAt"].(float64) != 12000 {
		t.Fatalf("price after -10%%: %v", p) // the seed already had a compare-at price, which is kept
	}
	if d3 := e.must(200, "GET", "/v1/products/P3", e.adminA, nil); d3["status"] != "active" || d3["pricing"].(map[string]any)["S3"].(map[string]any)["amount"].(float64) != 10000 {
		t.Fatalf("a product outside the selection changed: %v", d3)
	}
	for _, bad := range []map[string]any{
		{"ids": ids, "action": "status", "status": "publicado"},
		{"ids": []string{}, "action": "status", "status": "active"},
		{"ids": ids, "action": "category_add", "categoryId": "cat_nope"},
		{"ids": ids, "action": "price_percent", "percent": -95},
		{"ids": ids, "action": "borrar"},
	} {
		if c, _ := e.do("POST", "/v1/products/bulk", e.adminA, bad); c != 400 {
			t.Errorf("bulk %v = %d, want 400", bad, c)
		}
	}

	// Scheduled publishing: a draft with a past date goes live on the next tick.
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	e.must(200, "PUT", "/v1/products/P3", e.adminA, map[string]any{"handle": "p3", "title": "Producto 3", "status": "draft", "publishAt": past,
		"variants": []map[string]any{{"sku": "S3", "title": "Única"}}})
	n, err := e.db.PublishDue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("PublishDue = %d, %v", n, err)
	}
	e.drain()
	if d := e.must(200, "GET", "/v1/products/P3", e.adminA, nil); d["status"] != "active" || d["publishAt"] != nil {
		t.Fatalf("scheduled product: status=%v publishAt=%v", d["status"], d["publishAt"])
	}
	if c, _ := e.do("PUT", "/v1/products/P3", e.adminA, map[string]any{"handle": "p3", "title": "Producto 3", "status": "active", "publishAt": past,
		"variants": []map[string]any{{"sku": "S3"}}}); c != 400 {
		t.Fatalf("publishAt on an active product accepted: %d", c)
	}
}

func TestMediaLibrary(t *testing.T) {
	e := setup(t)
	// Validation: type by content, alt required.
	if c, m := e.upload("/v1/catalog/media", e.adminA, "foto.png", []byte("<script>alert(1)</script>"), map[string]string{"alt": "x"}); c != 415 || m["code"] != "unsupported_image" {
		t.Fatalf("html disguised as png: %d %v", c, m)
	}
	if c, m := e.upload("/v1/catalog/media", e.adminA, "foto.png", testPNG(3), map[string]string{"alt": "  "}); c != 400 {
		t.Fatalf("missing alt: %d %v", c, m)
	}
	if c, _ := e.upload("/v1/catalog/media", e.adminA, "", nil, map[string]string{"alt": "x"}); c != 400 {
		t.Fatalf("missing file: %d", c)
	}
	if c, _ := e.upload("/v1/catalog/media", e.sfA, "foto.png", testPNG(3), map[string]string{"alt": "x"}); c != 403 {
		t.Fatalf("storefront key uploaded: %d", c)
	}
	c, m := e.upload("/v1/catalog/media", e.adminA, "../../etc/Foto Tenis.PNG", testPNG(3), map[string]string{"alt": "Tenis de perfil"})
	if c != 201 {
		t.Fatalf("upload: %d %v", c, m)
	}
	md := m["media"].(map[string]any)
	id := md["id"].(string)
	if md["filename"] != "Foto Tenis.PNG" || md["width"].(float64) != 800 || md["height"].(float64) != 600 || md["alt"] != "Tenis de perfil" {
		t.Fatalf("media: %v", md)
	}
	widths := map[float64]bool{}
	for _, v := range md["variants"].([]any) {
		widths[v.(map[string]any)["w"].(float64)] = true
	}
	if !widths[160] || !widths[320] || !widths[640] || !widths[800] || widths[1024] {
		t.Fatalf("renditions (never upscaled): %v", widths)
	}
	if !regexp.MustCompile(`/media/store-a/` + id + `/160\.\w+ 160w, .*800w`).MatchString(md["srcset"].(string)) {
		t.Fatalf("srcset: %v", md["srcset"])
	}
	// The same file again is recognised instead of stored twice.
	if c, m := e.upload("/v1/catalog/media", e.adminA, "otra.png", testPNG(3), map[string]string{"alt": "Otra"}); c != 200 || m["duplicate"] != true {
		t.Fatalf("duplicate: %d %v", c, m)
	}

	// Public delivery: long immutable cache, right type, no originals, no other tenant.
	c, body, hdr := e.raw("GET", md["url"].(string), "", nil, "")
	if c != 200 || !strings.Contains(hdr.Get("Cache-Control"), "immutable") || !strings.Contains(hdr.Get("Cache-Control"), "max-age=31536000") ||
		!strings.HasPrefix(hdr.Get("Content-Type"), "image/") || hdr.Get("X-Content-Type-Options") != "nosniff" || len(body) == 0 {
		t.Fatalf("serve: %d %v", c, hdr)
	}
	if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("served rendition does not decode: %v", err)
	}
	for _, p := range []string{"/media/store-a/" + id + "/original.png", "/media/store-b/" + id + "/160.jpg", "/media/store-a/" + id + "/..%2F..%2Fx.jpg", "/media/store-a/" + id + "/999.jpg"} {
		if c, _, _ := e.raw("GET", p, "", nil, ""); c != 404 {
			t.Errorf("GET %s = %d, want 404", p, c)
		}
	}
	if c, _ := e.do("GET", "/v1/catalog/media/"+id, e.adminB, nil); c != 404 {
		t.Fatalf("store-b read media of store-a: %d", c)
	}

	// Alt text can be fixed but never emptied.
	if c, _ := e.do("PATCH", "/v1/catalog/media/"+id, e.adminA, map[string]any{"alt": ""}); c != 400 {
		t.Fatalf("empty alt accepted: %d", c)
	}
	e.must(200, "PATCH", "/v1/catalog/media/"+id, e.adminA, map[string]any{"alt": "Tenis rojos de perfil"})
	if l := e.must(200, "GET", "/v1/catalog/media?q=rojos", e.adminA, nil); l["total"].(float64) != 1 {
		t.Fatalf("search by alt: %v", l)
	}

	// In use: delete asks for confirmation, then detaches and removes the files.
	e.must(200, "PUT", "/v1/products/P1", e.adminA, map[string]any{"handle": "p1", "title": "Producto 1", "media": []string{id},
		"variants": []map[string]any{{"sku": "S1", "title": "Única", "mediaId": id}}})
	if c, m := e.do("DELETE", "/v1/catalog/media/"+id, e.adminA, nil); c != 409 || m["code"] != "media_in_use" {
		t.Fatalf("delete in use: %d %v", c, m)
	}
	e.must(204, "DELETE", "/v1/catalog/media/"+id+"?force=true", e.adminA, nil)
	d := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	if d["media"] != nil || d["variants"].([]any)[0].(map[string]any)["mediaId"] != nil || d["thumb"] != nil {
		t.Fatalf("deleted image still attached: media=%v thumb=%v", d["media"], d["thumb"])
	}
	if c, _, _ := e.raw("GET", md["url"].(string), "", nil, ""); c != 404 {
		t.Fatalf("file still served after delete: %d", c)
	}
}

func (e *env) waitImport(id string) map[string]any {
	e.t.Helper()
	for i := 0; i < 200; i++ {
		j := e.must(200, "GET", "/v1/catalog/imports/"+id, e.adminA, nil)
		if j["status"] == "done" || j["status"] == "failed" {
			return j
		}
		time.Sleep(25 * time.Millisecond)
	}
	e.t.Fatal("import did not finish")
	return nil
}

func TestCSVImportPreviewApplyAndExport(t *testing.T) {
	e := setup(t)
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, map[string]any{"name": "Material", "kind": "text"})
	src := "handle,title,status,brand,categories,tags,option1_name,option1_value,sku,price,compare_at_price,stock,weight_grams,attr:material\n" +
		"tenis-sierra,Tenis Sierra,active,Sierra,Calzado > Tenis,\"running, verano\",Talla,25,TS-25,1299.50,1500,7,400,Malla\n" +
		"tenis-sierra,,,,,,Talla,26,TS-26,1299.50,,0,410,\n" +
		"p1,Producto uno renombrado,,,,,,,S1,150,,3,,\n" + // updates an existing product and its price/stock
		"malo,Sin precio válido,active,,,,,,MALO-1,abc,,1,,\n" + // row error: skipped
		"roba-sku,Roba SKU,active,,,,,,S2,10,,1,,\n" // S2 belongs to p2: row error
	c, pv := e.do2("POST", "/v1/catalog/imports", e.adminA, strings.NewReader(src), "text/csv")
	if c != 201 {
		t.Fatalf("preview: %d %v", c, pv)
	}
	id := pv["id"].(string)
	sum := pv["summary"].(map[string]any)
	if pv["status"] != "preview" || sum["rows"].(float64) != 5 || sum["creates"].(float64) != 1 || sum["updates"].(float64) != 1 || sum["invalid"].(float64) != 2 {
		t.Fatalf("summary: %v", sum)
	}
	errRows := map[float64]string{}
	for _, x := range pv["errors"].([]any) {
		errRows[x.(map[string]any)["row"].(float64)] = x.(map[string]any)["column"].(string)
	}
	if errRows[5] != "price" || errRows[6] != "sku" || len(errRows) != 2 {
		t.Fatalf("row errors: %v", errRows)
	}
	// The preview wrote nothing.
	if c, _ := e.do("GET", "/v1/products?q=tenis", e.adminA, nil); c != 200 {
		t.Fatal(c)
	}
	if l := e.must(200, "GET", "/v1/products?q=sierra", e.adminA, nil); l["total"].(float64) != 0 {
		t.Fatalf("preview created products: %v", l["total"])
	}
	if c, m := e.do2("POST", "/v1/catalog/imports", e.adminA, strings.NewReader("title\nx\n"), "text/csv"); c != 422 || m["code"] != "invalid_csv" {
		t.Fatalf("csv without required columns: %d %v", c, m)
	}

	// Apply: runs in the background with progress; only valid products are written.
	if c, m := e.do("POST", "/v1/catalog/imports/"+id+"/apply", e.adminA, nil); c != 202 {
		t.Fatalf("apply: %d %v", c, m)
	}
	job := e.waitImport(id)
	if job["status"] != "done" || job["total"].(float64) != 2 || job["processed"].(float64) != 2 || job["created"].(float64) != 1 || job["updated"].(float64) != 1 || job["failed"].(float64) != 0 {
		t.Fatalf("job: %v", job)
	}
	if c, _ := e.do("POST", "/v1/catalog/imports/"+id+"/apply", e.adminA, nil); c != 409 {
		t.Fatalf("second apply: %d", c)
	}
	l := items(e.must(200, "GET", "/v1/products?q=tenis+sierra", e.adminA, nil))
	if len(l) != 1 {
		t.Fatalf("imported product not found: %v", l)
	}
	d := e.must(200, "GET", "/v1/products/"+l[0]["id"].(string), e.adminA, nil)
	if d["brand"] != "Sierra" || d["brandId"] == nil || len(d["categories"].([]any)) != 1 || len(d["variants"].([]any)) != 2 ||
		d["options"].([]any)[0].(map[string]any)["name"] != "Talla" || d["attributes"].(map[string]any)["material"] != "Malla" {
		t.Fatalf("imported product: %v", d)
	}
	if p := d["pricing"].(map[string]any)["TS-25"].(map[string]any); p["amount"].(float64) != 129950 || p["compareAt"].(float64) != 150000 {
		t.Fatalf("imported price: %v", p)
	}
	if d["available"].(map[string]any)["TS-25"].(float64) != 7 || d["available"].(map[string]any)["TS-26"].(float64) != 0 {
		t.Fatalf("imported stock: %v", d["available"])
	}
	cats := items(e.must(200, "GET", "/v1/catalog/categories", e.adminA, nil))
	if len(cats) != 2 || cats[0]["name"] != "Calzado" || cats[1]["name"] != "Tenis" || cats[1]["parentId"] != cats[0]["id"] || cats[0]["productsTotal"].(float64) != 1 {
		t.Fatalf("category path was not created as a tree: %v", cats)
	}
	p1 := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	if p1["title"] != "Producto uno renombrado" || p1["pricing"].(map[string]any)["S1"].(map[string]any)["amount"].(float64) != 15000 || p1["available"].(map[string]any)["S1"].(float64) != 3 {
		t.Fatalf("update of an existing product: %v", p1)
	}
	if c, _ := e.do("GET", "/v1/products?q=malo", e.adminA, nil); c != 200 {
		t.Fatal(c)
	}
	if l := e.must(200, "GET", "/v1/products?q=roba", e.adminA, nil); l["total"].(float64) != 0 {
		t.Fatal("an invalid product was imported")
	}

	// Export reads back as a valid import with the same data.
	c, raw, hdr := e.raw("GET", "/v1/catalog/export.csv?q=tenis", e.adminA, nil, "")
	if c != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		t.Fatalf("export: %d %v", c, hdr)
	}
	rows, err := csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("export rows: %d %v", len(rows), err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	if rows[1][col["sku"]] != "TS-25" || rows[1][col["price"]] != "1299.50" || rows[1][col["compare_at_price"]] != "1500.00" || rows[1][col["stock"]] != "7" ||
		rows[1][col["categories"]] != "Calzado > Tenis" || rows[1][col["brand"]] != "Sierra" || rows[2][col["option1_value"]] != "26" || rows[1][col["attr:material"]] != "Malla" {
		t.Fatalf("export content: %v", rows[1:])
	}
	c, again := e.do2("POST", "/v1/catalog/imports", e.adminA, bytes.NewReader(raw), "text/csv")
	if c != 201 || len(again["errors"].([]any)) != 0 || again["summary"].(map[string]any)["updates"].(float64) != 1 {
		t.Fatalf("re-import of the export: %d %v", c, again)
	}
}

func TestStorefrontCategoryFacetsAndImages(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	ropa := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Ropa", "description": "Prendas para todos los días", "seoTitle": "Ropa cómoda"})["id"].(string)
	play := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Playeras", "parentId": ropa})["id"].(string)
	oculta := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Oculta", "active": false})
	_ = oculta
	norte := e.must(201, "POST", "/v1/catalog/brands", e.adminA, map[string]any{"name": "Norte"})["id"].(string)
	sierra := e.must(201, "POST", "/v1/catalog/brands", e.adminA, map[string]any{"name": "Sierra"})["id"].(string)
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, map[string]any{"name": "Material", "kind": "select", "options": []string{"Algodón", "Lino"}, "filterable": true})
	_, up := e.upload("/v1/catalog/media", e.adminA, "p1.png", testPNG(9), map[string]string{"alt": "Playera \"Norte\" <azul>"})
	mid := up["media"].(map[string]any)["id"].(string)
	put := func(id, handle, title, brand, cat, material string, price int, media []string) {
		sku := strings.Replace(id, "P", "S", 1)
		e.must(200, "PUT", "/v1/products/"+id, e.adminA, map[string]any{"handle": handle, "title": title, "brandId": brand, "categories": []string{cat},
			"attributes": map[string]string{"material": material}, "media": media, "seoDescription": "Descripción SEO de " + title,
			"variants": []map[string]any{{"sku": sku, "title": "Única"}}, "prices": map[string]any{sku: map[string]any{"amount": price}},
			"related": map[string]any{"similar": []string{}}})
	}
	put("P1", "p1", "Playera Norte", norte, play, "Algodón", 10000, []string{mid})
	put("P2", "p2", "Playera Sierra", sierra, play, "Lino", 20000, nil)
	put("P3", "p3", "Pantalón Sierra", sierra, ropa, "Algodón", 30000, nil)
	e.must(201, "POST", "/v1/catalog/collections", e.adminA, map[string]any{"title": "Hasta 250", "kind": "rules", "rules": map[string]any{"priceMax": 25000}})

	get := func(path string, want int) string {
		t.Helper()
		c, b, _ := e.raw("GET", path, "", nil, "")
		if c != want {
			t.Fatalf("GET %s = %d, want %d", path, c, want)
		}
		return string(b)
	}
	total := func(html string) string {
		m := regexp.MustCompile(`data-total="(\d+)"`).FindStringSubmatch(html)
		if m == nil {
			return "?"
		}
		return m[1]
	}
	facet := func(html, label string) string {
		m := regexp.MustCompile(`<span>` + regexp.QuoteMeta(label) + `</span> <span class="facet-count">(\d+)</span>`).FindStringSubmatch(html)
		if m == nil {
			return "missing"
		}
		return m[1]
	}

	// Category page: products of the subtree, child categories, brand and attribute facets with counts.
	page := get("/s/store-a/main/c/ropa", 200)
	if total(page) != "3" || facet(page, "Playeras") != "2" || facet(page, "Norte") != "1" || facet(page, "Sierra") != "2" || facet(page, "Algodón") != "2" || facet(page, "Lino") != "1" {
		t.Fatalf("category page counts: total=%s playeras=%s norte=%s sierra=%s algodon=%s lino=%s", total(page), facet(page, "Playeras"), facet(page, "Norte"), facet(page, "Sierra"), facet(page, "Algodón"), facet(page, "Lino"))
	}
	for _, want := range []string{"<h1>Ropa</h1>", "<title>Ropa cómoda · ", "Prendas para todos los días", `href="/s/store-a/main/c/playeras"`, `rel="canonical" href="/s/store-a/main/c/ropa"`} {
		if !strings.Contains(page, want) {
			t.Errorf("category page lacks %q", want)
		}
	}
	// The product image: srcset with real widths, width/height against layout shift, escaped alt.
	if !regexp.MustCompile(`<img class="pcard-media ph" src="/media/store-a/` + mid + `/800\.\w+" srcset="[^"]*160w[^"]*800w" sizes="[^"]+" width="800" height="600" alt="Playera &#34;Norte&#34; &lt;azul&gt;"`).MatchString(page) {
		t.Fatalf("card image markup is wrong:\n%s", page[strings.Index(page, "pgrid"):min(len(page), strings.Index(page, "pgrid")+900)])
	}

	// Filters live in the URL and combine; counts of a facet ignore its own selection.
	f := get("/s/store-a/main/c/ropa?marca=sierra", 200)
	if total(f) != "2" || facet(f, "Norte") != "1" || facet(f, "Algodón") != "1" || !strings.Contains(f, `noindex`) {
		t.Fatalf("brand filter: total=%s norte=%s algodon=%s", total(f), facet(f, "Norte"), facet(f, "Algodón"))
	}
	f = get("/s/store-a/main/c/ropa?marca=sierra&a.material=Lino", 200)
	if total(f) != "1" || !strings.Contains(f, "Playera Sierra") || strings.Contains(f, "Pantalón Sierra") {
		t.Fatalf("brand + attribute: total=%s", total(f))
	}
	f = get("/s/store-a/main/c?min=150&max=250", 200)
	if total(f) != "1" || !strings.Contains(f, "Playera Sierra") {
		t.Fatalf("price range: total=%s", total(f))
	}
	// Sort by price, descending and ascending.
	d := get("/s/store-a/main/c/ropa?orden=precio-desc", 200)
	a := get("/s/store-a/main/c/ropa?orden=precio-asc", 200)
	if !(strings.Index(d, "Pantalón Sierra") < strings.Index(d, "Playera Norte")) || !(strings.Index(a, "Playera Norte</h3>") < strings.Index(a, "Pantalón Sierra</h3>")) {
		t.Fatal("sort by price does not order the cards")
	}
	// Subcategory, collection, search inside a category.
	if p := get("/s/store-a/main/c/playeras", 200); total(p) != "2" || !strings.Contains(p, `<a href="/s/store-a/main/c/ropa">Ropa</a>`) {
		t.Fatalf("subcategory: total=%s (breadcrumb must link the parent)", total(p))
	}
	if p := get("/s/store-a/main/col/hasta-250", 200); total(p) != "2" || !strings.Contains(p, "<h1>Hasta 250</h1>") {
		t.Fatalf("collection page: total=%s", total(p))
	}
	if p := get("/s/store-a/main/c/ropa?q=pantalon", 200); total(p) != "1" {
		t.Fatalf("search inside category: total=%s", total(p))
	}
	// Unknown or inactive categories and collections are 404 pages, drafts are hidden.
	get("/s/store-a/main/c/no-existe", 404)
	get("/s/store-a/main/c/oculta", 404)
	get("/s/store-a/main/col/no-existe", 404)
	e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"ids": []string{"P3"}, "action": "status", "status": "draft"})
	if p := get("/s/store-a/main/c/ropa", 200); total(p) != "2" || strings.Contains(p, "Pantalón Sierra") {
		t.Fatalf("draft still listed: total=%s", total(p))
	}
	// Hostile parameters are harmless.
	x := get("/s/store-a/main/c/ropa?marca=%22%3E%3Cscript%3Ealert(1)%3C/script%3E&orden=x&pagina=-4&min=abc", 200)
	if strings.Contains(x, "<script>alert(1)") {
		t.Fatal("reflected parameter is not escaped")
	}

	// Product page: gallery image, specifications, SEO, breadcrumb through the category.
	pdp := get("/s/store-a/main/p/p1", 200)
	for _, want := range []string{`data-pdp-img`, `fetchpriority="high"`, "Especificaciones", "<td>Algodón</td>", `content="Descripción SEO de Playera Norte"`,
		`<a href="/s/store-a/main/c/playeras">Playeras</a>`, `"image":["/media/store-a/` + mid} {
		if !strings.Contains(pdp, want) {
			t.Errorf("product page lacks %q", want)
		}
	}
	// The home page built with the builder shows the image too.
	if home := get("/s/store-a/main/index", 200); strings.Contains(home, "Playera Norte") && !strings.Contains(home, "/media/store-a/"+mid) {
		t.Error("builder product cards do not show the product image")
	}
}
