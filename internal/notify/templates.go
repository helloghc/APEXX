package notify

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

// Template keys.
const (
	TplOrderConfirmed      = "order_confirmed"
	TplOrderShipped        = "order_shipped"
	TplOrderDelivered      = "order_delivered"
	TplRefundIssued        = "refund_issued"
	TplReturnReceived      = "return_received"
	TplAccessCode          = "access_code"
	TplInvoiceReady        = "invoice_available"
	TplAbandonedCart       = "abandoned_cart"
	TplPaymentLink         = "payment_link"
	TplGiftCard            = "gift_card"
	TplPurchaseApproval    = "purchase_approval"
	TplQuoteOffered        = "quote_offered"
	TplCompanyDue          = "company_due"
	TplCompanyAutopayFail  = "company_autopay_failed"
	TplTeamInvite          = "team_invite"
	TplSubscriptionCreated = "subscription_created"
	TplSubscriptionFailed  = "subscription_failed"
)

// Blocks are the editable parts of a template. Body paragraphs are separated
// by a blank line. Every block accepts {{variables}}.
type Blocks struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
	CTA     string `json:"cta"`
	Footer  string `json:"footer"`
}

// Variable documents one placeholder for the editor.
type Variable struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Sample string `json:"sample"`
}

// Definition is a built-in template with its default copy.
type Definition struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Subject     string     `json:"subject"`
	Blocks      Blocks     `json:"blocks"`
	Variables   []Variable `json:"variables"`
	ShowItems   bool       `json:"showItems"`
	Optional    bool       `json:"optional"` // marketing-like: can be turned off
}

// Line is one row of the items table.
type Line struct {
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
	Total string `json:"total"`
}

// Row is one row of the totals table.
type Row struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Bold  bool   `json:"bold,omitempty"`
}

// Data is what a message carries to fill a template.
type Data struct {
	Vars      map[string]string `json:"vars"`
	Lines     []Line            `json:"lines,omitempty"`
	Totals    []Row             `json:"totals,omitempty"`
	ActionURL string            `json:"actionUrl,omitempty"`
}

// Brand is the look of the emails of a store.
type Brand struct {
	StoreName string `json:"storeName"`
	Color     string `json:"color"`
	LogoURL   string `json:"logoUrl"`
	Footer    string `json:"footer"`
}

var common = []Variable{
	{"tienda.nombre", "Nombre de la tienda", "Tienda Norte"},
	{"cliente.nombre", "Nombre del cliente", "Ana Torres"},
}

func orderVars(extra ...Variable) []Variable {
	v := append([]Variable{}, common...)
	v = append(v, Variable{"pedido.numero", "Número de pedido", "1042"}, Variable{"pedido.total", "Total del pedido", "$1,598.00 MXN"})
	return append(v, extra...)
}

// Definitions lists the built-in templates in display order.
var Definitions = []Definition{
	{Key: TplOrderConfirmed, Name: "Pedido confirmado", Description: "Se envía cuando el pago se confirma.",
		Subject: "Confirmamos tu pedido #{{pedido.numero}}",
		Blocks: Blocks{Heading: "¡Gracias por tu compra, {{cliente.nombre}}!",
			Body: "Recibimos tu pago y ya estamos preparando tu pedido #{{pedido.numero}}.\n\nTe avisaremos por correo cuando salga de nuestro almacén.",
			CTA:  "Ver mi pedido", Footer: "Si tienes dudas, responde a este correo."},
		Variables: orderVars(), ShowItems: true},
	{Key: TplOrderShipped, Name: "Pedido enviado con guía", Description: "Se envía al registrar un envío, con paquetería y número de guía.",
		Subject: "Tu pedido #{{pedido.numero}} va en camino",
		Blocks: Blocks{Heading: "Tu pedido va en camino",
			Body: "Hola {{cliente.nombre}}, enviamos tu pedido #{{pedido.numero}} con {{guia.paqueteria}}.\n\nNúmero de guía: {{guia.numero}}",
			CTA:  "Rastrear envío", Footer: "El rastreo puede tardar unas horas en actualizarse."},
		Variables: orderVars(Variable{"guia.paqueteria", "Paquetería", "Estafeta"}, Variable{"guia.numero", "Número de guía", "7012345678"}, Variable{"guia.url", "Enlace de rastreo", "https://rastreo.example/7012345678"}),
		ShowItems: true},
	{Key: TplOrderDelivered, Name: "Pedido entregado", Description: "Se envía al marcar un envío como entregado.",
		Subject: "Tu pedido #{{pedido.numero}} fue entregado",
		Blocks: Blocks{Heading: "Tu pedido llegó",
			Body: "Hola {{cliente.nombre}}, tu pedido #{{pedido.numero}} fue entregado.\n\nSi algo no está bien, responde a este correo y lo resolvemos.",
			CTA:  "Ver mi pedido", Footer: ""},
		Variables: orderVars(), ShowItems: true},
	{Key: TplRefundIssued, Name: "Reembolso", Description: "Se envía cuando se reembolsa todo o parte de un pedido.",
		Subject: "Reembolso de tu pedido #{{pedido.numero}}",
		Blocks: Blocks{Heading: "Hicimos un reembolso",
			Body: "Hola {{cliente.nombre}}, reembolsamos {{reembolso.monto}} de tu pedido #{{pedido.numero}}.\n\nTu banco puede tardar de 5 a 10 días hábiles en reflejarlo.",
			CTA:  "Ver mi pedido", Footer: ""},
		Variables: orderVars(Variable{"reembolso.monto", "Monto reembolsado", "$499.00 MXN"}, Variable{"reembolso.motivo", "Motivo", "Producto dañado"})},
	{Key: TplReturnReceived, Name: "Devolución recibida", Description: "Se envía cuando el almacén recibe los productos devueltos.",
		Subject: "Recibimos tu devolución {{devolucion.numero}}",
		Blocks: Blocks{Heading: "Recibimos tu devolución",
			Body: "Hola {{cliente.nombre}}, recibimos los productos de tu devolución {{devolucion.numero}} (pedido #{{pedido.numero}}).\n\n{{devolucion.resolucion}}",
			CTA:  "Ver mi pedido", Footer: ""},
		Variables: orderVars(Variable{"devolucion.numero", "Número de devolución", "RMA-5003"}, Variable{"devolucion.resolucion", "Qué sigue", "Reembolsamos $499.00 MXN a tu método de pago."}),
		ShowItems: true},
	{Key: TplAccessCode, Name: "Código de acceso", Description: "Código de un solo uso para entrar a la cuenta del comprador.",
		Subject: "Tu código para entrar a {{tienda.nombre}}: {{codigo}}",
		Blocks: Blocks{Heading: "Tu código de acceso",
			Body: "Usa este código para entrar a tu cuenta en {{tienda.nombre}}:\n\n{{codigo}}\n\nVence en 10 minutos. Si no lo pediste, ignora este correo.",
			CTA:  "", Footer: "Nunca te pediremos este código por teléfono ni por chat."},
		Variables: []Variable{{"tienda.nombre", "Nombre de la tienda", "Tienda Norte"}, {"codigo", "Código de 6 dígitos", "482913"}}},
	{Key: TplInvoiceReady, Name: "Factura disponible", Description: "Se envía cuando la factura del pedido está lista.",
		Subject: "La factura de tu pedido #{{pedido.numero}} está lista",
		Blocks: Blocks{Heading: "Tu factura está lista",
			Body: "Hola {{cliente.nombre}}, ya puedes descargar la factura de tu pedido #{{pedido.numero}}.",
			CTA:  "Descargar factura", Footer: ""},
		Variables: orderVars(Variable{"factura.url", "Enlace de la factura", "https://facturas.example/1042"})},
	{Key: TplAbandonedCart, Name: "Carrito abandonado", Description: "Recordatorio opcional para quien dejó su correo en el pago y no terminó la compra.",
		Subject: "Dejaste algo en tu carrito",
		Blocks: Blocks{Heading: "Tu carrito te espera",
			Body: "Hola {{cliente.nombre}}, guardamos los productos que elegiste en {{tienda.nombre}}.\n\nPuedes terminar tu compra cuando quieras.",
			CTA:  "Volver a mi carrito", Footer: "Recibes este correo porque empezaste una compra en nuestra tienda."},
		Variables: append(append([]Variable{}, common...), Variable{"carrito.total", "Total del carrito", "$1,598.00 MXN"}),
		ShowItems: true, Optional: true},
	{Key: TplGiftCard, Name: "Tarjeta de regalo o saldo a favor", Description: "Se envía con el código al crear una tarjeta de regalo o al reembolsar como saldo a favor.",
		Subject: "Tienes {{tarjeta.saldo}} para usar en {{tienda.nombre}}",
		Blocks: Blocks{Heading: "{{cliente.nombre}}, tienes saldo en {{tienda.nombre}}",
			Body: "Tu código es:\n\n{{tarjeta.codigo}}\n\nSaldo: {{tarjeta.saldo}}. Vigencia: {{tarjeta.vence}}.\n\nEscríbelo al pagar, en “Tarjeta de regalo o saldo a favor”. Si no lo usas todo, el resto queda guardado en el mismo código.",
			CTA:  "Ir a la tienda", Footer: "Guarda este correo: el código funciona como dinero en la tienda."},
		Variables: append(append([]Variable{}, common...), Variable{"tarjeta.codigo", "Código de la tarjeta", "ABCD-EFGH-JKLM-NPQR"}, Variable{"tarjeta.saldo", "Saldo", "$500.00 MXN"}, Variable{"tarjeta.vence", "Vigencia", "No vence"})},
	{Key: TplSubscriptionCreated, Name: "Suscripción activa", Description: "Se envía al iniciar una compra recurrente, con el enlace para administrarla.",
		Subject: "Tu suscripción en {{tienda.nombre}} está activa",
		Blocks: Blocks{Heading: "{{cliente.nombre}}, tu suscripción está lista",
			Body: "Recibirás {{suscripcion.productos}} {{suscripcion.frecuencia}}.\n\nPróxima entrega: {{suscripcion.proxima}}. Cobramos a tu tarjeta guardada el día de cada entrega.\n\nPuedes pausar, saltar una entrega o cancelar cuando quieras, sin llamar a nadie.",
			CTA:  "Administrar mi suscripción", Footer: "Guarda este correo: el botón es tu acceso a la suscripción."},
		Variables: append(append([]Variable{}, common...), Variable{"suscripcion.productos", "Productos", "1 × Café en grano 1 kg"}, Variable{"suscripcion.frecuencia", "Frecuencia", "cada mes"}, Variable{"suscripcion.proxima", "Próxima entrega", "11 de diciembre de 2026"})},
	{Key: TplSubscriptionFailed, Name: "Suscripción con pago pendiente", Description: "Se envía cuando el cobro de una entrega falla tres veces y la suscripción queda detenida.",
		Subject: "No pudimos cobrar tu suscripción en {{tienda.nombre}}",
		Blocks: Blocks{Heading: "Tu suscripción está detenida",
			Body: "Hola {{cliente.nombre}}, intentamos cobrar tu entrega {{suscripcion.frecuencia}} y tu tarjeta no pasó.\n\nNo te preocupes: no se hizo ningún cargo. Entra a tu suscripción para reactivarla o cancelarla.",
			CTA:  "Revisar mi suscripción", Footer: ""},
		Variables: append(append([]Variable{}, common...), Variable{"suscripcion.frecuencia", "Frecuencia", "cada mes"})},
	{Key: TplTeamInvite, Name: "Invitación al equipo", Description: "Se envía a la persona que invitas a administrar la tienda.",
		Subject: "Te invitaron al equipo de {{tienda.nombre}}",
		Blocks: Blocks{Heading: "{{cliente.nombre}}, únete al equipo de {{tienda.nombre}}",
			Body: "{{equipo.invita}} te invitó a administrar la tienda en Apexx.\n\nCrea tu contraseña con el botón de abajo. La invitación vence en 7 días y solo funciona una vez.",
			CTA:  "Aceptar invitación", Footer: "Si no esperabas esta invitación, ignora este correo."},
		Variables: append(append([]Variable{}, common...), Variable{"equipo.invita", "Quién invita", "Ana Torres"})},
	{Key: TplPurchaseApproval, Name: "Compra de empresa por aprobar", Description: "Se envía a quien aprueba las compras de una empresa cuando un comprador supera su monto autorizado.",
		Subject: "{{solicitud.quien}} pide aprobar una compra de {{solicitud.total}}",
		Blocks: Blocks{Heading: "Compra por aprobar en {{tienda.nombre}}",
			Body: "{{solicitud.quien}} quiere comprar a crédito de {{empresa.nombre}} por {{solicitud.total}}.\n\nRevisa los productos y aprueba o rechaza. El pedido solo se coloca si lo apruebas.",
			CTA:  "Revisar la compra", Footer: "Recibes este correo porque apruebas las compras de tu empresa en esta tienda."},
		Variables: append(append([]Variable{}, common...), Variable{"empresa.nombre", "Empresa", "ACME Industrial"}, Variable{"solicitud.total", "Total", "$12,400.00 MXN"}, Variable{"solicitud.quien", "Quién compra", "Luis Pérez"})},
	{Key: TplQuoteOffered, Name: "Cotización lista", Description: "Se envía cuando la tienda responde una solicitud de cotización con su oferta.",
		Subject: "Tu cotización #{{cotizacion.numero}} está lista",
		Blocks: Blocks{Heading: "Tenemos tu cotización",
			Body: "Hola {{cliente.nombre}}, preparamos la cotización #{{cotizacion.numero}} por {{cotizacion.total}}.\n\nPuedes aceptarla, pedir cambios o rechazarla. Vale hasta el {{cotizacion.vence}}.",
			CTA:  "Ver la cotización", Footer: "Recibes este correo porque pediste una cotización en esta tienda."},
		Variables: append(append([]Variable{}, common...), Variable{"cotizacion.numero", "Número de cotización", "17"}, Variable{"cotizacion.total", "Total", "$24,800.00 MXN"}, Variable{"cotizacion.vence", "Vigente hasta", "30 de noviembre"})},
	{Key: TplCompanyDue, Name: "Saldo de empresa por vencer", Description: "Se envía a quien administra una empresa unos días antes de que venza un cargo a crédito.",
		Subject: "{{empresa.nombre}}: {{saldo.monto}} vence el {{saldo.vence}}",
		Blocks: Blocks{Heading: "Tu saldo está por vencer",
			Body: "El saldo de {{empresa.nombre}} por {{saldo.monto}} vence el {{saldo.vence}}.\n\n{{saldo.cobro}}",
			CTA:  "Ver mi empresa", Footer: "Recibes este correo porque administras la cuenta de tu empresa en esta tienda."},
		Variables: append(append([]Variable{}, common...), Variable{"empresa.nombre", "Empresa", "ACME Industrial"}, Variable{"saldo.monto", "Monto", "$8,200.00 MXN"}, Variable{"saldo.vence", "Vence", "20 de noviembre"},
			Variable{"saldo.cobro", "Cómo se paga", "Lo cobraremos automáticamente a la tarjeta guardada de tu empresa."})},
	{Key: TplCompanyAutopayFail, Name: "Cobro automático rechazado", Description: "Se envía a quien administra una empresa cuando no se pudo cobrar su saldo vencido a la tarjeta guardada.",
		Subject: "No pudimos cobrar el saldo de {{empresa.nombre}}",
		Blocks: Blocks{Heading: "Tu tarjeta no pasó",
			Body: "Intentamos cobrar {{saldo.monto}} del saldo vencido de {{empresa.nombre}} y el banco lo rechazó.\n\n{{saldo.cobro}}",
			CTA:  "Actualizar la tarjeta", Footer: "Recibes este correo porque administras la cuenta de tu empresa en esta tienda."},
		Variables: append(append([]Variable{}, common...), Variable{"empresa.nombre", "Empresa", "ACME Industrial"}, Variable{"saldo.monto", "Monto", "$8,200.00 MXN"},
			Variable{"saldo.cobro", "Qué sigue", "Lo intentaremos de nuevo mañana."})},
	{Key: TplPaymentLink, Name: "Enlace de pago", Description: "Se envía al mandar un pedido borrador o un saldo pendiente para que el cliente pague.",
		Subject: "Completa el pago de tu pedido en {{tienda.nombre}}",
		Blocks: Blocks{Heading: "Tu pedido está listo para pagar",
			Body: "Hola {{cliente.nombre}}, preparamos tu pedido por {{pago.monto}}.\n\nPaga de forma segura con el botón de abajo. El enlace vence en 7 días.",
			CTA:  "Pagar ahora", Footer: ""},
		Variables: append(append([]Variable{}, common...), Variable{"pago.monto", "Monto a pagar", "$1,598.00 MXN"}),
		ShowItems: true},
}

// Definition returns the built-in template for key.
func DefinitionOf(key string) (Definition, bool) {
	for _, d := range Definitions {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}

// Sample builds preview data for a template.
func Sample(d Definition) Data {
	vars := map[string]string{}
	for _, v := range d.Variables {
		vars[v.Name] = v.Sample
	}
	out := Data{Vars: vars, ActionURL: "https://tienda.example/pedido"}
	if d.ShowItems {
		out.Lines = []Line{{"Tenis Sierra Talla 25", 1, "$1,099.00 MXN"}, {"Calcetas Norte Talla 26", 2, "$400.00 MXN"}}
		out.Totals = []Row{{"Productos", "$1,499.00 MXN", false}, {"Envío", "$99.00 MXN", false}, {"Total", "$1,598.00 MXN", true}}
	}
	return out
}

var placeholder = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

func fill(s string, vars map[string]string, escape bool) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		v, ok := vars[name]
		if !ok {
			return ""
		}
		if escape {
			return html.EscapeString(v)
		}
		return v
	})
}

var customVar = regexp.MustCompile(`^(campo|cliente\.campo)\.[a-z][a-z0-9_]{0,39}$`)

// UnknownVariables lists placeholders in the blocks that the template does not provide.
func UnknownVariables(d Definition, subject string, b Blocks) []string {
	known := map[string]bool{}
	for _, v := range d.Variables {
		known[v.Name] = true
	}
	// Emails about an order can use the store's own fields of the order and
	// of the customer; a field without a value renders empty.
	orderMail := known["pedido.numero"]
	seen := map[string]bool{}
	for _, s := range []string{subject, b.Heading, b.Body, b.CTA, b.Footer} {
		for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
			if !known[m[1]] && !(orderMail && customVar.MatchString(m[1])) {
				seen[m[1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func safeColor(c string) string {
	if hexColor.MatchString(c) {
		return c
	}
	return "#1d3fae"
}

// contrast returns black or white, whichever reads better on c.
func contrast(c string) string {
	var r, g, b int64
	for i, dst := range []*int64{&r, &g, &b} {
		var v int64
		for _, ch := range strings.ToLower(c[1+i*2 : 3+i*2]) {
			v *= 16
			if ch >= 'a' {
				v += int64(ch-'a') + 10
			} else {
				v += int64(ch - '0')
			}
		}
		*dst = v
	}
	if (r*299+g*587+b*114)/1000 > 150 {
		return "#111827"
	}
	return "#ffffff"
}

func safeURL(u string) string {
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}

// Rendered is an email ready to send.
type Rendered struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// Render fills subject and blocks with data and wraps them in the branded
// layout. Every variable value is HTML-escaped; the layout works in light and
// dark mail clients.
func Render(subject string, b Blocks, d Data, brand Brand, showItems bool) Rendered {
	vars := map[string]string{}
	for k, v := range d.Vars {
		vars[k] = v
	}
	if vars["tienda.nombre"] == "" {
		vars["tienda.nombre"] = brand.StoreName
	}
	color := safeColor(brand.Color)
	onColor := contrast(color)
	esc := html.EscapeString
	action := safeURL(d.ActionURL)

	var h, t strings.Builder
	h.WriteString(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark"><title>` + esc(fill(subject, vars, false)) + `</title>` +
		`<style>body{margin:0;background:#f3f4f6;color:#111827}.card{background:#ffffff}.muted{color:#6b7280}.line td{border-bottom:1px solid #e5e7eb}` +
		`@media (prefers-color-scheme:dark){body{background:#0f1218!important;color:#e5e7eb!important}.card{background:#181c25!important}.muted{color:#9ca3af!important}.line td{border-bottom:1px solid #2a3040!important}h1,p,td{color:#e5e7eb}}</style></head>` +
		`<body style="margin:0;padding:24px 12px;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:16px;line-height:1.5">` +
		`<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">` +
		`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px">`)
	// Header with the brand colour.
	h.WriteString(`<tr><td style="background:` + color + `;border-radius:12px 12px 0 0;padding:18px 28px">`)
	if logo := safeURL(brand.LogoURL); logo != "" {
		h.WriteString(`<img src="` + esc(logo) + `" alt="` + esc(vars["tienda.nombre"]) + `" height="32" style="display:block;height:32px;border:0">`)
	} else {
		h.WriteString(`<span style="color:` + onColor + `;font-size:18px;font-weight:700">` + esc(vars["tienda.nombre"]) + `</span>`)
	}
	h.WriteString(`</td></tr><tr><td class="card" style="padding:28px;border-radius:0 0 12px 12px">`)

	heading := fill(b.Heading, vars, false)
	if heading != "" {
		h.WriteString(`<h1 style="margin:0 0 16px;font-size:22px;line-height:1.25">` + esc(heading) + `</h1>`)
		t.WriteString(heading + "\n\n")
	}
	for _, p := range strings.Split(strings.ReplaceAll(b.Body, "\r\n", "\n"), "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// A paragraph that is only the access code gets a large, spaced style.
		if strings.TrimSpace(placeholder.ReplaceAllString(p, "")) == "" && strings.Contains(p, "codigo") {
			code := fill(p, vars, true)
			h.WriteString(`<p style="margin:0 0 16px;font-size:32px;font-weight:700;letter-spacing:6px;font-family:ui-monospace,Menlo,Consolas,monospace">` + code + `</p>`)
		} else {
			h.WriteString(`<p style="margin:0 0 16px">` + strings.ReplaceAll(fill(esc(p), escapeVars(vars), false), "\n", "<br>") + `</p>`)
		}
		t.WriteString(fill(p, vars, false) + "\n\n")
	}
	if showItems && len(d.Lines) > 0 {
		h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:8px 0 16px;font-size:15px">`)
		for _, l := range d.Lines {
			h.WriteString(`<tr class="line"><td style="padding:10px 0">` + esc(l.Title) + ` <span class="muted">× ` + itoa(l.Qty) + `</span></td>` +
				`<td align="right" style="padding:10px 0;white-space:nowrap">` + esc(l.Total) + `</td></tr>`)
			t.WriteString("  " + itoa(l.Qty) + " × " + l.Title + "  " + l.Total + "\n")
		}
		for _, r := range d.Totals {
			w := "400"
			if r.Bold {
				w = "700"
			}
			h.WriteString(`<tr><td style="padding:6px 0;font-weight:` + w + `">` + esc(r.Label) + `</td><td align="right" style="padding:6px 0;font-weight:` + w + `;white-space:nowrap">` + esc(r.Value) + `</td></tr>`)
			t.WriteString("  " + r.Label + ": " + r.Value + "\n")
		}
		h.WriteString(`</table>`)
		t.WriteString("\n")
	}
	if cta := fill(b.CTA, vars, false); cta != "" && action != "" {
		h.WriteString(`<p style="margin:8px 0 20px"><a href="` + esc(action) + `" style="display:inline-block;background:` + color + `;color:` + onColor +
			`;text-decoration:none;font-weight:600;padding:12px 22px;border-radius:8px">` + esc(cta) + `</a></p>`)
		t.WriteString(cta + ": " + action + "\n\n")
	}
	if f := fill(b.Footer, vars, false); f != "" {
		h.WriteString(`<p class="muted" style="margin:0;font-size:14px">` + esc(f) + `</p>`)
		t.WriteString(f + "\n\n")
	}
	h.WriteString(`</td></tr><tr><td class="muted" align="center" style="padding:16px 8px;font-size:12px">`)
	foot := brand.Footer
	if foot == "" {
		foot = vars["tienda.nombre"]
	}
	h.WriteString(esc(foot) + `</td></tr></table></td></tr></table></body></html>`)
	t.WriteString("— " + foot + "\n")
	return Rendered{Subject: strings.TrimSpace(fill(subject, vars, false)), HTML: h.String(), Text: t.String()}
}

func escapeVars(vars map[string]string) map[string]string {
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[k] = html.EscapeString(v)
	}
	return out
}

// Money formats minor units for people: 159800, "MXN" → "$1,598.00 MXN".
func Money(amount int64, currency string) string {
	neg := amount < 0
	if neg {
		amount = -amount
	}
	whole := itoa(amount / 100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	cents := itoa(amount % 100)
	if len(cents) < 2 {
		cents = "0" + cents
	}
	out := "$" + whole + "." + cents + " " + strings.TrimSpace(currency)
	if neg {
		return "-" + out
	}
	return out
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
