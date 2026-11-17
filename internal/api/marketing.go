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
//go:embed marketing/*.tmpl marketing/giro/*.tmpl marketing/resultado/*.tmpl marketing/tamano/*.tmpl marketing/style.css
var marketingFS embed.FS

// marketingV busts the stylesheet cache when the design changes.
const marketingV = "5"

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
	"/tienda": {file: "marketing/tienda.tmpl",
		Title: "Tienda y editor · Shulada",
		Desc:  "Arma tu tienda por secciones, elige uno de 4 temas originales y publica en milisegundos. Deshacer y versiones, dominio propio con HTTPS, blog, páginas y menús."},
	"/catalogo": {file: "marketing/catalogo.tmpl",
		Title: "Catálogo · Shulada",
		Desc:  "Un catálogo que escala a 50 000 productos: variantes, paquetes, digitales, preventas, campos personalizados, fichas técnicas, medios con edición e importación de VTEX y CSV."},
	"/precios": {file: "marketing/precios.tmpl",
		Title: "Precios y promociones · Shulada",
		Desc:  "Listas de precios por cliente, precios por volumen, cupones, campañas, 3x2, escalones, mínimos, tarjetas de regalo y programa de lealtad. Calculado en vivo, sin errores."},
	"/envios": {file: "marketing/envios.tmpl",
		Title: "Envíos · Shulada",
		Desc:  "Envíos cotizados en vivo con fecha, compra de guías desde el pedido, recolección en tienda, zonas y tarifas, días festivos, seguimiento, entrega digital y aduanas por país."},
	"/sandbox": {file: "marketing/sandbox.tmpl",
		Title: "Sandbox · Shulada",
		Desc:  "Duplica tu tienda en una copia privada, prueba precios, cupones y productos, publica solo lo que apruebes y revierte en un clic. Publicación programada, contraseña opcional y existencias aisladas."},
	"/marketing": {file: "marketing/marketing.tmpl",
		Title: "Marketing y analítica · Shulada",
		Desc:  "Píxeles de Meta, TikTok, Pinterest y Google Ads, Tag Manager, campañas de correo, recuperación de carritos, automatizaciones, reportes a la medida y una bitácora verificable."},
	"/marketplace": {file: "marketing/marketplace.tmpl",
		Title: "Marketplace · Shulada",
		Desc:  "Lanza tu marketplace: vendedores con su portal, sus productos y sus pagos con Stripe Connect. Define tu comisión y la plataforma reparte sola, con seguimiento de cada pago."},
	"/internacional": {file: "marketing/internacional.tmpl",
		Title: "Internacional y multicanal · Shulada",
		Desc:  "Vende en más países: mercados por país con su moneda e impuestos, la tienda en 6 idiomas con dirección propia para Google, canales de venta con inventario compartido y aduanas al comprar."},
	"/empezar": {file: "marketing/empezar.tmpl",
		Title: "Empezar · Shulada",
		Desc:  "Cuéntanos de tu negocio y una persona de Shulada te contacta para abrir tu tienda, migrar tu catálogo y darte una cotización a tu medida. Sin compromiso."},

	// Soluciones por giro (vertical).
	"/giro/moda":        {file: "marketing/giro/moda.tmpl", Title: "Moda y accesorios · Shulada", Desc: "Una tienda de moda rápida y hermosa: variantes de talla y color, colecciones y lookbook, preventas, tabla de medidas, recomendaciones y devoluciones fáciles. Con el tema Lienzo."},
	"/giro/tecnologia":  {file: "marketing/giro/tecnologia.tmpl", Title: "Tecnología y electrónica · Shulada", Desc: "Vende tecnología con fichas técnicas claras, comparador, variantes por capacidad y color, pregúntale a la tienda, meses sin intereses y garantías. Con el tema Voltio."},
	"/giro/super":       {file: "marketing/giro/super.tmpl", Title: "Súper y abarrotes · Shulada", Desc: "El súper en línea: «Agregar» en cada producto, recompra en un toque, entrega el mismo día o recolección, precios por volumen y suscripciones. Con el tema Mercadito."},
	"/giro/belleza":     {file: "marketing/giro/belleza.tmpl", Title: "Belleza y bienestar · Shulada", Desc: "Belleza con sets y kits, suscripciones, reseñas, muestras de regalo, rutinas con IA y programa de lealtad. Con el tema Bálsamo."},
	"/giro/hogar":       {file: "marketing/giro/hogar.tmpl", Title: "Hogar y muebles · Shulada", Desc: "Vende muebles y hogar: productos configurables, galerías grandes, envío de voluminosos por zona, pago a meses y fichas con medidas."},
	"/giro/mayoreo":     {file: "marketing/giro/mayoreo.tmpl", Title: "Mayoreo y distribución · Shulada", Desc: "Distribuye con precios por volumen, crédito a 30 días, pedidos recurrentes, punchout (cXML), catálogo por cliente y representantes de venta."},

	// Soluciones por resultado (outcome).
	"/resultado/vender-mas":      {file: "marketing/resultado/vender-mas.tmpl", Title: "Vender más · Shulada", Desc: "Más ventas sin más esfuerzo: búsqueda que entiende, recomendaciones, más canales y mercados, promociones que jalan, recuperación de carritos y suscripciones."},
	"/resultado/mas-conversion":  {file: "marketing/resultado/mas-conversion.tmpl", Title: "Más conversión · Shulada", Desc: "Menos fricción, más compras: carga en menos de un segundo, pago en un minuto con OXXO y SPEI, pago como invitado, pregúntale a la tienda, reseñas y envío claro antes de pagar."},
	"/resultado/mas-margen":      {file: "marketing/resultado/mas-margen.tmpl", Title: "Más margen · Shulada", Desc: "Vende más gastando menos en operar: equipos ilimitados, IA incluida, sin servidores, promociones exactas, comisión de marketplace y reportes claros."},
	"/resultado/nuevos-canales":  {file: "marketing/resultado/nuevos-canales.tmpl", Title: "Nuevos canales · Shulada", Desc: "Abre canales sin montar otra plataforma: B2B, marketplace, internacional, punto de venta, canales y feeds — todos con un solo inventario."},

	// Soluciones por tamaño (organización).
	"/tamano/emprendes": {file: "marketing/tamano/emprendes.tmpl", Title: "Para quien empieza · Shulada", Desc: "Tu primera tienda viva en minutos: sin servidores ni desarrolladores, 4 temas listos, equipo sin costo, IA incluida y un sandbox para probar."},
	"/tamano/pyme":      {file: "marketing/tamano/pyme.tmpl", Title: "Para la PyME que crece · Shulada", Desc: "La plataforma que crece contigo: B2B y mayoreo, más canales y países, equipo con permisos, lealtad y reportes. Sin migrar otra vez."},
	"/tamano/empresa":   {file: "marketing/tamano/empresa.tmpl", Title: "Para empresas a escala · Shulada", Desc: "Escala sin cortes: 50 000 productos, publicaciones en milisegundos, despliegue sin corte, cada tienda aislada, superadmin y planes, API y auditoría."},
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
