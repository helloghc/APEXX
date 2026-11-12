package api

import (
	"bytes"
	"encoding/json"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/i18n"
	"github.com/helloghc/apexx/internal/notify"
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
				r = r.WithContext(notify.WithLang(r.Context(), hl))
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
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		cfg := t.Languages()
		markets := t.Markets(mainChannel)
		if len(cfg.Enabled) == 0 && len(markets) < 2 {
			next.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		mk := s.marketFor(r, t, parts[0], parts[1])
		lang := q.Get("hl")
		if lang == "" && mk != "" {
			if c, _ := t.Channel(mk); c.Language != "" {
				lang = c.Language // a market opens in its own language
			}
		}
		if lang != i18n.Default && !cfg.Has(lang) {
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
			body = localizePage(body, page{lang: lang, mk: mk, cfg: cfg, markets: markets, url: origin, q: q})
			w.Header().Del("Content-Length")
			w.Header().Set("Content-Language", lang)
		}
		w.WriteHeader(lw.status)
		_, _ = w.Write(body)
	})
}

// marketParam returns the market asked for in the address when the store
// offers it to shoppers; "" means the main channel.
func marketParam(t interface {
	Markets(string) []cart.Channel
}, mk string) string {
	if mk == "" || mk == mainChannel {
		return ""
	}
	for _, c := range t.Markets(mainChannel) {
		if c.ID == mk {
			return mk
		}
	}
	return ""
}

type hostMarketKey struct{}

// marketFor is the market a storefront page opens in: the one in the address
// (?mk=), else the domain's, else the storefront's; "" is the main channel.
func (s *Server) marketFor(r *http.Request, t interface {
	Markets(string) []cart.Channel
}, tenant, site string) string {
	if mk := marketParam(t, r.URL.Query().Get("mk")); mk != "" || r.URL.Query().Get("mk") == mainChannel {
		return mk
	}
	if v, _ := r.Context().Value(hostMarketKey{}).(string); v != "" {
		return marketParam(t, v)
	}
	return marketParam(t, s.Proj.SiteMarket(tenant, site))
}

func isCustomHost(r *http.Request) bool {
	v, _ := r.Context().Value(hostKey{}).(bool)
	return v
}

// withLang adds the language to an internal link.
func withLang(href, lang string) string { return withParams(href, lang, "") }

// withParams keeps the language and the market in an internal link.
func withParams(href, lang, mk string) string {
	if href == "" || href[0] == '#' || strings.HasPrefix(href, "//") || strings.HasPrefix(href, "/s/assets/") || strings.HasPrefix(href, "/s/fonts/") || strings.HasPrefix(href, "/media/") {
		return href
	}
	// Relative links ("p/playera") stay in the store too; anything with a
	// scheme (https:, mailto:, tel:) leaves it and is not touched.
	if href[0] != '/' {
		if i := strings.IndexAny(href, ":/?#"); i >= 0 && href[i] == ':' {
			return href
		}
	}
	frag := ""
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href, frag = href[:i], href[i:]
	}
	add := func(k, v string) {
		if v == "" || strings.Contains(href, k+"=") {
			return
		}
		sep := "?"
		if strings.Contains(href, "?") {
			sep = "&"
		}
		href += sep + k + "=" + v
	}
	if lang != i18n.Default {
		add("hl", lang)
	}
	add("mk", mk)
	return href + frag
}

// page is what localizePage needs to know about the request.
type page struct {
	lang, mk string
	cfg      *i18n.Config
	markets  []cart.Channel
	url      string
	q        url.Values
}

// localizePage translates a finished page, keeps the language and the market
// in its links and tells search engines where the other versions live.
func localizePage(body []byte, p page) []byte {
	lang, cfg := p.lang, p.cfg
	out := string(body)
	keep := func(h string) string { return withParams(h, lang, p.mk) }
	if lang != i18n.Default {
		out = i18n.Markup(out, i18n.With(lang, cfg.Own[lang]), i18n.Options{Href: keep, FormParam: lang})
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
	} else if p.mk != "" {
		out = i18n.Markup(out, func(string) (string, bool) { return "", false }, i18n.Options{Href: keep})
	}
	if p.mk != "" {
		out = strings.Replace(out, `<html lang="`, `<html data-mk="`+html.EscapeString(p.mk)+`" lang="`, 1)
	}
	link := func(code, mk string) string {
		v := url.Values{}
		for k, vals := range p.q {
			if k != "hl" && k != "mk" {
				v[k] = vals
			}
		}
		if code != i18n.Default {
			v.Set("hl", code)
		}
		if mk != "" {
			v.Set("mk", mk)
		}
		if enc := v.Encode(); enc != "" {
			return p.url + "?" + enc
		}
		return p.url
	}
	var head, nav strings.Builder
	nav.WriteString(`<nav class="langs" aria-label="Idioma y país / Language and country">`)
	if len(cfg.Enabled) > 0 {
		nav.WriteString(`<ul>`)
		for _, l := range i18n.Languages {
			if l.Code != i18n.Default && !cfg.Has(l.Code) {
				continue
			}
			u := html.EscapeString(link(l.Code, p.mk))
			if p.mk == "" {
				head.WriteString(`<link rel="alternate" hreflang="` + l.Tag + `" href="` + u + `">`)
			}
			cur := ""
			if l.Code == lang {
				cur = ` aria-current="true"`
			}
			nav.WriteString(`<li><a href="` + u + `" hreflang="` + l.Tag + `" lang="` + l.Tag + `"` + cur + `>` + html.EscapeString(l.Name) + `</a></li>`)
		}
		nav.WriteString(`</ul>`)
	}
	if len(p.markets) > 1 {
		nav.WriteString(`<ul class="markets">`)
		for i, c := range p.markets {
			mk := c.ID
			if i == 0 {
				mk = ""
			}
			ml := lang
			if c.Language != "" && (c.Language == i18n.Default || cfg.Has(c.Language)) {
				ml = c.Language
			}
			u := html.EscapeString(link(ml, mk))
			if c.Country != "" {
				head.WriteString(`<link rel="alternate" hreflang="` + ml + `-` + c.Country + `" href="` + u + `">`)
			}
			cur := ""
			if mk == p.mk {
				cur = ` aria-current="true"`
			}
			name := c.Name
			if name == "" {
				name = countryName(c.Country)
			}
			if name == "" {
				name = strings.ToUpper(c.ID)
			}
			label := name + " (" + string(c.Currency) + ")"
			nav.WriteString(`<li><a href="` + u + `"` + cur + `>` + html.EscapeString(label) + `</a></li>`)
		}
		nav.WriteString(`</ul>`)
	}
	head.WriteString(`<link rel="alternate" hreflang="x-default" href="` + html.EscapeString(link(i18n.Default, "")) + `">`)
	nav.WriteString(`</nav>`)
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

// countryName names the countries Apexx sells to most, in their own words.
func countryName(code string) string {
	switch strings.ToUpper(code) {
	case "MX":
		return "México"
	case "US":
		return "United States"
	case "CA":
		return "Canada"
	case "ES":
		return "España"
	case "FR":
		return "France"
	case "BR":
		return "Brasil"
	case "PT":
		return "Portugal"
	case "ID":
		return "Indonesia"
	case "JP":
		return "日本"
	case "CO":
		return "Colombia"
	case "AR":
		return "Argentina"
	case "CL":
		return "Chile"
	case "PE":
		return "Perú"
	}
	return code
}

// Product translations: written on the product's own screen. Each text keeps
// the Spanish original it translates; when the original changes the panel
// shows the translation as outdated until someone reviews it.

var productFields = []string{"title", "description", "seoTitle", "seoDescription"}

func productSource(p catalog.Product) map[string]string {
	return map[string]string{"title": p.Title, "description": p.Description, "seoTitle": p.SEOTitle, "seoDescription": p.SEODescription}
}

func (s *Server) getProductTexts(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	p, err := t.Catalog.Get(r.PathValue("id"))
	if err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	var pt i18n.ProductTexts
	if _, err := s.DB.GetDoc(r.Context(), tenant, "product_i18n", p.ID, &pt); err != nil {
		internalError(w, s.Log, err)
		return
	}
	src := productSource(p)
	type field struct {
		Text     string `json:"text"`
		Outdated bool   `json:"outdated"`
	}
	out := map[string]map[string]field{}
	for _, lang := range t.Languages().Enabled {
		out[lang] = map[string]field{}
		for _, f := range productFields {
			x := pt[lang][f]
			out[lang][f] = field{Text: x.Text, Outdated: x.Text != "" && x.Source != src[f]}
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"languages": t.Languages().Enabled, "source": src, "texts": out})
}

func (s *Server) putProductTexts(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	t, ok := s.Proj.Tenant(pr.Tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	p, err := t.Catalog.Get(r.PathValue("id"))
	if err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	var body map[string]map[string]string
	if !decode(w, r, &body) {
		return
	}
	src := productSource(p)
	pt := i18n.ProductTexts{}
	for lang, fields := range body {
		if !i18n.Known(lang) || lang == i18n.Default {
			apierr.BadRequest(w, "Idioma no disponible: "+lang)
			return
		}
		for f, text := range fields {
			if _, known := src[f]; !known {
				continue
			}
			text = strings.TrimSpace(text)
			if len(text) > 20000 {
				apierr.BadRequest(w, "Texto demasiado largo", apierr.FieldError{Field: lang + "." + f, Code: "too_long", Message: "Hasta 20,000 caracteres"})
				return
			}
			if text == "" || src[f] == "" {
				continue
			}
			if pt[lang] == nil {
				pt[lang] = map[string]i18n.Text{}
			}
			pt[lang][f] = i18n.Text{Source: src[f], Text: text}
		}
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "product_i18n", p.ID, store.KeyLanguages, pt); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// NotifyPhrases translates the fixed words of a buyer email into a language
// with the store's own translations first and Apexx's after.
func (s *Server) NotifyPhrases(tenant, lang string) func(string) string {
	t, ok := s.Proj.Tenant(tenant)
	if !ok || !i18n.Known(lang) || lang == i18n.Default {
		return nil
	}
	look := i18n.With(lang, t.Languages().Own[lang])
	return func(x string) string { return i18n.Phrase(x, look) }
}
