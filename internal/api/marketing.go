package api

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"sync"
)

// The public marketing site: the home and a landing per service, all sharing
// one layout (with the mega-menu) and one stylesheet, so every page inherits
// the product's identity. Pages are static HTML built from templates.
//
//go:embed marketing/*.tmpl marketing/style.css
var marketingFS embed.FS

// marketingV busts the stylesheet cache when the design changes.
const marketingV = "1"

type mktPage struct {
	file  string
	Title string
	Desc  string
	V     string
}

// marketingSite lists every public page by its URL path.
var marketingSite = map[string]*mktPage{
	"/": {file: "marketing/home.tmpl",
		Title: "Shulada · La plataforma de comercio que no te frena",
		Desc:  "El sistema completo de comercio para marcas y mayoristas: tienda, catálogo, precios, pedidos, pagos, envíos, B2B, marketplace, internacional, IA y analítica. Publica en milisegundos, prueba en un sandbox y vuelve atrás sin romper nada."},
	"/para-quien-compra": {file: "marketing/comprador.tmpl",
		Title: "Para quien compra · Shulada",
		Desc:  "La experiencia del comprador en Shulada: búsqueda que entiende, pregúntale a la tienda, reseñas, favoritos, pago en un minuto con OXXO y SPEI, rastreo, devoluciones, suscripciones y lealtad."},
	"/b2b": {file: "marketing/b2b.tmpl",
		Title: "B2B y mayoreo · Shulada",
		Desc:  "Vende a empresas con las mismas herramientas: crédito a 30 días, aprobaciones por nivel, precios por cliente, cotizaciones, punchout (cXML), listas de requisición, sucursales y representantes."},
	"/ia": {file: "marketing/ia.tmpl",
		Title: "Inteligencia artificial · Shulada",
		Desc:  "IA incluida en Shulada: búsqueda por significado, «pregúntale a la tienda» con respuestas citadas, recomendaciones y copilotos. Sin configurar nada, con presupuesto por tienda."},
	"/pagos": {file: "marketing/pagos.tmpl",
		Title: "Pagos y checkout · Shulada",
		Desc:  "Cobra como en México: tarjeta, OXXO y SPEI con meses sin intereses. Cada tienda cobra con su propia cuenta de Stripe; Shulada nunca ve los datos de las tarjetas. Cobros reversibles e idempotentes."},
}

var (
	marketingOnce  sync.Once
	marketingTmpls map[string]*template.Template
	marketingErr   error
)

// tmplDict builds a map from alternating key/value pairs, for passing several
// values into a sub-template ({{template "x" dict "a" 1 "b" 2}}).
func tmplDict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict: needs an even number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			return nil, errors.New("dict: keys must be strings")
		}
		m[k] = pairs[i+1]
	}
	return m, nil
}

func buildMarketing() {
	funcs := template.FuncMap{"dict": tmplDict}
	marketingTmpls = make(map[string]*template.Template, len(marketingSite))
	for path, p := range marketingSite {
		base, err := template.New("layout").Funcs(funcs).ParseFS(marketingFS, "marketing/layout.tmpl")
		if err != nil {
			marketingErr = err
			return
		}
		if _, err := base.ParseFS(marketingFS, p.file); err != nil {
			marketingErr = err
			return
		}
		marketingTmpls[path] = base
	}
}

// serveMarketing renders a public marketing page (or the stylesheet). It
// returns false if the path is not part of the marketing site, so the caller
// can fall through to a 404.
func (s *Server) serveMarketing(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/marketing.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		b, _ := marketingFS.ReadFile("marketing/style.css")
		_, _ = w.Write(b)
		return true
	}
	page, ok := marketingSite[r.URL.Path]
	if !ok {
		return false
	}
	marketingOnce.Do(buildMarketing)
	if marketingErr != nil {
		internalError(w, s.Log, marketingErr)
		return true
	}
	var buf bytes.Buffer
	data := struct {
		Title, Desc, V string
	}{page.Title, page.Desc, marketingV}
	if err := marketingTmpls[r.URL.Path].ExecuteTemplate(&buf, "layout", data); err != nil {
		internalError(w, s.Log, err)
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = buf.WriteTo(w)
	return true
}
