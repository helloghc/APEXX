package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Blog: only due posts are public; a scheduled one shows up by itself at its
// time and can be previewed before with the signed link; bodies are sanitized.
func TestBlog(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	if st, _ := e.do("POST", "/v1/blog", e.sfA, m{"title": "x"}); st != 401 && st != 403 {
		t.Fatalf("storefront key wrote a post: %d", st)
	}
	live := e.must(200, "POST", "/v1/blog", e.adminA, m{"title": "Cómo cuidar tu ropa de lino", "status": "published", "author": "Ana",
		"body": `<p>Lávala en frío.</p><script>alert(1)</script><p onclick="x()">Sin cloro.</p>`, "tags": []string{"Cuidados"}})["post"].(map[string]any)
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	sched := e.must(200, "POST", "/v1/blog", e.adminA, m{"title": "Lanzamiento de invierno", "status": "published", "publishAt": future, "body": "<p>Pronto.</p>"})
	e.must(200, "POST", "/v1/blog", e.adminA, m{"title": "Borrador", "status": "draft", "body": "<p>x</p>"})
	if st, _ := e.do("POST", "/v1/blog", e.adminA, m{"title": "Otro", "handle": live["handle"]}); st != 400 {
		t.Fatalf("repeated handle accepted: %d", st)
	}
	get := func(path string) (int, string) {
		res, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	st, list := get("/s/store-a/main/blog")
	if st != 200 || !strings.Contains(list, "Cómo cuidar tu ropa de lino") || strings.Contains(list, "Lanzamiento de invierno") || strings.Contains(list, "Borrador") {
		t.Fatalf("blog list = %d", st)
	}
	st, page := get("/s/store-a/main/blog/" + live["handle"].(string))
	if st != 200 || strings.Contains(page, "alert(1)") || strings.Contains(page, "onclick") || !strings.Contains(page, "Lávala en frío.") {
		t.Fatalf("post page = %d", st)
	}
	if _, tagged := get("/s/store-a/main/blog?tema=cuidados"); !strings.Contains(tagged, "Cómo cuidar") {
		t.Fatal("tag filter lost the post")
	}
	sp := sched["post"].(map[string]any)
	if st, _ := get("/s/store-a/main/blog/" + sp["handle"].(string)); st != 404 {
		t.Fatalf("scheduled post visible early: %d", st)
	}
	if st, body := get(sched["previewUrl"].(string)); st != 200 || !strings.Contains(body, "Vista previa") || !strings.Contains(body, "noindex") {
		t.Fatalf("preview = %d", st)
	}
	if st, _ := get("/s/store-a/main/blog/" + sp["handle"].(string) + "?vista=" + sp["id"].(string) + ".00"); st != 404 {
		t.Fatalf("forged preview = %d", st)
	}
	// At its time it is live by itself.
	_, _ = e.db.System.Exec(t.Context(), `UPDATE blog_posts SET publish_at=now() - interval '1 minute' WHERE id=$1`, sp["id"])
	if st, _ := get("/s/store-a/main/blog/" + sp["handle"].(string)); st != 200 {
		t.Fatalf("scheduled post not live at its time: %d", st)
	}
	if _, sm := get("/s/store-a/main/sitemap.xml"); !strings.Contains(sm, "/blog/"+live["handle"].(string)) {
		t.Fatal("post missing from the sitemap")
	}
	if st, _ := get("/s/store-b/main/blog/" + live["handle"].(string)); st == 200 {
		t.Fatal("store B shows store A's post")
	}
}
