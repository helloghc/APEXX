package api

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"sync"
)

// The public marketing site: the home and a landing per service, all sharing
// one layout (with the mega-menu) and one stylesheet, so every page inherits
// the product's identity. Pages are static HTML built from templates.
//
//go:embed marketing/*.tmpl marketing/giro/*.tmpl marketing/resultado/*.tmpl marketing/tamano/*.tmpl marketing/en/*.tmpl marketing/style.css
var marketingFS embed.FS

// marketingV busts the stylesheet cache when the design changes.
const marketingV = "9"

type mktPage struct {
	file   string
	layout string // template file for the chrome; empty = Spanish default
	Title  string
	Desc   string
	V      string
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
	"/giro/moda":       {file: "marketing/giro/moda.tmpl", Title: "Moda y accesorios · Shulada", Desc: "Una tienda de moda rápida y hermosa: variantes de talla y color, colecciones y lookbook, preventas, tabla de medidas, recomendaciones y devoluciones fáciles. Con el tema Lienzo."},
	"/giro/tecnologia": {file: "marketing/giro/tecnologia.tmpl", Title: "Tecnología y electrónica · Shulada", Desc: "Vende tecnología con fichas técnicas claras, comparador, variantes por capacidad y color, pregúntale a la tienda, meses sin intereses y garantías. Con el tema Voltio."},
	"/giro/super":      {file: "marketing/giro/super.tmpl", Title: "Súper y abarrotes · Shulada", Desc: "El súper en línea: «Agregar» en cada producto, recompra en un toque, entrega el mismo día o recolección, precios por volumen y suscripciones. Con el tema Mercadito."},
	"/giro/belleza":    {file: "marketing/giro/belleza.tmpl", Title: "Belleza y bienestar · Shulada", Desc: "Belleza con sets y kits, suscripciones, reseñas, muestras de regalo, rutinas con IA y programa de lealtad. Con el tema Bálsamo."},
	"/giro/hogar":      {file: "marketing/giro/hogar.tmpl", Title: "Hogar y muebles · Shulada", Desc: "Vende muebles y hogar: productos configurables, galerías grandes, envío de voluminosos por zona, pago a meses y fichas con medidas."},
	"/giro/mayoreo":    {file: "marketing/giro/mayoreo.tmpl", Title: "Mayoreo y distribución · Shulada", Desc: "Distribuye con precios por volumen, crédito a 30 días, pedidos recurrentes, punchout (cXML), catálogo por cliente y representantes de venta."},

	// Soluciones por resultado (outcome).
	"/resultado/vender-mas":     {file: "marketing/resultado/vender-mas.tmpl", Title: "Vender más · Shulada", Desc: "Más ventas sin más esfuerzo: búsqueda que entiende, recomendaciones, más canales y mercados, promociones que jalan, recuperación de carritos y suscripciones."},
	"/resultado/mas-conversion": {file: "marketing/resultado/mas-conversion.tmpl", Title: "Más conversión · Shulada", Desc: "Menos fricción, más compras: carga en menos de un segundo, pago en un minuto con OXXO y SPEI, pago como invitado, pregúntale a la tienda, reseñas y envío claro antes de pagar."},
	"/resultado/mas-margen":     {file: "marketing/resultado/mas-margen.tmpl", Title: "Más margen · Shulada", Desc: "Vende más gastando menos en operar: equipos ilimitados, IA incluida, sin servidores, promociones exactas, comisión de marketplace y reportes claros."},
	"/resultado/nuevos-canales": {file: "marketing/resultado/nuevos-canales.tmpl", Title: "Nuevos canales · Shulada", Desc: "Abre canales sin montar otra plataforma: B2B, marketplace, internacional, punto de venta, canales y feeds — todos con un solo inventario."},

	// Soluciones por tamaño (organización).
	"/tamano/emprendes": {file: "marketing/tamano/emprendes.tmpl", Title: "Para quien empieza · Shulada", Desc: "Tu primera tienda viva en minutos: sin servidores ni desarrolladores, 4 temas listos, equipo sin costo, IA incluida y un sandbox para probar."},
	"/tamano/pyme":      {file: "marketing/tamano/pyme.tmpl", Title: "Para la PyME que crece · Shulada", Desc: "La plataforma que crece contigo: B2B y mayoreo, más canales y países, equipo con permisos, lealtad y reportes. Sin migrar otra vez."},
	"/tamano/empresa":   {file: "marketing/tamano/empresa.tmpl", Title: "Para empresas a escala · Shulada", Desc: "Escala sin cortes: 50 000 productos, publicaciones en milisegundos, despliegue sin corte, cada tienda aislada, superadmin y planes, API y auditoría."},

	// Recursos y planes.
	"/por-que":   {file: "marketing/por-que.tmpl", Title: "Por qué Shulada", Desc: "Lo que hace distinta a Shulada: todo es reversible, la velocidad se mide en milisegundos, equipos ilimitados, IA incluida, cada tienda aislada y sin cortes."},
	"/planes":    {file: "marketing/planes.tmpl", Title: "Planes · Shulada", Desc: "Todo incluido en cada plan: equipos ilimitados, IA, sandbox y cientos de funciones, sin cobrar por asiento. El precio se ajusta a tu volumen; pide una cotización a tu medida."},
	"/preguntas": {file: "marketing/preguntas.tmpl", Title: "Preguntas frecuentes · Shulada", Desc: "Respuestas claras: asientos ilimitados, migración desde VTEX, medios de pago OXXO y SPEI, dominio propio, IA incluida, reversible y sin administrar servidores."},
	"/comparar":  {file: "marketing/comparar.tmpl", Title: "Comparar · Shulada", Desc: "Comparación honesta frente a la plataforma tradicional: lo que Shulada incluye y otros cobran aparte."},

	// Confianza, legal y soporte.
	"/seguridad":  {file: "marketing/seguridad.tmpl", Title: "Seguridad y confianza · Shulada", Desc: "Cómo protegemos tu negocio y tus datos: pagos PCI vía Stripe que Shulada nunca ve, cada tienda aislada, todo reversible y una bitácora de auditoría que no se altera."},
	"/estado":     {file: "marketing/estado.tmpl", Title: "Estado y disponibilidad · Shulada", Desc: "El estado de cada componente de Shulada y nuestros compromisos de disponibilidad: despliegue sin corte, respaldo, reversión, monitoreo y SLA por plan."},
	"/contacto":   {file: "marketing/contacto.tmpl", Title: "Contacto · Shulada", Desc: "Habla con ventas, soporte, seguridad o prensa. Te respondemos el mismo día hábil."},
	"/privacidad": {file: "marketing/privacidad.tmpl", Title: "Aviso de Privacidad · Shulada", Desc: "Cómo tratamos y protegemos tus datos personales conforme a la LFPDPPP: qué datos recabamos, para qué, tus derechos ARCO y cómo ejercerlos."},
	"/terminos":   {file: "marketing/terminos.tmpl", Title: "Términos y Condiciones · Shulada", Desc: "Las reglas para usar Shulada como plataforma de comercio, escritas para entenderse."},

	// Crecimiento: migración, partners, ayuda y developers.
	"/migrar":     {file: "marketing/migrar.tmpl", Title: "Migrar desde VTEX o Shopify · Shulada", Desc: "Múdate desde VTEX, Shopify, Woo o un CSV sin perder catálogo, categorías, precios ni SEO. Revisas en un sandbox y publicamos sin corte."},
	"/partners":   {file: "marketing/partners.tmpl", Title: "Partners y agencias · Shulada", Desc: "Para agencias y estudios que montan y operan tiendas: entrega más rápido, mantén menos y crece tu portafolio sobre una base estable."},
	"/ayuda":      {file: "marketing/ayuda.tmpl", Title: "Centro de ayuda · Shulada", Desc: "Guías por tema para sacarle todo el jugo a tu tienda: editor, catálogo, precios, pagos, envíos, B2B, IA y API. Y soporte humano a un clic."},
	"/developers": {file: "marketing/developers.tmpl", Title: "Developers y API · Shulada", Desc: "Integra Shulada con tus sistemas: API REST, webhooks de eventos y punchout (cXML) para compras B2B. Seguro, idempotente y auditable."},

	// Capacidades del motor con página propia.
	"/suscripciones": {file: "marketing/suscripciones.tmpl", Title: "Suscripciones · Shulada", Desc: "Ingresos recurrentes: reposición automática, cobros recurrentes y membresías. El cliente se da de alta una vez y vuelve solo, con control desde su cuenta."},
	"/impuestos":     {file: "marketing/impuestos.tmpl", Title: "Impuestos · Shulada", Desc: "El impuesto correcto calculado en vivo: IVA en México y el impuesto de cada país al vender fuera, con aduanas al comprar y desglose para tu contabilidad."},
	"/lealtad":       {file: "marketing/lealtad.tmpl", Title: "Lealtad y recompensas · Shulada", Desc: "Puntos por compra, tarjetas de regalo y cupones de recompra incluidos, sin apps externas. Haz que la segunda compra sea inevitable."},
	"/pos":           {file: "marketing/pos.tmpl", Title: "Punto de venta y omnicanal · Shulada", Desc: "Vende en mostrador, en línea y en otros canales con un solo inventario, el mismo cliente y los mismos pedidos. Lo que vendes en caja descuenta del mismo stock."},
	"/devoluciones":  {file: "marketing/devoluciones.tmpl", Title: "Devoluciones y posventa · Shulada", Desc: "Cambios y devoluciones de autoservicio con tus reglas: el cliente solicita, tú apruebas, reembolsas (reversible) y reingresas a inventario. Todo en la bitácora."},

	"/nosotros": {file: "marketing/nosotros.tmpl", Title: "Nosotros · Shulada", Desc: "Quiénes somos y en qué creemos: comercio rápido, reversible y todo incluido para México y LATAM. Transparencia por delante: te mostramos todo antes de decidir."},

	// English site (/en). Untranslated /en/* paths 302 to their Spanish version.
	"/en":           {file: "marketing/en/home.tmpl", layout: "marketing/en/layout.tmpl", Title: "Shulada · The commerce platform that doesn't slow you down", Desc: "The complete commerce system for brands and wholesalers: storefront, catalog, pricing, orders, payments, shipping, B2B, marketplace, international and AI. Publish in milliseconds, test in a sandbox and roll back without breaking anything."},
	"/en/planes":    {file: "marketing/en/planes.tmpl", layout: "marketing/en/layout.tmpl", Title: "Pricing · Shulada", Desc: "Everything included in every plan: unlimited seats, AI, sandbox and hundreds of features, with no per-seat fees. Pricing scales with your volume — get a quote tailored to you."},
	"/en/comparar":  {file: "marketing/en/comparar.tmpl", layout: "marketing/en/layout.tmpl", Title: "Compare · Shulada", Desc: "An honest comparison with VTEX, Shopify and Tiendanube: what Shulada includes out of the box versus what others charge extra for."},
	"/en/empezar":   {file: "marketing/en/empezar.tmpl", layout: "marketing/en/layout.tmpl", Title: "Get started · Shulada", Desc: "Tell us about your business and someone from Shulada will reach out to open your store, migrate your catalog and give you a tailored quote. No commitment."},
	"/en/contacto":  {file: "marketing/en/contacto.tmpl", layout: "marketing/en/layout.tmpl", Title: "Contact · Shulada", Desc: "Talk to sales, support, security or press. We reply the same business day."},
	"/en/seguridad": {file: "marketing/en/seguridad.tmpl", layout: "marketing/en/layout.tmpl", Title: "Security & trust · Shulada", Desc: "How we protect your business and your data: PCI payments via Stripe that Shulada never sees, every store isolated, everything reversible and an audit log that can't be altered."},
	"/en/b2b":       {file: "marketing/en/b2b.tmpl", layout: "marketing/en/layout.tmpl", Title: "B2B & wholesale · Shulada", Desc: "Sell to businesses with the same tools: net-30 credit, tiered approvals, per-customer pricing, quotes, punchout (cXML), requisition lists, branches and reps."},
	"/en/pagos":     {file: "marketing/en/pagos.tmpl", layout: "marketing/en/layout.tmpl", Title: "Payments & checkout · Shulada", Desc: "Each store charges with its own Stripe account; Shulada never sees card data. Cards and local methods, reversible and idempotent charges."},
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
		// Always parse the Spanish layout: it defines the shared "ic" (icons) and
		// "mitem" helpers. A page with its own layout (e.g. English) parses it on
		// top, overriding the "layout" definition while reusing those helpers.
		base, err := template.New("layout").Funcs(funcs).ParseFS(marketingFS, "marketing/layout.tmpl")
		if err != nil {
			marketingErr = err
			return
		}
		if p.layout != "" {
			if _, err := base.ParseFS(marketingFS, p.layout); err != nil {
				marketingErr = err
				return
			}
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
		// Untranslated English path falls back to its Spanish equivalent, so the
		// English site never 404s while pages are being translated.
		if es := strings.TrimPrefix(r.URL.Path, "/en"); es != r.URL.Path {
			if es == "" {
				es = "/"
			}
			if _, exists := marketingSite[es]; exists {
				http.Redirect(w, r, es, http.StatusFound)
				return true
			}
		}
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
