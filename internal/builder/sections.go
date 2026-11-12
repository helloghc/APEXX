package builder

import (
	"html/template"
	"strings"
)

func intp(i int) *int           { return &i }
func floatp(f float64) *float64 { return &f }
func boolp(b bool) *bool        { return &b }

// Link is a label/URL pair parsed from a "Etiqueta | /url" line.
type Link struct{ Label, URL string }

// sectionFuncs are available to every section template.
var sectionFuncs = template.FuncMap{
	// links parses one "Etiqueta | url" per line.
	"links": func(v any) []Link {
		s, _ := v.(string)
		var out []Link
		for _, line := range strings.Split(s, "\n") {
			label, url, ok := strings.Cut(line, "|")
			label, url = strings.TrimSpace(label), strings.TrimSpace(url)
			if label == "" {
				continue
			}
			if !ok || url == "" {
				url = "#"
			}
			out = append(out, Link{label, url})
		}
		return out
	},
	"tone":      func(i int) int { return i%6 + 1 },
	"toneOf":    func(v any) int { s, _ := v.(string); return ToneOf(s) },
	"hasPrefix": func(v any, p string) bool { s, _ := v.(string); return strings.HasPrefix(s, p) },
	"initial": func(v any) string {
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			return ""
		}
		return strings.ToUpper(string([]rune(s)[0]))
	},
	"str":  func(v any) string { s, _ := v.(string); return s },
	"dict": func(h1, text any) map[string]any { return map[string]any{"H1": h1, "Text": text} },
}

// partials are shared by every section template.
const partials = `{{define "card"}}<li class="pcard"><a class="pcard-link" href="{{.URL}}">{{if .Image}}{{.Image}}{{else}}<div class="pcard-media ph ph-{{.Tone}}" aria-hidden="true"><span>{{.Initial}}</span></div>{{end}}<div class="pcard-body">{{with .Brand}}<p class="pcard-brand">{{.}}</p>{{end}}<h3 class="pcard-title">{{.Title}}</h3><p class="pcard-price">{{if .CompareAt}}<span class="sale">{{.Price}}</span> <s>{{.CompareAt}}</s>{{else}}{{.Price}}{{end}}</p></div></a>{{if .SoldOut}}<p class="pcard-badge">Agotado</p>{{else if .CompareAt}}<p class="pcard-badge is-sale">Oferta</p>{{end}}{{if and .SKU (not .SoldOut)}}<button class="pcard-add" type="button" data-quick-add="{{.SKU}}" data-title="{{.Title}}">Agregar</button>{{end}}</li>{{end}}` +
	`{{define "heading"}}{{if .H1}}<h1 class="display">{{.Text}}</h1>{{else}}<h2 class="display">{{.Text}}</h2>{{end}}{{end}}`

// DefaultSections is the section catalog available to every theme.
func DefaultSections() []SectionDefinition {
	str := func(title string, max int) Schema { return Schema{Type: "string", Title: title, MaxLength: intp(max)} }
	area := func(title string, max int) Schema {
		return Schema{Type: "string", Title: title, MaxLength: intp(max), Widget: "textarea"}
	}
	obj := func(req []string, props map[string]Schema) Schema {
		return Schema{Type: "object", Required: req, AdditionalProperties: boolp(false), Properties: props, Order: orderFields(props)}
	}
	return []SectionDefinition{
		{
			Type: "announcement", Version: "1.0.0", Name: "Barra de anuncios", Category: "encabezado",
			Description: "Mensaje corto arriba de todo: envío gratis, promociones, horarios.", Hydration: "none",
			Budget:         Budget{HTMLBytes: 400, CSSBytes: 300},
			SettingsSchema: obj([]string{"text"}, map[string]Schema{"text": str("Mensaje", 120), "linkLabel": str("Texto del enlace", 30), "linkUrl": {Type: "string", Title: "Enlace", Widget: "link-picker"}}),
			Template:       `<div class="sx-announce"><p>{{.S.text}}{{with .S.linkLabel}} <a href="{{$.S.linkUrl}}">{{.}}</a>{{end}}</p></div>`,
		},
		{
			Type: "header", Version: "1.0.0", Name: "Encabezado", Category: "encabezado",
			Description: "Logo, menú, búsqueda y carrito.", Hydration: "none",
			Budget: Budget{HTMLBytes: 1500, CSSBytes: 1500},
			SettingsSchema: obj([]string{"logoText"}, map[string]Schema{
				"logoText":   str("Nombre de la tienda", 40),
				"menu":       area("Menú (una línea por enlace: Etiqueta | /url)", 600),
				"showSearch": {Type: "boolean", Title: "Mostrar búsqueda", Default: true},
			}),
			Template: `<header class="sx-header"><div class="hdr-inner">` +
				`<details class="hdr-menu-m"><summary aria-label="Abrir menú"><span></span><span></span><span></span></summary><nav aria-label="Menú móvil"><ul data-menu="header">{{range links .S.menu}}<li><a href="{{.URL}}">{{.Label}}</a></li>{{end}}</ul></nav></details>` +
				`<a class="hdr-logo" href="index">{{.S.logoText}}</a>` +
				`<nav class="hdr-nav" aria-label="Principal"><ul data-menu="header">{{range links .S.menu}}<li><a href="{{.URL}}">{{.Label}}</a></li>{{end}}</ul></nav>` +
				`<div class="hdr-tools">{{if .S.showSearch}}<form class="hdr-search" action="c" role="search"><label for="hq-{{.ID}}" class="sr">Buscar productos</label><input id="hq-{{.ID}}" name="q" type="search" placeholder="Buscar"><button type="submit" aria-label="Buscar"><svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><circle cx="11" cy="11" r="7" fill="none" stroke="currentColor" stroke-width="2"/><path d="m20 20-4-4" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></button></form>{{end}}` +
				`<a class="hdr-cart" href="cuenta"><svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true"><circle cx="12" cy="8" r="4" fill="none" stroke="currentColor" stroke-width="2"/><path d="M4 21a8 8 0 0 1 16 0" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg><span class="sr">Mi cuenta</span></a>` +
				`<a class="hdr-cart" href="cart"><svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true"><path d="M5 7h14l-1.5 12h-11z" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M9 7a3 3 0 0 1 6 0" fill="none" stroke="currentColor" stroke-width="2"/></svg><span class="sr">Carrito</span><span class="hdr-count" data-cart-count aria-live="polite">0</span></a></div>` +
				`</div></header>`,
		},
		{
			Type: "hero", Version: "2.0.0", Name: "Banner principal", Category: "heroes",
			Description: "Imagen o arte de ancho completo, dividido o solo texto, con título y botones.",
			EnabledOn:   []string{"home", "collection", "page"}, Hydration: "none",
			Budget: Budget{HTMLBytes: 3000, CSSBytes: 2500, Requests: 1, AboveFoldImages: 1},
			SettingsSchema: obj([]string{"heading"}, map[string]Schema{
				"eyebrow":    str("Antetítulo", 40),
				"heading":    {Type: "string", Title: "Título", MaxLength: intp(80), Widget: "text"},
				"subheading": area("Texto", 200),
				"image":      {Type: "string", Title: "Imagen (vacío = arte del tema)", Widget: "media"},
				"alt":        {Type: "string", Title: "Texto alternativo", MinLength: intp(5), MaxLength: intp(140)},
				"monogram":   str("Monograma del arte", 3),
				"ctaLabel":   str("Texto del botón", 30),
				"ctaUrl":     {Type: "string", Title: "Enlace del botón", Widget: "link-picker"},
				"cta2Label":  str("Segundo botón", 30),
				"cta2Url":    {Type: "string", Title: "Enlace del segundo botón", Widget: "link-picker"},
				"layout":     {Type: "string", Title: "Diseño", Enum: []any{"full", "split", "text"}, Default: "full"},
				"height":     {Type: "string", Title: "Altura", Enum: []any{"small", "medium", "large"}, Default: "medium"},
				"overlay":    {Type: "number", Title: "Oscurecer imagen", Minimum: floatp(0), Maximum: floatp(0.8), Default: 0.2, Widget: "slider"},
			}),
			Template: `<div class="sx-hero hero-{{.S.layout}} hero-h-{{.S.height}}" style="--overlay:{{.S.overlay}}">` +
				`{{if ne (str .S.layout) "text"}}<div class="hero-media">{{if .S.image}}<img src="{{.S.image}}" alt="{{.S.alt}}" width="1600" height="900" fetchpriority="high">{{else}}<div class="ph ph-hero ph-{{toneOf .S.heading}}" aria-hidden="true"><span>{{.S.monogram}}</span></div>{{end}}</div>{{end}}` +
				`<div class="hero-copy">{{with .S.eyebrow}}<p class="eyebrow">{{.}}</p>{{end}}{{template "heading" (dict .H1 .S.heading)}}{{with .S.subheading}}<p class="lede">{{.}}</p>{{end}}` +
				`<p class="hero-ctas">{{with .S.ctaLabel}}<a class="btn" href="{{$.S.ctaUrl}}">{{.}}</a>{{end}}{{with .S.cta2Label}}<a class="btn btn-ghost" href="{{$.S.cta2Url}}">{{.}}</a>{{end}}</p></div></div>`,
		},
		{
			Type: "rich-text", Version: "1.1.0", Name: "Texto", Category: "contenido",
			Description: "Título y párrafo, alineado a la izquierda o al centro.", Hydration: "none",
			Budget: Budget{HTMLBytes: 1500, CSSBytes: 500},
			SettingsSchema: obj([]string{"heading"}, map[string]Schema{
				"eyebrow": str("Antetítulo", 40), "heading": str("Título", 120), "body": area("Texto", 2000),
				"align": {Type: "string", Title: "Alineación", Enum: []any{"left", "center"}, Default: "center"},
			}),
			Template: `<div class="sx-rich align-{{.S.align}}">{{with .S.eyebrow}}<p class="eyebrow">{{.}}</p>{{end}}{{template "heading" (dict .H1 .S.heading)}}{{with .S.body}}<p class="lede">{{.}}</p>{{end}}</div>`,
		},
		{
			// Content from a store app (reviews, social feed, booking) in an isolated frame:
			// it cannot read the page, the cart or the shopper's data.
			Type: "app-embed", Version: "1.0.0", Name: "Contenido de una app", Category: "contenido",
			Description: "Muestra una vista de una app (reseñas, redes, reservaciones) en un marco aislado.", Hydration: "none",
			Budget: Budget{HTMLBytes: 700, CSSBytes: 200},
			SettingsSchema: obj([]string{"url", "title"}, map[string]Schema{
				"title":  str("Título (para lectores de pantalla)", 80),
				"url":    {Type: "string", Title: "Dirección https de la vista de la app", MaxLength: intp(300)},
				"height": {Type: "integer", Title: "Altura en píxeles", Minimum: floatp(120), Maximum: floatp(1600), Default: 400.0},
			}),
			Template: `<div class="sx-embed">{{if hasPrefix .S.url "https://"}}<iframe src="{{.S.url}}" title="{{.S.title}}" loading="lazy" sandbox="allow-scripts allow-popups allow-forms" referrerpolicy="no-referrer" style="width:100%;height:{{.S.height}}px;border:0"></iframe>{{end}}</div>`,
		},
		{
			Type: "product-grid", Version: "1.0.0", Name: "Grilla de productos", Category: "productos",
			Description: "Productos del catálogo filtrados por marca o categoría.", Hydration: "none",
			Budget: Budget{HTMLBytes: 8000, CSSBytes: 1500, Requests: 0},
			SettingsSchema: obj([]string{"heading"}, map[string]Schema{
				"heading": str("Título", 80), "linkLabel": str("Texto del enlace", 30),
				"brand": str("Solo esta marca", 60), "category": str("Solo esta categoría", 60),
				"limit":   {Type: "integer", Title: "Cantidad", Minimum: floatp(2), Maximum: floatp(24), Default: 8.0},
				"columns": {Type: "integer", Title: "Columnas en escritorio", Minimum: floatp(2), Maximum: floatp(6), Default: 4.0},
			}),
			Template: `<div class="sx-products"><div class="sx-head"><h2 class="display">{{.S.heading}}</h2>{{with .S.linkLabel}}<a class="sx-more" href="c">{{.}}</a>{{end}}</div><ul class="pgrid cols-{{.S.columns}}">{{range .Products}}{{template "card" .}}{{end}}</ul></div>`,
		},
		{
			Type: "product-carousel", Version: "2.2.0", Name: "Carrusel de productos", Category: "productos",
			Description: "Productos en una fila deslizable (sin JavaScript).", Hydration: "none",
			Budget: Budget{HTMLBytes: 6000, CSSBytes: 1500, Requests: 0},
			SettingsSchema: obj([]string{"heading"}, map[string]Schema{
				"heading": str("Título", 80), "collection": {Type: "string", Title: "Colección", Widget: "collection-picker"},
				"brand": str("Solo esta marca", 60), "category": str("Solo esta categoría", 60),
				"limit": {Type: "integer", Title: "Cantidad", Minimum: floatp(1), Maximum: floatp(24), Default: 8.0},
			}),
			Template: `<div class="sx-products"><div class="sx-head"><h2 class="display">{{.S.heading}}</h2><a class="sx-more" href="c">Ver todo</a></div><ul class="pcarousel" tabindex="0" aria-label="{{.S.heading}}">{{range .Products}}{{template "card" .}}{{end}}</ul></div>`,
		},
		{
			Type: "collection-tiles", Version: "1.0.0", Name: "Colecciones", Category: "colecciones",
			Description: "Mosaico de categorías o colecciones con arte del tema.", Hydration: "none",
			Blocks: BlockRules{Allowed: []string{"tile"}, Min: 2, Max: 8},
			Budget: Budget{HTMLBytes: 2000, CSSBytes: 1200},
			SettingsSchema: obj(nil, map[string]Schema{"heading": str("Título", 80),
				"style": {Type: "string", Title: "Estilo", Enum: []any{"tall", "square", "pill"}, Default: "tall"}}),
			Template: `<div class="sx-tiles tiles-{{.S.style}}">{{with .S.heading}}<div class="sx-head"><h2 class="display">{{.}}</h2></div>{{end}}<ul class="tiles">{{range $i, $b := .Blocks}}<li class="tile"><a href="{{index .Settings "link"}}"><div class="ph ph-{{tone $i}}" aria-hidden="true"><span>{{initial (index .Settings "title")}}</span></div><span class="tile-copy"><strong>{{index .Settings "title"}}</strong>{{with index .Settings "caption"}}<small>{{.}}</small>{{end}}</span></a></li>{{end}}</ul></div>`,
		},
		{
			Type: "image-text", Version: "1.0.0", Name: "Imagen con texto", Category: "contenido",
			Description: "Arte o imagen junto a una historia, con botón.", Hydration: "none",
			Budget: Budget{HTMLBytes: 1500, CSSBytes: 900},
			SettingsSchema: obj([]string{"heading"}, map[string]Schema{
				"eyebrow": str("Antetítulo", 40), "heading": str("Título", 100), "body": area("Texto", 800),
				"image":    {Type: "string", Title: "Imagen (vacío = arte del tema)", Widget: "media"},
				"alt":      {Type: "string", Title: "Texto alternativo", MinLength: intp(5), MaxLength: intp(140)},
				"ctaLabel": str("Texto del botón", 30), "ctaUrl": {Type: "string", Title: "Enlace", Widget: "link-picker"},
				"reverse": {Type: "boolean", Title: "Imagen a la derecha", Default: false},
			}),
			Template: `<div class="sx-imgtext{{if .S.reverse}} is-reverse{{end}}"><div class="it-media">{{if .S.image}}<img src="{{.S.image}}" alt="{{.S.alt}}" width="900" height="1100" loading="lazy">{{else}}<div class="ph ph-{{toneOf .S.heading}}" aria-hidden="true"><span>{{initial .S.heading}}</span></div>{{end}}</div><div class="it-copy">{{with .S.eyebrow}}<p class="eyebrow">{{.}}</p>{{end}}<h2 class="display">{{.S.heading}}</h2>{{with .S.body}}<p class="lede">{{.}}</p>{{end}}{{with .S.ctaLabel}}<a class="btn" href="{{$.S.ctaUrl}}">{{.}}</a>{{end}}</div></div>`,
		},
		{
			Type: "benefits", Version: "1.1.0", Name: "Íconos de beneficios", Category: "contenido",
			Description: "De 2 a 4 beneficios con título y texto.", Hydration: "none",
			Blocks:         BlockRules{Allowed: []string{"benefit"}, Min: 2, Max: 4},
			Budget:         Budget{HTMLBytes: 1500, CSSBytes: 800},
			SettingsSchema: obj(nil, map[string]Schema{"heading": str("Título", 80)}),
			Template:       `<div class="sx-benefits">{{with .S.heading}}<h2 class="display sr-soft">{{.}}</h2>{{end}}<ul>{{range $i, $b := .Blocks}}<li><span class="bf-icon bf-{{tone $i}}" aria-hidden="true"></span><strong>{{index .Settings "title"}}</strong><span>{{index .Settings "text"}}</span></li>{{end}}</ul></div>`,
		},
		{
			Type: "testimonials", Version: "1.0.0", Name: "Testimonios", Category: "contenido",
			Description: "Opiniones de clientes.", Hydration: "none",
			Blocks:         BlockRules{Allowed: []string{"quote"}, Min: 1, Max: 6},
			Budget:         Budget{HTMLBytes: 1800, CSSBytes: 700},
			SettingsSchema: obj(nil, map[string]Schema{"heading": str("Título", 80)}),
			Template:       `<div class="sx-quotes">{{with .S.heading}}<div class="sx-head"><h2 class="display">{{.}}</h2></div>{{end}}<ul>{{range .Blocks}}<li><figure><blockquote>“{{index .Settings "quote"}}”</blockquote><figcaption><strong>{{index .Settings "author"}}</strong>{{with index .Settings "detail"}} · {{.}}{{end}}</figcaption></figure></li>{{end}}</ul></div>`,
		},
		{
			Type: "faq", Version: "1.0.0", Name: "Preguntas frecuentes", Category: "contenido",
			Description: "Preguntas y respuestas desplegables, accesibles sin JavaScript.", Hydration: "none",
			Blocks:         BlockRules{Allowed: []string{"question"}, Min: 1, Max: 12},
			Budget:         Budget{HTMLBytes: 2000, CSSBytes: 500},
			SettingsSchema: obj(nil, map[string]Schema{"heading": str("Título", 80)}),
			Template:       `<div class="sx-faq">{{with .S.heading}}<h2 class="display">{{.}}</h2>{{end}}{{range .Blocks}}<details><summary>{{index .Settings "q"}}</summary><p>{{index .Settings "a"}}</p></details>{{end}}</div>`,
		},
		{
			Type: "logo-strip", Version: "1.0.0", Name: "Marcas", Category: "contenido",
			Description: "Fila de marcas que vendes.", Hydration: "none",
			Blocks:         BlockRules{Allowed: []string{"logo"}, Min: 2, Max: 10},
			Budget:         Budget{HTMLBytes: 800, CSSBytes: 400},
			SettingsSchema: obj(nil, map[string]Schema{"heading": str("Título", 80)}),
			Template:       `<div class="sx-logos">{{with .S.heading}}<p class="eyebrow">{{.}}</p>{{end}}<ul>{{range .Blocks}}<li><a href="c?q={{index .Settings "name"}}">{{index .Settings "name"}}</a></li>{{end}}</ul></div>`,
		},
		{
			Type: "newsletter", Version: "1.0.0", Name: "Boletín", Category: "conversion",
			Description: "Suscripción por correo (el envío a una herramienta de correo llega con los conectores).", Hydration: "interaction",
			Budget:         Budget{HTMLBytes: 900, JSBytes: 0, CSSBytes: 600},
			SettingsSchema: obj([]string{"heading"}, map[string]Schema{"heading": str("Título", 80), "body": area("Texto", 240), "buttonLabel": {Type: "string", Title: "Botón", MaxLength: intp(24), Default: "Suscribirme"}}),
			Template:       `<div class="sx-news"><h2 class="display">{{.S.heading}}</h2>{{with .S.body}}<p class="lede">{{.}}</p>{{end}}<form class="news-form" data-newsletter novalidate><label for="nl-{{.ID}}" class="sr">Correo</label><input id="nl-{{.ID}}" type="email" name="email" placeholder="tu@correo.com" autocomplete="email" required><button class="btn" type="submit">{{.S.buttonLabel}}</button></form><p class="news-ok" data-newsletter-ok hidden>¡Listo! Te escribiremos pronto.</p></div>`,
		},
		{
			Type: "footer", Version: "1.0.0", Name: "Pie de página", Category: "pie",
			Description: "Columnas de enlaces, redes, medios de pago y datos legales.", Hydration: "none",
			Blocks: BlockRules{Allowed: []string{"column"}, Min: 0, Max: 4},
			Budget: Budget{HTMLBytes: 1500, CSSBytes: 800},
			SettingsSchema: obj(nil, map[string]Schema{
				"storeName": str("Nombre de la tienda", 40),
				"about":     area("Sobre la tienda", 300), "social": area("Redes (Etiqueta | url)", 400),
				"legal": str("Texto legal", 160),
			}),
			Template: `<footer class="sx-footer"><div class="ft-grid"><div class="ft-about"><p class="ft-logo">{{.S.storeName}}</p>{{with .S.about}}<p>{{.}}</p>{{end}}<ul class="ft-social">{{range links .S.social}}<li><a href="{{.URL}}" rel="noopener">{{.Label}}</a></li>{{end}}</ul></div>{{range .Blocks}}<nav class="ft-col" aria-label="{{index .Settings "title"}}"><p class="ft-title">{{index .Settings "title"}}</p><ul>{{range links (index .Settings "links")}}<li><a href="{{.URL}}">{{.Label}}</a></li>{{end}}</ul></nav>{{end}}<template data-menu="footer"></template></div><div class="ft-bottom"><p>{{.S.legal}}</p><p>Pagos seguros con Stripe · Visa · Mastercard · American Express</p></div></footer>`,
		},
		{
			Type: "video-wall", Version: "1.0.0", Name: "Muro de videos", Category: "contenido",
			Description: "Ejemplo de sección pesada: el builder la frena si rompe el presupuesto.", Hydration: "idle",
			Budget:         Budget{HTMLBytes: 4000, JSBytes: 90000, CSSBytes: 3000, Requests: 6, AboveFoldImages: 2},
			SettingsSchema: obj(nil, map[string]Schema{"heading": str("Título", 80)}),
			Template:       `<h2>{{.S.heading}}</h2>`,
		},
	}
}
