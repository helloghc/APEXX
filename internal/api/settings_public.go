package api

import (
	"bytes"
	"context"
	"html"
	"html/template"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/sitecfg"
	"github.com/helloghc/apexx/internal/store"
)

// ---------- routing by Host and redirects ----------

type hostKey struct{}

// customHost reports whether the request arrived on a verified custom domain
// (so links are built without the /s/{tenant}/{site} prefix).
func customHost(r *http.Request) bool {
	v, _ := r.Context().Value(hostKey{}).(bool)
	return v
}

// platformPath lists the paths that never belong to a store, on any host.
func platformPath(p string) bool {
	for _, pre := range []string{"/s/", "/v1/", "/admin", "/internal/", "/healthz", "/readyz", "/metrics", "/favicon.ico"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func requestHost(r *http.Request) string {
	h := strings.ToLower(r.Host)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// siteRouting does two things before the router sees a request:
//
//  1. Custom domains: a request for a verified hostname is served as the
//     store it belongs to. "/" is the home page and "/p/tenis" the product,
//     without the /s/{tenant}/{site} prefix. Unknown hosts get the platform.
//  2. Redirects: an old path of the store answers 301/302 to its new place.
//     The lookup is one map read on the in-memory projection.
func (s *Server) siteRouting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Proj == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		tenant, site, rel, base := "", "", "", ""
		if ht, ok := s.Proj.Host(requestHost(r)); ok && !platformPath(path) {
			tenant, site, rel = ht.Tenant, ht.Site, path
			r = r.WithContext(context.WithValue(r.Context(), hostKey{}, true))
		} else if strings.HasPrefix(path, "/s/") {
			parts := strings.SplitN(path[3:], "/", 3)
			if len(parts) == 3 && parts[0] != "assets" && parts[0] != "fonts" && parts[1] != "api" && parts[1] != "assets" {
				tenant, site, rel = parts[0], parts[1], "/"+parts[2]
				base = "/s/" + tenant + "/" + site
			}
		}
		if tenant == "" {
			next.ServeHTTP(w, r)
			return
		}
		if t, ok := s.Proj.Tenant(tenant); ok {
			if rd, ok := t.Site().Redirects[sitecfg.NormalizePath(rel)]; ok {
				target := rd.Target
				if strings.HasPrefix(target, "/") {
					target = base + target
				}
				if r.URL.RawQuery != "" && !strings.Contains(target, "?") {
					target += "?" + r.URL.RawQuery
				}
				w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=60")
				http.Redirect(w, r, target, rd.Code)
				return
			}
		}
		if customHost(r) {
			if rel == "/" || rel == "" {
				rel = "/index"
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/s/" + tenant + "/" + site + rel
			r2.URL.RawPath = ""
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

// siteBase returns the prefix of every link of the store for this request.
func siteBase(r *http.Request) string {
	if customHost(r) {
		return ""
	}
	return "/s/" + r.PathValue("tenant") + "/" + r.PathValue("site")
}

// origin returns scheme://host for absolute URLs (sitemap, robots, canonical).
// The primary verified domain wins; otherwise the host of the request.
func (s *Server) origin(r *http.Request, site *projection.SiteView) (string, string) {
	if site.Primary != "" {
		return "https://" + site.Primary, ""
	}
	scheme := "http"
	if r.TLS != nil || (s.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host, siteBase(r)
}

// tlsAsk answers Caddy's on-demand TLS question: "may I get a certificate
// for this host?". Only verified domains of some store are allowed, so nobody
// can make the platform request certificates for arbitrary names.
func (s *Server) tlsAsk(w http.ResponseWriter, r *http.Request) {
	if !askLimiter.allow(s.clientIP(r)) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	host := sitecfg.NormalizeHost(r.URL.Query().Get("domain"))
	if _, ok := s.Proj.Host(host); !ok {
		http.Error(w, "unknown domain", http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

var askLimiter = &shopLimiter{n: 600}

// ---------- menus inside the theme chrome ----------

var (
	headerMenuRe = regexp.MustCompile(`(?s)(<ul data-menu="header">)(.*?)(</ul>)`)
	footerMenuRe = regexp.MustCompile(`<template data-menu="footer"></template>`)
	relLinkRe    = regexp.MustCompile(`(href|action)="([A-Za-z0-9_][^":]*)"`)
)

func renderLinks(b *strings.Builder, links []sitecfg.Link, sub string) {
	for _, l := range links {
		b.WriteString(`<li><a href="` + html.EscapeString(l.URL) + `"`)
		if l.NewTab {
			b.WriteString(` target="_blank" rel="noopener"`)
		}
		b.WriteString(">" + html.EscapeString(l.Label) + "</a>")
		if len(l.Children) > 0 {
			b.WriteString(`<ul class="` + sub + `">`)
			renderLinks(b, l.Children, sub)
			b.WriteString("</ul>")
		}
		b.WriteString("</li>")
	}
}

// applyMenus puts the navigation menus of the store (Configuración →
// Navegación) into theme HTML. Themes mark where the header menu goes with
// <ul data-menu="header"> and the footer columns with <template
// data-menu="footer">. Without a menu the text typed in the section stays.
func applyMenus(doc []byte, site *projection.SiteView, base string) []byte {
	if m, ok := site.Menus["header"]; ok && len(m.Items) > 0 && bytes.Contains(doc, []byte(`data-menu="header"`)) {
		var b strings.Builder
		renderLinks(&b, sitecfg.Resolve(m.Items, base), "hdr-sub")
		inner := b.String()
		doc = headerMenuRe.ReplaceAllFunc(doc, func(match []byte) []byte {
			return []byte(`<ul data-menu="header">` + inner + `</ul>`)
		})
	}
	if m, ok := site.Menus["footer"]; ok && len(m.Items) > 0 && bytes.Contains(doc, []byte(`data-menu="footer"`)) {
		// Top-level links with children become columns; loose links share one.
		var b strings.Builder
		var loose []sitecfg.Link
		for _, l := range sitecfg.Resolve(m.Items, base) {
			if len(l.Children) == 0 {
				loose = append(loose, l)
				continue
			}
			b.WriteString(`<nav class="ft-col" aria-label="` + html.EscapeString(l.Label) + `"><p class="ft-title">` + html.EscapeString(l.Label) + `</p><ul>`)
			renderLinks(&b, l.Children, "ft-sub")
			b.WriteString("</ul></nav>")
		}
		if len(loose) > 0 {
			b.WriteString(`<nav class="ft-col" aria-label="` + html.EscapeString(m.Name) + `"><p class="ft-title">` + html.EscapeString(m.Name) + `</p><ul>`)
			renderLinks(&b, loose, "ft-sub")
			b.WriteString("</ul></nav>")
		}
		doc = footerMenuRe.ReplaceAll(doc, []byte(b.String()))
	}
	return doc
}

// absolutize rewrites page-relative links of the theme chrome ("c", "cart")
// so they also work on nested pages such as /p/{handle} or /politicas/{slug}.
func absolutize(doc []byte, base string) []byte {
	return relLinkRe.ReplaceAll(doc, []byte(`$1="`+base+`/$2"`))
}

// decorate finishes the data of a shop page: store name, links prefix and
// the theme chrome with the store's menus.
func (s *Server) decorate(r *http.Request, t *projection.Tenant, pd pageData) pageData {
	site := t.Site()
	if site.Settings.Name != "" {
		pd.Store = site.Settings.Name
	}
	pd.Base = siteBase(r)
	chrome := func(h template.HTML) template.HTML {
		return template.HTML(absolutize(applyMenus([]byte(h), site, pd.Base), pd.Base))
	}
	pd.Chrome.Header, pd.Chrome.Footer = chrome(pd.Chrome.Header), chrome(pd.Chrome.Footer)
	return pd
}

// ---------- public pages ----------

func (s *Server) policyPage(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	p, found := t.Site().Policies[r.PathValue("slug")]
	if !found || !p.Published {
		w.WriteHeader(http.StatusNotFound)
		pd.Title = "Página no encontrada"
		s.renderShop(w, "policy", struct {
			pageData
			Policy  *sitecfg.Policy
			Body    template.HTML
			Updated string
			Others  []sitecfg.Policy
		}{pageData: pd}, false)
		return
	}
	var others []sitecfg.Policy
	for _, o := range t.Site().Policies {
		if o.Published && o.Slug != p.Slug {
			others = append(others, o)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].Title < others[j].Title })
	pd.Title, pd.Description = p.Title, sitecfg.Excerpt(p.Body, 155)
	s.renderShop(w, "policy", struct {
		pageData
		Policy  *sitecfg.Policy
		Body    template.HTML
		Updated string
		Others  []sitecfg.Policy
	}{pd, &p, template.HTML(p.Body), humanDay(p.UpdatedAt.In(t.Site().Settings.Location())), others}, true)
}

var months = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

func humanDay(t time.Time) string {
	return strings.TrimLeft(t.Format("02"), "0") + " de " + months[t.Month()-1] + " de " + t.Format("2006")
}

// sitemap lists every public URL of the store with the real date each page
// last changed (the product's own update time, the policy's edit time, the
// last publication of builder pages). VTEX sitemaps carry no usable lastmod.
func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := s.openSite(tenant, r.PathValue("site"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	site := t.Site()
	origin, base := s.origin(r, site)
	root := origin + base
	var urls []sitecfg.SitemapURL
	var published time.Time
	if rels := st.Releases(); len(rels) > 0 {
		for _, rel := range rels {
			if rel.PublishedAt.After(published) {
				published = rel.PublishedAt
			}
		}
	}
	var pages []string
	for h := range st.Templates() {
		if _, _, err := st.Live(h); err == nil {
			pages = append(pages, h)
		}
	}
	sort.Strings(pages)
	for _, h := range pages {
		loc := root + "/" + h
		if h == "index" && base == "" {
			loc = root + "/"
		}
		urls = append(urls, sitecfg.SitemapURL{Loc: loc, LastMod: published})
	}
	var newest time.Time
	var products []sitecfg.SitemapURL
	after := ""
	for len(products) < 49000 {
		items, next := t.Catalog.List(after, 1000)
		for _, p := range items {
			if p.Status != "active" {
				continue
			}
			products = append(products, sitecfg.SitemapURL{Loc: root + "/p/" + p.Handle, LastMod: p.UpdatedAt})
			if p.UpdatedAt.After(newest) {
				newest = p.UpdatedAt
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	urls = append(urls, sitecfg.SitemapURL{Loc: root + "/c", LastMod: newest})
	urls = append(urls, products...)
	var slugs []string
	for slug, p := range site.Policies {
		if p.Published {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		urls = append(urls, sitecfg.SitemapURL{Loc: root + "/politicas/" + slug, LastMod: site.Policies[slug].UpdatedAt})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=300")
	_, _ = w.Write(sitecfg.Sitemap(urls))
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(r.PathValue("tenant"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	site := t.Site()
	origin, base := s.origin(r, site)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=300")
	_, _ = w.Write([]byte(sitecfg.Robots(origin, base, site.Settings.Robots)))
}

func (s *Server) logo(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if _, ok := s.Proj.Tenant(tenant); !ok {
		http.NotFound(w, r)
		return
	}
	a, err := s.DB.GetAsset(r.Context(), tenant, "logo")
	if err == store.ErrNotFound {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("ETag", `"`+a.SHA+`"`)
	h.Set("Content-Security-Policy", "default-src 'none'")
	if r.URL.Query().Get("v") == a.SHA {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "public, max-age=300")
	}
	_, _ = w.Write(a.Body)
}

// publicMenus exposes the menus, resolved to URLs, for themes and headless
// storefronts. ?base= overrides the link prefix (default /s/{tenant}/main).
func (s *Server) publicMenus(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	base := "/s/" + tenant + "/main"
	if customHost(r) {
		base = ""
	}
	out := map[string]any{}
	for id, m := range t.Site().Menus {
		out[id] = map[string]any{"name": m.Name, "items": sitecfg.Resolve(m.Items, base)}
	}
	w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=30")
	writeJSON(w, http.StatusOK, map[string]any{"menus": out})
}

// extendShopConfig adds what the checkout form needs to know about the store.
func (s *Server) extendShopConfig(tenant string, out map[string]any) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return
	}
	st := t.Site().Settings
	co := map[string]any{"guestCheckout": st.Checkout.GuestCheckout, "phoneRequired": st.Checkout.PhoneRequired,
		"notesField": st.Checkout.NotesField, "termsCheckbox": st.Checkout.TermsCheckbox}
	if p, ok := t.Site().Policies[st.Checkout.TermsPolicy]; ok && p.Published {
		co["termsPolicy"], co["termsTitle"] = p.Slug, p.Title
	}
	out["checkout"] = co
	out["store"] = map[string]any{"name": st.Name, "logoUrl": st.LogoURL}
}
