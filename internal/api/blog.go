package api

import (
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/sitecfg"
	"github.com/helloghc/apexx/internal/store"
)

// Blog: posts written in the panel (sanitized HTML), live from their
// publication time, with their own pages, SEO and sitemap entries. A post
// scheduled for later stays hidden until then; the store previews it with a
// signed link.

type blogPost struct {
	ID        string     `json:"id"`
	Handle    string     `json:"handle"`
	Title     string     `json:"title"`
	Excerpt   string     `json:"excerpt"`
	Body      string     `json:"body"`
	Cover     string     `json:"cover"`
	Author    string     `json:"author"`
	Tags      []string   `json:"tags"`
	Status    string     `json:"status"`
	PublishAt *time.Time `json:"publishAt"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

const blogCols = `id, handle, title, excerpt, body, cover, author, tags, status, publish_at, created_at, updated_at`

func scanPost(r pgx.CollectableRow) (blogPost, error) {
	var p blogPost
	return p, r.Scan(&p.ID, &p.Handle, &p.Title, &p.Excerpt, &p.Body, &p.Cover, &p.Author, &p.Tags, &p.Status, &p.PublishAt, &p.CreatedAt, &p.UpdatedAt)
}

var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

func (s *Server) routesBlog(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/blog", s.authed("builder", auth.Read, noStorefront(s.listPosts)))
	handle("POST /v1/blog", s.authed("builder", auth.Edit, s.savePost))
	handle("GET /v1/blog/{id}", s.authed("builder", auth.Read, noStorefront(s.getPost)))
	handle("PUT /v1/blog/{id}", s.authed("builder", auth.Edit, s.savePost))
	handle("DELETE /v1/blog/{id}", s.authed("builder", auth.Edit, s.deletePost))
	handle("GET /s/{tenant}/{site}/blog", s.shopBlog)
	handle("GET /s/{tenant}/{site}/blog/{handle}", s.shopBlogPost)
}

func (s *Server) listPosts(w http.ResponseWriter, r *http.Request) {
	var out []blogPost
	err := s.DB.Tx(r.Context(), principal(r).Tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+blogCols+` FROM blog_posts WHERE tenant_id=$1 ORDER BY COALESCE(publish_at, created_at) DESC LIMIT 200`, principal(r).Tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanPost)
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for i := range out {
		out[i].Body = "" // the list does not carry the bodies
	}
	if out == nil {
		out = []blogPost{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) postByID(r *http.Request, tenant, id string) (blogPost, error) {
	var p blogPost
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+blogCols+` FROM blog_posts WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		p, err = pgx.CollectExactlyOneRow(rows, scanPost)
		return err
	})
	return p, err
}

func (s *Server) getPost(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	p, err := s.postByID(r, pr.Tenant, r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.NotFound(w, "Artículo no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"post": p, "previewUrl": "/s/" + pr.Tenant + "/main/blog/" + p.Handle + "?vista=" + s.previewToken(pr.Tenant, p.ID)})
}

func (s *Server) previewToken(tenant, id string) string {
	b, err := s.Keys.Blind(tenant, "blog-preview", id)
	if err != nil {
		return ""
	}
	return id + "." + hex.EncodeToString(b[:10])
}

func (s *Server) savePost(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in blogPost
	if !decode(w, r, &in) {
		return
	}
	in.Title, in.Excerpt, in.Author = strings.TrimSpace(in.Title), strings.TrimSpace(in.Excerpt), strings.TrimSpace(in.Author)
	in.Handle = strings.TrimSpace(in.Handle)
	if in.Handle == "" {
		in.Handle = slugify(in.Title)
	}
	in.Body = sitecfg.SanitizeHTML(in.Body)
	var errs []apierr.FieldError
	switch {
	case in.Title == "" || len([]rune(in.Title)) > 160:
		errs = append(errs, apierr.FieldError{Field: "title", Code: "invalid", Message: "Escribe un título de hasta 160 caracteres"})
	case !handleRe.MatchString(in.Handle):
		errs = append(errs, apierr.FieldError{Field: "handle", Code: "invalid", Message: "Dirección con minúsculas, números y guiones"})
	}
	if len([]rune(in.Excerpt)) > 300 || len(in.Body) > 200_000 || len(in.Tags) > 20 || len([]rune(in.Author)) > 80 {
		errs = append(errs, apierr.FieldError{Field: "body", Code: "too_long", Message: "Resumen de hasta 300 caracteres, texto de hasta 200 KB y 20 etiquetas"})
	}
	if in.Status != "draft" && in.Status != "published" {
		in.Status = "draft"
	}
	if in.Status == "published" && in.PublishAt == nil {
		now := time.Now().UTC()
		in.PublishAt = &now
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa el artículo", errs...)
		return
	}
	for i, t := range in.Tags {
		in.Tags[i] = strings.TrimSpace(strings.ToLower(t))
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	id := r.PathValue("id")
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		var err error
		if id == "" {
			id = newRandomID("post_")
			_, err = tx.Exec(r.Context(), `INSERT INTO blog_posts (tenant_id, id, handle, title, excerpt, body, cover, author, tags, status, publish_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				pr.Tenant, id, in.Handle, in.Title, in.Excerpt, in.Body, in.Cover, in.Author, in.Tags, in.Status, in.PublishAt)
		} else {
			var ct pgconn.CommandTag
			ct, err = tx.Exec(r.Context(), `UPDATE blog_posts SET handle=$3, title=$4, excerpt=$5, body=$6, cover=$7, author=$8, tags=$9, status=$10, publish_at=$11, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				pr.Tenant, id, in.Handle, in.Title, in.Excerpt, in.Body, in.Cover, in.Author, in.Tags, in.Status, in.PublishAt)
			if err == nil && ct.RowsAffected() == 0 {
				return store.ErrNotFound
			}
		}
		if err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "blog.saved", id, map[string]any{"title": in.Title, "status": in.Status})
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Artículo no encontrado")
		return
	case err != nil && strings.Contains(err.Error(), "blog_posts_tenant_id_handle_key"):
		apierr.BadRequest(w, "Ya hay un artículo con esa dirección", apierr.FieldError{Field: "handle", Code: "taken", Message: "Ya hay un artículo con esa dirección"})
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	r.SetPathValue("id", id)
	s.getPost(w, r)
}

func (s *Server) deletePost(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var n int64
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM blog_posts WHERE tenant_id=$1 AND id=$2`, pr.Tenant, r.PathValue("id"))
		n = ct.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "blog.deleted", r.PathValue("id"), nil)
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if n == 0 {
		apierr.NotFound(w, "Artículo no encontrado")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// livePosts returns the published posts already due, newest first.
func (s *Server) livePosts(r *http.Request, tenant, tag string, limit int) ([]blogPost, error) {
	var out []blogPost
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+blogCols+` FROM blog_posts WHERE tenant_id=$1 AND status='published' AND publish_at <= now()
			AND ($2='' OR $2 = ANY(tags)) ORDER BY publish_at DESC LIMIT $3`, tenant, tag, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanPost)
		return err
	})
	return out, err
}

type blogCard struct {
	Handle, Title, Excerpt, Date, Cover string
}

type blogPage struct {
	pageData
	Posts []blogCard
	Tag   string
	Post  *struct {
		Title, Date, Author, Cover string
		Body                       template.HTML
		Tags                       []string
		Preview                    bool
	}
}

func (s *Server) shopBlog(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	tag := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tema")))
	posts, err := s.livePosts(r, t.ID, tag, 60)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	pg := blogPage{pageData: pd, Tag: tag}
	pg.Title, pg.Description = "Blog", "Artículos y novedades de "+t.Site().Settings.Name
	if tag != "" {
		pg.NoIndex = true
	}
	loc := t.Site().Settings.Location()
	for _, p := range posts {
		c := blogCard{Handle: p.Handle, Title: p.Title, Excerpt: firstNonEmpty(p.Excerpt, sitecfg.Excerpt(p.Body, 180)), Date: humanDay(p.PublishAt.In(loc))}
		if m, ok := t.Taxo.Media(p.Cover); ok {
			c.Cover = viewMedia(t.ID, m).URL
		}
		pg.Posts = append(pg.Posts, c)
	}
	s.renderShop(w, "blog", pg, true)
}

func (s *Server) shopBlogPost(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	var p blogPost
	err := s.DB.Tx(r.Context(), t.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+blogCols+` FROM blog_posts WHERE tenant_id=$1 AND handle=$2`, t.ID, r.PathValue("handle"))
		if err != nil {
			return err
		}
		p, err = pgx.CollectExactlyOneRow(rows, scanPost)
		return err
	})
	live := err == nil && p.Status == "published" && p.PublishAt != nil && !p.PublishAt.After(time.Now())
	// A draft or a scheduled post is visible only with the store's signed preview link.
	preview := err == nil && !live && r.URL.Query().Get("vista") != "" && r.URL.Query().Get("vista") == s.previewToken(t.ID, p.ID)
	if !live && !preview {
		w.WriteHeader(http.StatusNotFound)
		pd.Title, pd.NoIndex = "Artículo no encontrado", true
		s.renderShop(w, "blog", blogPage{pageData: pd}, false)
		return
	}
	loc := t.Site().Settings.Location()
	pg := blogPage{pageData: pd}
	pg.Title, pg.Description = p.Title, firstNonEmpty(p.Excerpt, sitecfg.Excerpt(p.Body, 155))
	pg.Canonical = pd.Base + "/blog/" + p.Handle
	pg.NoIndex = preview
	post := &struct {
		Title, Date, Author, Cover string
		Body                       template.HTML
		Tags                       []string
		Preview                    bool
	}{Title: p.Title, Author: p.Author, Body: template.HTML(p.Body), Tags: p.Tags, Preview: preview}
	if p.PublishAt != nil {
		post.Date = humanDay(p.PublishAt.In(loc))
	}
	if m, ok := t.Taxo.Media(p.Cover); ok {
		post.Cover = viewMedia(t.ID, m).URL
		pg.OGImage = post.Cover
	}
	pg.Post = post
	s.renderShop(w, "blog", pg, !preview)
}
