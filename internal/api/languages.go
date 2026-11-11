package api

import (
	"bytes"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/i18n"
	"github.com/helloghc/apexx/internal/store"
)

// The storefront is written in Spanish and shown in the languages each store
// turns on. The language travels in the URL (?hl=en), so every language has
// its own address for search engines and shared caches never mix them.

func (s *Server) getLanguages(w http.ResponseWriter, r *http.Request) {
	var cfg i18n.Config
	if _, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "i18n", "config", &cfg); err != nil {
		internalError(w, s.Log, err)
		return
	}
	cfg.Normalize()
	httpx.JSON(w, r, http.StatusOK, map[string]any{"available": i18n.Languages, "default": i18n.Default, "config": cfg})
}

func (s *Server) putLanguages(w http.ResponseWriter, r *http.Request) {
	var cfg i18n.Config
	if !decode(w, r, &cfg) {
		return
	}
	for _, l := range cfg.Enabled {
		if !i18n.Known(l) {
			apierr.BadRequest(w, "Idioma no disponible: "+l)
			return
		}
	}
	cfg.Normalize()
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "i18n", "config", store.KeyLanguages, cfg); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": cfg})
}

// langWriter holds a storefront page until it is complete, so it can be shown
// in the shopper's language.
type langWriter struct {
	http.ResponseWriter
	lang   string
	status int
	buf    bytes.Buffer
}

func (w *langWriter) WriteHeader(code int)        { w.status = code }
func (w *langWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }

// pageLang tells a page handler which language its templates should use.
func pageLang(w http.ResponseWriter) string {
	if lw, ok := w.(*langWriter); ok {
		return lw.lang
	}
	return i18n.Default
}

// localized serves storefront pages in the language asked for with ?hl=.
// Stores with only Spanish pass straight through.
func (s *Server) localized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Proj == nil || !strings.HasPrefix(r.URL.Path, "/s/") {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.SplitN(r.URL.Path[3:], "/", 3)
		// The storefront script asks for its messages in the shopper's language.
		if hl := r.Header.Get("X-Apexx-Lang"); hl != "" && len(parts) >= 2 && parts[1] == "api" {
			if t, ok := s.Proj.Tenant(parts[0]); ok && t.Languages().Has(hl) {
				lw := &langWriter{ResponseWriter: w, lang: hl, status: http.StatusOK}
				next.ServeHTTP(lw, r)
				body := lw.buf.Bytes()
				if strings.Contains(w.Header().Get("Content-Type"), "json") {
					body = localizeJSON(body, i18n.With(hl, t.Languages().Own[hl]))
					w.Header().Del("Content-Length")
				}
				w.WriteHeader(lw.status)
				_, _ = w.Write(body)
				return
			}
		}
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		if len(parts) < 2 || parts[0] == "assets" || parts[0] == "fonts" || parts[1] == "api" || parts[1] == "assets" {
			next.ServeHTTP(w, r)
			return
		}
		t, ok := s.Proj.Tenant(parts[0])
		if !ok || len(t.Languages().Enabled) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		cfg := t.Languages()
		lang := r.URL.Query().Get("hl")
		if !cfg.Has(lang) {
			lang = i18n.Default
		}
		lw := &langWriter{ResponseWriter: w, lang: lang, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		body := lw.buf.Bytes()
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			origin, _ := s.origin(r, t.Site())
			if isCustomHost(r) {
				origin += r.URL.Path[len("/s/"+parts[0]+"/"+parts[1]):]
			} else {
				origin += r.URL.Path
			}
			body = localizePage(body, lang, cfg, origin, r.URL.Query())
			w.Header().Del("Content-Length")
			w.Header().Set("Content-Language", lang)
		}
		w.WriteHeader(lw.status)
		_, _ = w.Write(body)
	})
}

func isCustomHost(r *http.Request) bool {
	v, _ := r.Context().Value(hostKey{}).(bool)
	return v
}

// withLang adds the language to an internal link.
func withLang(href, lang string) string {
	if lang == i18n.Default || href == "" || href[0] != '/' || strings.HasPrefix(href, "//") || strings.HasPrefix(href, "/s/assets/") || strings.HasPrefix(href, "/s/fonts/") || strings.HasPrefix(href, "/media/") || strings.Contains(href, "hl=") {
		return href
	}
	frag := ""
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href, frag = href[:i], href[i:]
	}
	sep := "?"
	if strings.Contains(href, "?") {
		sep = "&"
	}
	return href + sep + "hl=" + lang + frag
}

// localizePage translates a finished page, keeps the language in its links
// and tells search engines where the other languages live.
func localizePage(page []byte, lang string, cfg *i18n.Config, pageURL string, q url.Values) []byte {
	out := string(page)
	if lang != i18n.Default {
		out = i18n.Markup(out, i18n.With(lang, cfg.Own[lang]), i18n.Options{Href: func(h string) string { return withLang(h, lang) }, FormParam: lang})
		tag := lang
		if l, ok := i18n.Find(lang); ok {
			tag = l.Tag
		}
		// "<title>Carrito · Tienda</title>": the page name is ours, the store name is not.
		if a := strings.Index(out, "<title>"); a >= 0 {
			if b := strings.Index(out[a:], " · "); b > 0 && !strings.Contains(out[a:a+b], "<") {
				name := html.UnescapeString(out[a+7 : a+b])
				if tr, ok := i18n.With(lang, cfg.Own[lang])(name); ok {
					out = out[:a+7] + html.EscapeString(tr) + out[a+b:]
				}
			}
		}
		out = strings.Replace(out, `<html lang="es">`, `<html lang="`+tag+`" data-hl="`+lang+`">`, 1)
		out = strings.Replace(out, `/s/assets/shop.js?v=`, `/s/assets/shop.js?hl=`+lang+`&amp;v=`, 1)
	}
	link := func(code string) string {
		v := url.Values{}
		for k, vals := range q {
			if k != "hl" {
				v[k] = vals
			}
		}
		if code != i18n.Default {
			v.Set("hl", code)
		}
		if enc := v.Encode(); enc != "" {
			return pageURL + "?" + enc
		}
		return pageURL
	}
	var head, nav strings.Builder
	nav.WriteString(`<nav class="langs" aria-label="Idioma / Language"><ul>`)
	for _, l := range i18n.Languages {
		if l.Code != i18n.Default && !cfg.Has(l.Code) {
			continue
		}
		u := html.EscapeString(link(l.Code))
		head.WriteString(`<link rel="alternate" hreflang="` + l.Tag + `" href="` + u + `">`)
		cur := ""
		if l.Code == lang {
			cur = ` aria-current="true"`
		}
		nav.WriteString(`<li><a href="` + u + `" hreflang="` + l.Tag + `" lang="` + l.Tag + `"` + cur + `>` + html.EscapeString(l.Name) + `</a></li>`)
	}
	head.WriteString(`<link rel="alternate" hreflang="x-default" href="` + html.EscapeString(link(i18n.Default)) + `">`)
	nav.WriteString(`</ul></nav>`)
	out = strings.Replace(out, "</head>", head.String()+"</head>", 1)
	if i := strings.LastIndex(out, "</body>"); i >= 0 {
		out = out[:i] + nav.String() + out[i:]
	}
	return []byte(out)
}

// localizedPages parses the storefront templates once per language, with
// Apexx's own phrases already translated.
func localizedPages(names ...string) map[string]map[string]*template.Template {
	read := func(name string) string {
		b, err := shopFS.ReadFile("shop/" + name + ".tmpl")
		if err != nil {
			panic(err)
		}
		return string(b)
	}
	layout := read("layout")
	out := map[string]map[string]*template.Template{}
	for _, l := range i18n.Languages {
		m := map[string]*template.Template{}
		for _, p := range names {
			src := layout + read(p)
			if l.Code != i18n.Default {
				src = i18n.Markup(src, i18n.Builtin(l.Code), i18n.Options{})
			}
			m[p] = template.Must(template.New(p).Funcs(shopFuncs).Parse(src))
		}
		out[l.Code] = m
	}
	return out
}

var shopScripts sync.Map // language -> translated storefront script

// shopScript returns the storefront script in that language.
func shopScript(lang string) []byte {
	if v, ok := shopScripts.Load(lang); ok {
		return v.([]byte)
	}
	b, _ := shopFS.ReadFile("shop/shop.js")
	if lang != i18n.Default && i18n.Known(lang) {
		b = []byte(i18n.Script(string(b), i18n.Builtin(lang)))
	}
	shopScripts.Store(lang, b)
	return b
}

// localizeJSON translates the messages inside an API answer. Only whole
// phrases found in the dictionary change, so codes and data stay as they are.
func localizeJSON(body []byte, look i18n.Lookup) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return body
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case string:
			return i18n.Phrase(t, look)
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
		}
		return x
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(walk(v)); err != nil {
		return body
	}
	return out.Bytes()
}
