package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/i18n"
	"github.com/helloghc/apexx/internal/store"
	"github.com/jackc/pgx/v5"
)

// Tags and pixels: the store's analytics, ad pixels and conversion tags,
// managed as data. Publishing writes a new version and an outbox event; every
// node rebuilds its copy and the next page view loads /s/{store}/api/tags.js at
// the new version. No deploy, no code change.
//
// The storefront emits the standard commerce events (GA4 names: view_item,
// add_to_cart, view_cart, begin_checkout, purchase) and tags.js hands them
// to each tag in its own format, so a pixel works by pasting its ID. Tags
// load only with the shopper's consent for their category, never on payment,
// approval, quote or account pages, and the page's security policy allows
// exactly the providers in use.

// tagsRuntime is the script that runs the tags in the browser.
var tagsRuntime = func() string {
	b, err := shopFS.ReadFile("shop/tags-runtime.js")
	if err != nil {
		panic(err)
	}
	return string(b)
}()

// Tag kinds. Each one says what the merchant pastes (its fields and their
// format, as the provider's own installation guide shows them), the group it
// belongs to, the permission it needs by default, and the origins its code
// uses: the page's security policy allows exactly those of the active tags.
const (
	tagGTM       = "gtm"
	tagGA4       = "ga4"
	tagAdobe     = "adobe"
	tagGoogleAds = "gads"
	tagMeta      = "meta"
	tagTikTok    = "tiktok"
	tagPinterest = "pinterest"
	tagSnapchat  = "snapchat"
	tagX         = "x"
	tagLinkedIn  = "linkedin"
	tagMicrosoft = "microsoft"
	tagReddit    = "reddit"
	tagHotjar    = "hotjar"
	tagClarity   = "clarity"
	tagCrazyEgg  = "crazyegg"
	tagWhatsApp  = "whatsapp"
	tagTawk      = "tawk"
	tagCrisp     = "crisp"
	tagTidio     = "tidio"
	tagIntercom  = "intercom"
	tagZendesk   = "zendesk"
	tagHubSpot   = "hubspot"
	tagTrengo    = "trengo"
	tagJivo      = "jivo"
	tagScript    = "script"
	tagCode      = "code"
)

// Places a tag can fire.
var tagEvents = []string{"page_view", "view_item", "add_to_cart", "view_cart", "begin_checkout", "purchase"}

// tagField is one value the merchant fills in.
type tagField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Example  string   `json:"example,omitempty"`
	Help     string   `json:"help,omitempty"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
	re       *regexp.Regexp
	clean    func(string) string
}

type tagKind struct {
	ID                           string     `json:"id"`
	Label                        string     `json:"label"`
	Group                        string     `json:"group"`
	Category                     string     `json:"category"` // default permission
	Fields                       []tagField `json:"fields"`
	Fire                         bool       `json:"fire"`           // the merchant chooses where it fires
	Receives                     string     `json:"receives"`       // what it gets on its own
	Docs                         string     `json:"docs,omitempty"` // the provider's installation guide
	script, connect, style, font []string
}

var (
	upper  = strings.ToUpper
	lower  = strings.ToLower
	digits = func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	reUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func field(key, label, example string, required bool, re string, clean func(string) string) tagField {
	f := tagField{Key: key, Label: label, Example: example, Required: required, clean: clean}
	if re != "" {
		f.re = regexp.MustCompile(re)
	}
	return f
}

const (
	commerceGA     = "Recibe las vistas de página y los eventos de comercio (view_item, add_to_cart, view_cart, begin_checkout, purchase) con producto, monto y moneda; la compra lleva el número de pedido."
	groupAnalytics = "Analítica"
	groupAds       = "Publicidad"
	groupHeat      = "Mapas de calor y grabaciones"
	groupChat      = "Chat y atención"
	groupOwn       = "Avanzado"
)

var tagKindList = []tagKind{
	{ID: tagGTM, Label: "Google Tag Manager", Group: groupAnalytics, Category: "marketing",
		Fields:   []tagField{field("account", "ID del contenedor", "GTM-ABC1234", true, `^GTM-[A-Z0-9]{4,12}$`, upper)},
		Receives: "El contenedor recibe en el dataLayer las vistas y los eventos de comercio (view_item, add_to_cart, view_cart, begin_checkout, purchase con ecommerce). Las etiquetas se administran en Google Tag Manager.",
		Docs:     "https://developers.google.com/tag-platform/tag-manager/web",
		script:   []string{"https://www.googletagmanager.com"}, connect: []string{"https://www.googletagmanager.com", "https://*.google-analytics.com", "https://*.analytics.google.com"}},
	{ID: tagGA4, Label: "Google Analytics 4", Group: groupAnalytics, Category: "analytics",
		Fields: []tagField{field("account", "ID de medición", "G-ABC123DEF4", true, `^G-[A-Z0-9]{4,15}$`, upper)}, Receives: commerceGA,
		Docs:   "https://developers.google.com/analytics/devguides/collection/ga4/ecommerce",
		script: []string{"https://www.googletagmanager.com"}, connect: []string{"https://www.googletagmanager.com", "https://*.google-analytics.com", "https://*.analytics.google.com"}},
	{ID: tagAdobe, Label: "Adobe Analytics (Experience Platform)", Group: groupAnalytics, Category: "analytics",
		Fields:   []tagField{field("account", "Código de inserción de la propiedad (Tags)", "https://assets.adobedtm.com/abc123/def456/launch-0123456789ab.min.js", true, `^https://assets\.adobedtm\.com/[A-Za-z0-9/_.-]{6,200}\.js$`, strings.TrimSpace)},
		Receives: "Carga tu propiedad de Adobe Experience Platform Tags. Los eventos de comercio llegan al Adobe Client Data Layer (window.adobeDataLayer) con el esquema XDM de comercio: commerce.productViews, commerce.productListAdds, commerce.productListViews, commerce.checkouts y commerce.purchases con productListItems y order (purchaseID, priceTotal, currencyCode). Tus reglas de Tags los envían con el Web SDK.",
		Docs:     "https://experienceleague.adobe.com/en/docs/experience-platform/xdm/field-groups/event/commerce-details",
		script:   []string{"https://assets.adobedtm.com"}, connect: []string{"https://assets.adobedtm.com", "https://*.adobedc.net", "https://*.omtrdc.net", "https://*.demdex.net"}},
	{ID: tagGoogleAds, Label: "Google Ads", Group: groupAds, Category: "marketing", Fire: true,
		Fields: []tagField{field("account", "ID de conversión", "AW-123456789", true, `^AW-[0-9]{6,15}$`, upper),
			field("label", "Etiqueta de conversión", "AbC-D_efG-h12_34-567", false, `^[A-Za-z0-9_-]{4,40}$`, strings.TrimSpace)},
		Receives: "Envía la conversión con monto, moneda y número de pedido donde elijas (por omisión, el pedido confirmado).",
		Docs:     "https://support.google.com/google-ads/answer/6331314",
		script:   []string{"https://www.googletagmanager.com", "https://www.googleadservices.com", "https://googleads.g.doubleclick.net"},
		connect:  []string{"https://www.google.com", "https://www.googleadservices.com", "https://googleads.g.doubleclick.net", "https://*.google-analytics.com"}},
	{ID: tagMeta, Label: "Meta (Facebook e Instagram)", Group: groupAds, Category: "marketing",
		Fields:   []tagField{field("account", "ID del píxel", "123456789012345", true, `^[0-9]{8,20}$`, digits)},
		Receives: "Recibe PageView, ViewContent, AddToCart, InitiateCheckout y Purchase con monto, moneda y productos. La compra usa el número de pedido como identificador, así Meta descarta el duplicado de las conversiones desde el servidor.",
		Docs:     "https://developers.facebook.com/docs/meta-pixel/reference",
		script:   []string{"https://connect.facebook.net"}, connect: []string{"https://www.facebook.com", "https://connect.facebook.net"}},
	{ID: tagTikTok, Label: "TikTok", Group: groupAds, Category: "marketing",
		Fields:   []tagField{field("account", "ID del píxel", "C4ABCDEFGH1234567890", true, `^[A-Z0-9]{10,30}$`, upper)},
		Receives: "Recibe la vista de página, ViewContent, AddToCart, InitiateCheckout y CompletePayment con monto y productos; la compra lleva el número de pedido como event_id.",
		Docs:     "https://business-api.tiktok.com/portal/docs?id=1739583652957185",
		script:   []string{"https://analytics.tiktok.com"}, connect: []string{"https://analytics.tiktok.com"}},
	{ID: tagPinterest, Label: "Pinterest", Group: groupAds, Category: "marketing",
		Fields:   []tagField{field("account", "ID de la etiqueta", "2612345678901", true, `^[0-9]{8,20}$`, digits)},
		Receives: "Recibe la vista de página, pagevisit, addtocart y checkout (la compra) con monto, productos y número de pedido.",
		Docs:     "https://help.pinterest.com/en/business/article/add-event-codes",
		script:   []string{"https://s.pinimg.com"}, connect: []string{"https://ct.pinterest.com"}},
	{ID: tagSnapchat, Label: "Snapchat", Group: groupAds, Category: "marketing",
		Fields:   []tagField{field("account", "ID del píxel", "a1b2c3d4-e5f6-7890-abcd-ef1234567890", true, reUUID.String(), lower)},
		Receives: "Recibe PAGE_VIEW, VIEW_CONTENT, ADD_CART, START_CHECKOUT y PURCHASE con precio, moneda, productos y número de pedido (client_dedup_id).",
		Docs:     "https://businesshelp.snapchat.com/s/article/pixel-direct-implementation",
		script:   []string{"https://sc-static.net"}, connect: []string{"https://tr.snapchat.com", "https://*.snapchat.com"}},
	{ID: tagX, Label: "X (Twitter)", Group: groupAds, Category: "marketing",
		Fields: []tagField{field("account", "ID del píxel", "o1a2b", true, `^[a-z0-9]{4,12}$`, lower),
			field("ev_purchase", "ID del evento de compra", "tw-o1a2b-o1a2c", false, `^tw-[a-z0-9]+-[a-z0-9]+$`, lower),
			field("ev_add_to_cart", "ID del evento de agregar al carrito", "tw-o1a2b-o1a2d", false, `^tw-[a-z0-9]+-[a-z0-9]+$`, lower),
			field("ev_begin_checkout", "ID del evento de inicio del pago", "tw-o1a2b-o1a2e", false, `^tw-[a-z0-9]+-[a-z0-9]+$`, lower),
			field("ev_view_item", "ID del evento de producto visto", "tw-o1a2b-o1a2f", false, `^tw-[a-z0-9]+-[a-z0-9]+$`, lower)},
		Receives: "El píxel base cuenta las visitas. Cada evento que creas en el Administrador de eventos de X tiene su ID: pégalo y la tienda lo envía con monto, moneda, productos y número de pedido (conversion_id).",
		Docs:     "https://business.x.com/en/help/campaign-measurement-and-analytics/conversion-tracking-for-websites",
		script:   []string{"https://static.ads-twitter.com"}, connect: []string{"https://analytics.twitter.com", "https://static.ads-twitter.com", "https://t.co"}},
	{ID: tagLinkedIn, Label: "LinkedIn", Group: groupAds, Category: "marketing",
		Fields: []tagField{field("account", "ID de socio (Insight Tag)", "1234567", true, `^[0-9]{4,12}$`, digits),
			field("conv_purchase", "ID de la conversión de compra", "12345678", false, `^[0-9]{4,12}$`, digits),
			field("conv_add_to_cart", "ID de la conversión de agregar al carrito", "12345679", false, `^[0-9]{4,12}$`, digits),
			field("conv_begin_checkout", "ID de la conversión de inicio del pago", "12345680", false, `^[0-9]{4,12}$`, digits)},
		Receives: "El Insight Tag cuenta las visitas. Las conversiones que creas en Campaign Manager se envían con su ID en compra, carrito o inicio del pago, con monto y número de pedido.",
		Docs:     "https://www.linkedin.com/help/lms/answer/a418880",
		script:   []string{"https://snap.licdn.com"}, connect: []string{"https://px.ads.linkedin.com", "https://*.linkedin.com"}},
	{ID: tagMicrosoft, Label: "Microsoft Advertising (Bing)", Group: groupAds, Category: "marketing",
		Fields:   []tagField{field("account", "ID de la etiqueta UET", "12345678", true, `^[0-9]{4,12}$`, digits)},
		Receives: "Recibe las vistas de página y view_item, add_to_cart, begin_checkout y purchase con ecomm_prodid, revenue_value y currency.",
		Docs:     "https://help.ads.microsoft.com/apex/index/3/en/56684",
		script:   []string{"https://bat.bing.com"}, connect: []string{"https://bat.bing.com", "https://*.bing.com", "https://*.clarity.ms"}},
	{ID: tagReddit, Label: "Reddit", Group: groupAds, Category: "marketing",
		Fields:   []tagField{field("account", "ID del píxel", "a2_abc123def456", true, `^(a2|t2)_[a-z0-9]{4,30}$`, lower)},
		Receives: "Recibe PageVisit, ViewContent, AddToCart y Purchase con monto, moneda, productos y número de pedido (conversionId).",
		Docs:     "https://business.reddithelp.com/s/article/manual-conversion-events-with-the-reddit-pixel",
		script:   []string{"https://www.redditstatic.com"}, connect: []string{"https://alb.reddit.com", "https://*.reddit.com"}},
	{ID: tagHotjar, Label: "Hotjar", Group: groupHeat, Category: "analytics",
		Fields:   []tagField{field("account", "ID del sitio", "1234567", true, `^[0-9]{5,12}$`, digits)},
		Receives: "Mapas de calor y grabaciones de sesión. Recibe los eventos de comercio como eventos de Hotjar para filtrar grabaciones (por ejemplo, las que llegaron a la compra).",
		Docs:     "https://help.hotjar.com/hc/en-us/articles/115009336727-How-to-Install-Your-Hotjar-Tracking-Code",
		script:   []string{"https://static.hotjar.com", "https://script.hotjar.com"}, connect: []string{"https://*.hotjar.com", "https://*.hotjar.io", "wss://*.hotjar.com"},
		font: []string{"https://script.hotjar.com"}},
	{ID: tagClarity, Label: "Microsoft Clarity", Group: groupHeat, Category: "analytics",
		Fields:   []tagField{field("account", "ID del proyecto", "abcd1234ef", true, `^[a-z0-9]{6,20}$`, lower)},
		Receives: "Mapas de calor y grabaciones de sesión. Recibe los eventos de comercio como eventos de Clarity para filtrar grabaciones.",
		Docs:     "https://learn.microsoft.com/en-us/clarity/setup-and-installation/clarity-setup",
		script:   []string{"https://www.clarity.ms", "https://*.clarity.ms"}, connect: []string{"https://*.clarity.ms"}},
	{ID: tagCrazyEgg, Label: "Crazy Egg", Group: groupHeat, Category: "analytics",
		Fields:   []tagField{field("account", "Número de cuenta", "00123456", true, `^[0-9]{8}$`, digits)},
		Receives: "Mapas de calor, de desplazamiento y grabaciones de sesión.",
		Docs:     "https://support.crazyegg.com/hc/en-us/articles/360054584474-Install-the-Crazy-Egg-Tracking-Script",
		script:   []string{"https://script.crazyegg.com"}, connect: []string{"https://*.crazyegg.com"}},
	{ID: tagWhatsApp, Label: "Botón de WhatsApp", Group: groupChat, Category: "necessary",
		Fields: []tagField{field("account", "Número de WhatsApp con lada del país", "52 55 1234 5678", true, `^[0-9]{8,15}$`, digits),
			field("message", "Mensaje que aparece escrito", "Hola, tengo una pregunta sobre un producto", false, `^[^<>]{0,200}$`, strings.TrimSpace),
			field("text", "Texto del botón", "¿Te ayudamos?", false, `^[^<>]{0,40}$`, strings.TrimSpace),
			{Key: "position", Label: "Posición", Required: false, Options: []string{"right", "left"}}},
		Receives: "Un botón flotante que abre WhatsApp (en el teléfono, la app; en la computadora, WhatsApp Web) con tu número y el mensaje. En la ficha de producto, el mensaje incluye el nombre del producto. No usa servicios externos ni pide permiso de cookies.",
		Docs:     "https://faq.whatsapp.com/5913398998672934"},
	{ID: tagTawk, Label: "Tawk.to", Group: groupChat, Category: "necessary",
		Fields: []tagField{field("account", "ID de la propiedad", "64a1b2c3d4e5f6a7b8c9d0e1", true, `^[0-9a-f]{24}$`, lower),
			field("widget", "ID del widget", "default", false, `^[a-z0-9]{4,20}$`, lower)},
		Receives: "El chat de Tawk.to en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://help.tawk.to/article/adding-a-widget-to-your-website",
		script:   []string{"https://embed.tawk.to", "https://cdn.jsdelivr.net"}, connect: []string{"https://*.tawk.to", "wss://*.tawk.to"},
		style: []string{"https://embed.tawk.to"}, font: []string{"https://embed.tawk.to"}},
	{ID: tagCrisp, Label: "Crisp", Group: groupChat, Category: "necessary",
		Fields:   []tagField{field("account", "Website ID", "a1b2c3d4-e5f6-7890-abcd-ef1234567890", true, reUUID.String(), lower)},
		Receives: "El chat de Crisp en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://docs.crisp.chat/guides/chatbox-sdks/web-sdk/",
		script:   []string{"https://client.crisp.chat"}, connect: []string{"https://client.crisp.chat", "wss://client.relay.crisp.chat", "https://storage.crisp.chat"},
		style: []string{"https://client.crisp.chat"}, font: []string{"https://client.crisp.chat"}},
	{ID: tagTidio, Label: "Tidio", Group: groupChat, Category: "necessary",
		Fields:   []tagField{field("account", "Clave pública", "abcdefghijklmnopqrstuvwxyz123456", true, `^[a-z0-9]{32}$`, lower)},
		Receives: "El chat de Tidio en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://help.tidio.com/hc/en-us/articles/5424136283548",
		script:   []string{"https://code.tidio.co", "https://widget-v4.tidiochat.com"}, connect: []string{"https://*.tidio.co", "wss://*.tidio.co", "https://*.tidiochat.com", "wss://*.tidiochat.com"},
		style: []string{"https://*.tidiochat.com"}, font: []string{"https://*.tidiochat.com"}},
	{ID: tagIntercom, Label: "Intercom", Group: groupChat, Category: "necessary",
		Fields:   []tagField{field("account", "App ID (Workspace ID)", "abc12def", true, `^[a-z0-9]{6,12}$`, lower)},
		Receives: "El Messenger de Intercom en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://developers.intercom.com/installing-intercom/web/installation",
		script:   []string{"https://widget.intercom.io", "https://js.intercomcdn.com"}, connect: []string{"https://*.intercom.io", "wss://*.intercom.io", "https://*.intercomcdn.com"},
		font: []string{"https://js.intercomcdn.com"}},
	{ID: tagZendesk, Label: "Zendesk", Group: groupChat, Category: "necessary",
		Fields:   []tagField{field("account", "Clave del widget (key)", "a1b2c3d4-e5f6-7890-abcd-ef1234567890", true, reUUID.String(), lower)},
		Receives: "El widget de mensajería de Zendesk en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://developer.zendesk.com/documentation/zendesk-web-widget-sdks/sdks/web/getting_started/",
		script:   []string{"https://static.zdassets.com", "https://*.zdassets.com", "https://*.zendesk.com"},
		connect:  []string{"https://*.zendesk.com", "wss://*.zendesk.com", "https://*.zdassets.com"},
		style:    []string{"https://static.zdassets.com"}, font: []string{"https://static.zdassets.com"}},
	{ID: tagHubSpot, Label: "HubSpot", Group: groupChat, Category: "marketing",
		Fields:   []tagField{field("account", "ID de la cuenta (Hub ID)", "12345678", true, `^[0-9]{4,12}$`, digits)},
		Receives: "El código de HubSpot: chat, formularios y analítica de HubSpot.",
		Docs:     "https://knowledge.hubspot.com/reports/install-the-hubspot-tracking-code",
		script:   []string{"https://js.hs-scripts.com", "https://js.hs-analytics.net", "https://js.hs-banner.com", "https://js.usemessages.com", "https://js.hscollectedforms.net", "https://js.hsadspixel.net"},
		connect:  []string{"https://*.hubspot.com", "https://*.hubapi.com", "wss://*.hubspot.com", "https://*.hs-banner.com", "https://*.hscollectedforms.net", "https://*.hs-analytics.net", "https://*.usemessages.com"}},
	{ID: tagTrengo, Label: "Trengo", Group: groupChat, Category: "necessary",
		Fields:   []tagField{field("account", "Clave del widget", "AbCdEf123456", true, `^[A-Za-z0-9]{8,40}$`, strings.TrimSpace)},
		Receives: "El widget de Trengo (chat, WhatsApp y más canales) en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://help.trengo.com/article/how-to-add-the-trengo-website-widget",
		script:   []string{"https://static.widget.trengo.eu"}, connect: []string{"https://*.trengo.eu", "wss://*.trengo.eu", "https://*.trengo.com", "wss://*.pusher.com", "https://*.pusher.com"},
		style: []string{"https://static.widget.trengo.eu"}, font: []string{"https://static.widget.trengo.eu"}},
	{ID: tagJivo, Label: "JivoChat", Group: groupChat, Category: "necessary",
		Fields:   []tagField{field("account", "ID del widget", "AbCdEf1234", true, `^[A-Za-z0-9]{6,20}$`, strings.TrimSpace)},
		Receives: "El chat de JivoChat en todas las páginas, salvo el pago y la cuenta.",
		Docs:     "https://www.jivochat.com/help/",
		script:   []string{"https://code.jivosite.com", "https://*.jivosite.com"}, connect: []string{"https://*.jivosite.com", "wss://*.jivosite.com"},
		style: []string{"https://*.jivosite.com"}, font: []string{"https://*.jivosite.com"}},
	{ID: tagScript, Label: "Script por URL", Group: groupOwn, Category: "marketing", Fire: true,
		Receives: "Carga la dirección que pongas en los lugares que elijas."},
	{ID: tagCode, Label: "Código propio", Group: groupOwn, Category: "marketing", Fire: true,
		Receives: "Ejecuta tu JavaScript como función con el evento en apexx, en los lugares que elijas."},
}

var tagKinds = func() map[string]tagKind {
	m := map[string]tagKind{}
	for _, k := range tagKindList {
		m[k.ID] = k
	}
	return m
}()

// Tag is one tag or pixel.
type Tag struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Account  string            `json:"account,omitempty"` // the main ID: GTM-…, G-…, pixel id, number…
	Params   map[string]string `json:"params,omitempty"`  // the kind's other fields
	Label    string            `json:"label,omitempty"`   // older versions: the Google Ads label (now params.label)
	Src      string            `json:"src,omitempty"`     // script: its https address
	Code     string            `json:"code,omitempty"`    // code: a function body that gets apexx
	Domains  []string          `json:"domains,omitempty"` // script and code: other https origins they use
	Category string            `json:"category"`          // necessary, analytics, marketing
	Fire     []string          `json:"fire,omitempty"`    // script, code and Google Ads: where it fires
	URL      string            `json:"url,omitempty"`     // only on pages whose address contains this
	Active   bool              `json:"active"`
}

// MetaTag is a verification tag for the page head (Search Console, Bing,
// Meta, Pinterest…): not a script, so it needs no consent and goes on every
// page of the store.
type MetaTag struct {
	Kind    string `json:"kind"`           // google, bing, meta, pinterest, yandex, other
	Name    string `json:"name,omitempty"` // other: the meta name
	Content string `json:"content"`
}

// metaKinds are the verification tags services ask for, by meta name.
var metaKinds = []struct{ ID, Label, Name string }{
	{"google", "Google Search Console", "google-site-verification"},
	{"bing", "Bing Webmaster Tools", "msvalidate.01"},
	{"meta", "Meta (verificación de dominio)", "facebook-domain-verification"},
	{"pinterest", "Pinterest", "p:domain_verify"},
	{"yandex", "Yandex Webmaster", "yandex-verification"},
	{"other", "Otra (nombre y contenido)", ""},
}

// TagsConfig is the store's tags, verification tags and consent banner, at
// a version. Changes says, in words, what this version changed.
type TagsConfig struct {
	Tags       []Tag     `json:"tags"`
	Meta       []MetaTag `json:"meta"`
	BannerText string    `json:"bannerText"`
	// ConsentMode: notice (default: tell the visitor and measure from the
	// first page), analytics (analytics without asking, advertising with
	// permission) or opt_in (everything optional waits for permission).
	ConsentMode   string    `json:"consentMode"`
	Version       int64     `json:"version"`
	PublishedAt   time.Time `json:"publishedAt,omitzero"`
	PublishedBy   string    `json:"publishedBy,omitempty"`
	PublisherName string    `json:"publisherName,omitempty"`
	RestoredFrom  int64     `json:"restoredFrom,omitempty"`
	Changes       []string  `json:"changes,omitempty"`
}

// Consent modes.
const (
	consentNotice    = "notice"
	consentAnalytics = "analytics"
	consentOptIn     = "opt_in"
)

var consentModes = []string{consentNotice, consentAnalytics, consentOptIn}

// Script categories.
var scriptCategories = []string{"necessary", "analytics", "marketing"}

const (
	maxTags     = 30
	maxTagCode  = 20000
	tagsHistory = 20
)

// tagsEntry is a node's built copy of a store's tags.
type tagsEntry struct {
	cfg     TagsConfig
	active  int
	js      []byte            // tags.js
	codes   map[string][]byte // id -> code file
	script  string            // extra script-src
	connect string            // extra connect-src
	style   string            // extra style-src
	font    string            // extra font-src
	meta    template.HTML     // verification tags for the head
	gtm     bool              // a Tag Manager container: its tags need https: and inline scripts
	until   time.Time
	langs   sync.Map // language -> tags.js with the banner's words translated
}

type tagsCache struct {
	mu   sync.Mutex
	m    map[string]*tagsEntry
	once sync.Once
}

func (s *Server) routesTags(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/tags", s.authed("builder", auth.Read, s.getTags))
	handle("PUT /v1/settings/tags", s.authed("builder", auth.Admin, s.putTags))
	handle("GET /v1/settings/tags/history", s.authed("builder", auth.Read, s.tagsHistory))
	handle("POST /v1/settings/tags/restore", s.authed("builder", auth.Admin, s.restoreTags))
	handle("GET /s/{tenant}/api/tags.js", s.tagsJS)
	handle("GET /s/{tenant}/api/tags/{file}", s.tagCodeJS)
}

// watchTags drops a store's built tags on every node when they change.
func (s *Server) watchTags() {
	s.tags.once.Do(func() {
		if s.Proj == nil {
			return
		}
		s.Proj.On(store.TopicSettings, func(tenant, key string) {
			if key == "" || key == store.KeyTags {
				s.tags.mu.Lock()
				delete(s.tags.m, tenant)
				s.tags.mu.Unlock()
			}
		})
	})
}

// loadTags reads a store's tags; a store that still has scripts from before
// tags existed gets them as script tags.
func (s *Server) loadTags(ctx context.Context, tenant string) (TagsConfig, error) {
	var c TagsConfig
	found, err := s.DB.GetDoc(ctx, tenant, "store_tags", "config", &c)
	if err != nil || found {
		if c.Tags == nil {
			c.Tags = []Tag{}
		}
		return c, err
	}
	var old struct {
		Items []struct {
			ID, Name, Category, Src string
			Active                  bool
		} `json:"items"`
		BannerText string `json:"bannerText"`
	}
	if _, err := s.DB.GetDoc(ctx, tenant, "store_scripts", "config", &old); err != nil {
		return c, err
	}
	c.Tags, c.BannerText = []Tag{}, old.BannerText
	for _, it := range old.Items {
		c.Tags = append(c.Tags, Tag{ID: it.ID, Name: it.Name, Kind: tagScript, Src: it.Src, Category: it.Category, Fire: []string{"page_view"}, Active: it.Active})
	}
	return c, nil
}

// tagsOf returns the built tags of a store (built at most once per change).
func (s *Server) tagsOf(tenant string) *tagsEntry {
	s.watchTags()
	s.tags.mu.Lock()
	e, ok := s.tags.m[tenant]
	s.tags.mu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e
	}
	c, err := s.loadTags(context.Background(), tenant)
	if err != nil {
		s.Log.Warn("load tags", "tenant", tenant, "err", err)
		return &tagsEntry{until: time.Now().Add(5 * time.Second)}
	}
	e = buildTags(tenant, c)
	s.tags.mu.Lock()
	if s.tags.m == nil {
		s.tags.m = map[string]*tagsEntry{}
	}
	s.tags.m[tenant] = e
	s.tags.mu.Unlock()
	return e
}

// tagsHead is what every storefront page of the store carries in its head:
// its verification tags (always) and, on pages that may load them, its
// tags.js at the current version.
func (s *Server) tagsHead(tenant string) (meta, script template.HTML) {
	if tenant == "" {
		return "", ""
	}
	e := s.tagsOf(tenant)
	if e.active > 0 {
		script = template.HTML(`<script src="/s/` + url.PathEscape(tenant) + `/api/tags.js?v=` + strconv.FormatInt(e.cfg.Version, 10) + `" defer></script>`) // #nosec G203 -- tenant is escaped, version is a number
	}
	return e.meta, script
}

// buildTags makes tags.js, the verification tags and the policy origins of
// a store's tags.
func buildTags(tenant string, c TagsConfig) *tagsEntry {
	e := &tagsEntry{cfg: c, codes: map[string][]byte{}, until: time.Now().Add(5 * time.Minute)}
	var script, connect, style, font []string
	add := func(list *[]string, o ...string) {
		for _, x := range o {
			if x != "" && !slices.Contains(*list, x) {
				*list = append(*list, x)
			}
		}
	}
	type pub struct {
		ID       string            `json:"id"`
		Name     string            `json:"name"`
		Kind     string            `json:"kind"`
		Account  string            `json:"account,omitempty"`
		Params   map[string]string `json:"params,omitempty"`
		Src      string            `json:"src,omitempty"`
		Code     string            `json:"code,omitempty"` // address of its code file
		Category string            `json:"cat"`
		Fire     []string          `json:"fire,omitempty"`
		URL      string            `json:"url,omitempty"`
	}
	out := []pub{}
	for _, t := range c.Tags {
		if !t.Active {
			continue
		}
		k, ok := tagKinds[t.Kind]
		if !ok {
			continue
		}
		e.active++
		add(&script, k.script...)
		add(&connect, k.connect...)
		add(&style, k.style...)
		add(&font, k.font...)
		params := t.Params
		if t.Label != "" && params["label"] == "" { // versions before params
			params = map[string]string{"label": t.Label}
		}
		p := pub{ID: t.ID, Name: t.Name, Kind: t.Kind, Account: t.Account, Params: params, Category: t.Category, Fire: t.Fire, URL: t.URL}
		switch t.Kind {
		case tagGTM:
			e.gtm = true
		case tagScript:
			p.Src = t.Src
			if o := originOf(t.Src); o != "" {
				add(&script, o)
				add(&connect, o)
			}
			add(&script, t.Domains...)
			add(&connect, t.Domains...)
		case tagCode:
			p.Code = "/s/" + url.PathEscape(tenant) + "/api/tags/" + url.PathEscape(t.ID) + ".js?v=" + strconv.FormatInt(c.Version, 10)
			add(&script, t.Domains...)
			add(&connect, t.Domains...)
			id, _ := json.Marshal(t.ID)
			// Each piece of code lives in its own file: a mistake in one stops
			// that tag only. It runs as a function that gets the event.
			e.codes[t.ID] = []byte("(window.apexxCode=window.apexxCode||{})[" + string(id) + "]=function(apexx){\n" + t.Code + "\n};\n")
		}
		out = append(out, p)
	}
	e.script, e.connect = strings.Join(script, " "), strings.Join(connect, " ")
	e.style, e.font = strings.Join(style, " "), strings.Join(font, " ")
	var meta strings.Builder
	for _, m := range c.Meta {
		meta.WriteString(`<meta name="` + template.HTMLEscapeString(m.Name) + `" content="` + template.HTMLEscapeString(m.Content) + `">`)
	}
	e.meta = template.HTML(meta.String()) // #nosec G203 -- both attributes are escaped (and validated on publish)
	mode := c.ConsentMode
	if mode == "" {
		mode = consentNotice
	}
	cfg, _ := json.Marshal(map[string]any{"v": c.Version, "mode": mode, "banner": c.BannerText, "tags": out})
	e.js = []byte("(function(C){\n" + tagsRuntime + "\n})(" + string(cfg) + ");\n")
	if e.active == 0 {
		e.js = []byte("/* sin etiquetas activas */\n")
	}
	return e
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return "https://" + u.Host
}

// shopCSPFor is the storefront security policy of a page: the base policy,
// plus the origins of the store's tags on pages that may load them. A Tag
// Manager container decides its tags in its own console, so with one the
// page allows https scripts and inline tags (still never on payment,
// approval, quote or account pages).
func (s *Server) shopCSPFor(tenant string, thirdParty bool) string {
	if !thirdParty || tenant == "" {
		return shopCSP
	}
	// App content sections load in sandboxed frames from any https origin.
	base := strings.Replace(shopCSP, "frame-src https://js.stripe.com https://hooks.stripe.com;", "frame-src https://js.stripe.com https://hooks.stripe.com https:;", 1)
	e := s.tagsOf(tenant)
	script, connect := e.script, e.connect
	if e.gtm {
		script, connect = "https: 'unsafe-inline'", "https: wss:"
	}
	if script != "" {
		base = strings.Replace(base, "script-src 'self' https://js.stripe.com;", "script-src 'self' https://js.stripe.com "+script+";", 1)
	}
	if connect != "" {
		base = strings.Replace(base, "connect-src 'self' https://api.stripe.com;", "connect-src 'self' https://api.stripe.com "+connect+";", 1)
	}
	if e.style != "" {
		base = strings.Replace(base, "style-src 'self' 'unsafe-inline';", "style-src 'self' 'unsafe-inline' "+e.style+";", 1)
	}
	if e.font != "" {
		base = strings.Replace(base, "font-src 'self';", "font-src 'self' "+e.font+";", 1)
	}
	return base
}

// ---------- panel ----------

func (s *Server) getTags(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadTags(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	metas := []map[string]string{}
	for _, m := range metaKinds {
		metas = append(metas, map[string]string{"id": m.ID, "label": m.Label, "name": m.Name})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": c, "kinds": tagKindList, "metaKinds": metas, "events": tagEvents})
}

var (
	reTagID    = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	reMetaName = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,60}$`)
	reMetaVal  = regexp.MustCompile(`^[A-Za-z0-9_:.=+/-]{4,200}$`)
)

// validateTags normalizes c and lists what is wrong.
func (s *Server) validateTags(c *TagsConfig) []apierr.FieldError {
	var errs []apierr.FieldError
	if len(c.Tags) > maxTags {
		errs = append(errs, apierr.FieldError{Field: "tags", Code: "too_many", Message: "Hasta " + strconv.Itoa(maxTags) + " etiquetas"})
	}
	c.BannerText = strings.TrimSpace(c.BannerText)
	if c.ConsentMode == "" {
		c.ConsentMode = consentNotice
	}
	if !slices.Contains(consentModes, c.ConsentMode) {
		errs = append(errs, apierr.FieldError{Field: "consentMode", Code: "invalid", Message: "Elige cómo se pide el permiso: solo aviso, analítica sin permiso o todo con permiso"})
	}
	if len([]rune(c.BannerText)) > 300 {
		errs = append(errs, apierr.FieldError{Field: "bannerText", Code: "too_long", Message: "Texto de hasta 300 caracteres"})
	}
	publicHTTPS := func(raw string) bool {
		u, err := url.Parse(raw)
		return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && len(raw) <= 500 &&
			(s.Webhooks == nil || s.Webhooks.AllowPrivate || s.Webhooks.CheckURL(raw) == nil)
	}
	ids := map[string]bool{}
	for i := range c.Tags {
		t := &c.Tags[i]
		f := "tags[" + strconv.Itoa(i) + "]"
		bad := func(field, msg string) {
			errs = append(errs, apierr.FieldError{Field: f + "." + field, Code: "invalid", Message: msg})
		}
		t.Name, t.Account, t.Src, t.URL = strings.TrimSpace(t.Name), strings.TrimSpace(t.Account), strings.TrimSpace(t.Src), strings.TrimSpace(t.URL)
		if t.ID == "" || ids[t.ID] || !reTagID.MatchString(t.ID) {
			t.ID = newRandomID("tag_")
		}
		ids[t.ID] = true
		k, known := tagKinds[t.Kind]
		if !known {
			bad("kind", "Elige un tipo de etiqueta")
			continue
		}
		if t.Name == "" {
			t.Name = k.Label
		}
		if len([]rune(t.Name)) > 60 {
			bad("name", "Escribe un nombre de hasta 60 caracteres")
		}
		if t.Category == "" {
			t.Category = k.Category
		}
		if !slices.Contains(scriptCategories, t.Category) {
			bad("category", "Categoría: necesaria, analítica o publicidad")
		}
		// The kind's fields: the main ID in Account, the rest in Params.
		if t.Label != "" { // versions before params
			if t.Params == nil {
				t.Params = map[string]string{}
			}
			if t.Params["label"] == "" {
				t.Params["label"] = t.Label
			}
			t.Label = ""
		}
		params := map[string]string{}
		for _, fd := range k.Fields {
			v := t.Params[fd.Key]
			if fd.Key == "account" {
				v = t.Account
			}
			v = strings.TrimSpace(v)
			if fd.clean != nil {
				v = fd.clean(v)
			}
			switch {
			case v == "" && fd.Required:
				bad(fd.Key, "Falta: "+lowerFirst(fd.Label))
			case v == "":
			case len(fd.Options) > 0 && !slices.Contains(fd.Options, v):
				bad(fd.Key, "Opción desconocida: "+v)
			case fd.re != nil && !fd.re.MatchString(v):
				msg := lowerFirst(fd.Label) + " no tiene el formato de " + k.Label
				if fd.Example != "" {
					msg += " (por ejemplo, " + fd.Example + ")"
				}
				bad(fd.Key, strings.ToUpper(msg[:1])+msg[1:])
			}
			if fd.Key == "account" {
				t.Account = v
			} else if v != "" {
				params[fd.Key] = v
			}
		}
		t.Params = nil
		if len(params) > 0 {
			t.Params = params
		}
		if t.Kind == tagScript {
			if !publicHTTPS(t.Src) {
				bad("src", "La dirección del script empieza con https:// y es pública")
			}
		} else {
			t.Src = ""
		}
		if t.Kind == tagCode {
			if strings.TrimSpace(t.Code) == "" || len(t.Code) > maxTagCode {
				bad("code", "Escribe el código (hasta 20 000 caracteres)")
			}
			if strings.Contains(strings.ToLower(t.Code), "<script") {
				bad("code", "Pega solo el código JavaScript, sin las etiquetas <script>")
			}
		} else {
			t.Code = ""
		}
		if t.Kind == tagScript || t.Kind == tagCode {
			var ds []string
			for _, d := range t.Domains {
				d = strings.TrimSuffix(strings.TrimSpace(d), "/")
				if d == "" {
					continue
				}
				if o := originOf(d); o == "" || o != d || !publicHTTPS(d) {
					bad("domains", "Cada dominio es https://nombre.dominio, público")
				} else if !slices.Contains(ds, d) {
					ds = append(ds, d)
				}
			}
			if len(ds) > 10 {
				bad("domains", "Hasta 10 dominios")
			}
			t.Domains = ds
		} else {
			t.Domains = nil
		}
		// Where it fires: providers follow every event on their own; Google
		// Ads, scripts and code fire where the store says.
		if k.Fire {
			var fire []string
			for _, ev := range t.Fire {
				if !slices.Contains(tagEvents, ev) {
					bad("fire", "Lugar desconocido: "+ev)
				} else if !slices.Contains(fire, ev) {
					fire = append(fire, ev)
				}
			}
			if len(fire) == 0 {
				if t.Kind == tagGoogleAds {
					fire = []string{"purchase"}
				} else {
					bad("fire", "Elige dónde se dispara")
				}
			}
			t.Fire = fire
		} else {
			t.Fire = nil
		}
		if len(t.URL) > 200 {
			bad("url", "Hasta 200 caracteres")
		}
	}
	// Verification tags.
	if len(c.Meta) > 20 {
		errs = append(errs, apierr.FieldError{Field: "meta", Code: "too_many", Message: "Hasta 20 etiquetas de verificación"})
	}
	for i := range c.Meta {
		m := &c.Meta[i]
		f := "meta[" + strconv.Itoa(i) + "]"
		m.Name, m.Content = strings.TrimSpace(m.Name), strings.TrimSpace(m.Content)
		// A pasted <meta name="…" content="…"> is read for its parts.
		if strings.HasPrefix(m.Content, "<meta") {
			if n := attr(m.Content, "name"); n != "" && m.Kind == "other" && m.Name == "" {
				m.Name = n
			}
			m.Content = attr(m.Content, "content")
		}
		known := false
		for _, mk := range metaKinds {
			if mk.ID == m.Kind {
				known = true
				if mk.Name != "" {
					m.Name = mk.Name
				}
			}
		}
		switch {
		case !known:
			errs = append(errs, apierr.FieldError{Field: f + ".kind", Code: "invalid", Message: "Elige el servicio"})
		case !reMetaName.MatchString(m.Name) || strings.EqualFold(m.Name, "viewport") || strings.EqualFold(m.Name, "robots") || strings.HasPrefix(strings.ToLower(m.Name), "http"):
			errs = append(errs, apierr.FieldError{Field: f + ".name", Code: "invalid", Message: "El nombre tiene letras, números, puntos, dos puntos o guiones"})
		case !reMetaVal.MatchString(m.Content):
			errs = append(errs, apierr.FieldError{Field: f + ".content", Code: "invalid", Message: "Pega el código de verificación (letras, números y - _ : . = + /)"})
		}
	}
	if c.Meta == nil {
		c.Meta = []MetaTag{}
	}
	return errs
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if len(r) > 1 && r[1] >= 'A' && r[1] <= 'Z' { // "ID del…" stays
		return s
	}
	return strings.ToLower(string(r[:1])) + string(r[1:])
}

// attr reads one attribute of a pasted tag.
func attr(tag, name string) string {
	m := regexp.MustCompile(`(?i)\b` + name + `\s*=\s*["']([^"']*)["']`).FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// tagsChanges says, in words, what next changes from prev.
func tagsChanges(prev, next TagsConfig) []string {
	var out []string
	label := func(t Tag) string {
		k := tagKinds[t.Kind].Label
		if t.Name != "" && t.Name != k {
			k = t.Name + " (" + tagKinds[t.Kind].Label + ")"
		}
		if t.Account != "" {
			k += " " + t.Account
		}
		return k
	}
	old := map[string]Tag{}
	for _, t := range prev.Tags {
		old[t.ID] = t
	}
	seen := map[string]bool{}
	for _, t := range next.Tags {
		seen[t.ID] = true
		o, ok := old[t.ID]
		if !ok {
			st := ""
			if !t.Active {
				st = " (apagada)"
			}
			out = append(out, "Agregó "+label(t)+st)
			continue
		}
		var diffs []string
		cmp := func(what, a, b string) {
			if a != b {
				if len(a) > 60 || len(b) > 60 {
					diffs = append(diffs, what)
				} else {
					diffs = append(diffs, fmt.Sprintf("%s: «%s» → «%s»", what, a, b))
				}
			}
		}
		cmp("nombre", o.Name, t.Name)
		cmp("ID", o.Account, t.Account)
		for _, fd := range tagKinds[t.Kind].Fields {
			if fd.Key != "account" {
				cmp(lowerFirst(fd.Label), o.Params[fd.Key], t.Params[fd.Key])
			}
		}
		cmp("dirección", o.Src, t.Src)
		if o.Code != t.Code {
			diffs = append(diffs, "código")
		}
		cmp("permiso", o.Category, t.Category)
		cmp("lugares", strings.Join(o.Fire, ", "), strings.Join(t.Fire, ", "))
		cmp("solo en direcciones con", o.URL, t.URL)
		cmp("dominios", strings.Join(o.Domains, " "), strings.Join(t.Domains, " "))
		if o.Active != t.Active {
			if t.Active {
				diffs = append(diffs, "la encendió")
			} else {
				diffs = append(diffs, "la apagó")
			}
		}
		if len(diffs) > 0 {
			out = append(out, "Cambió "+label(t)+": "+strings.Join(diffs, "; "))
		}
	}
	for _, t := range prev.Tags {
		if !seen[t.ID] {
			out = append(out, "Quitó "+label(t))
		}
	}
	key := func(m MetaTag) string { return m.Name + "=" + m.Content }
	pm, nm := map[string]bool{}, map[string]bool{}
	for _, m := range prev.Meta {
		pm[key(m)] = true
	}
	for _, m := range next.Meta {
		nm[key(m)] = true
		if !pm[key(m)] {
			out = append(out, "Agregó la verificación "+m.Name)
		}
	}
	for _, m := range prev.Meta {
		if !nm[key(m)] {
			out = append(out, "Quitó la verificación "+m.Name)
		}
	}
	if pm, nm := firstNonEmpty(prev.ConsentMode, consentNotice), firstNonEmpty(next.ConsentMode, consentNotice); pm != nm {
		names := map[string]string{consentNotice: "solo aviso", consentAnalytics: "analítica sin permiso, publicidad con permiso", consentOptIn: "todo con permiso"}
		out = append(out, "Cambió el permiso de cookies: «"+names[pm]+"» → «"+names[nm]+"»")
	}
	if prev.BannerText != next.BannerText {
		out = append(out, "Cambió el texto del aviso de cookies")
	}
	if len(out) == 0 {
		out = []string{"Sin cambios"}
	}
	return out
}

func (s *Server) putTags(w http.ResponseWriter, r *http.Request) {
	var c TagsConfig
	if !decode(w, r, &c) {
		return
	}
	s.publishTags(w, r, c, 0)
}

// publishTags validates, versions and publishes c.
func (s *Server) publishTags(w http.ResponseWriter, r *http.Request, c TagsConfig, restored int64) {
	pr := principal(r)
	if errs := s.validateTags(&c); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa las etiquetas", errs...)
		return
	}
	cur, err := s.loadTags(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	c.Version, c.PublishedAt, c.PublishedBy, c.PublisherName, c.RestoredFrom = cur.Version+1, time.Now().UTC(), pr.Actor(), firstNonEmpty(pr.Name, pr.Email, pr.ID), restored
	// What changed, in words: kept with the version and in the audit log
	// (which also keeps the whole configuration of each version).
	c.Changes = tagsChanges(cur, c)
	if restored > 0 {
		c.Changes = append([]string{fmt.Sprintf("Restauró la versión %d", restored)}, c.Changes...)
	}
	// The configuration and its copy in the history, in one transaction; the
	// event makes every node rebuild it.
	err = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if err := store.SetDocTx(r.Context(), tx, pr.Tenant, pr.Actor(), "store_tags", "config", store.KeyTags, c); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_docs (tenant_id, kind, id, data, updated_at) VALUES ($1, 'store_tags_history', $2, $3, now())
			ON CONFLICT (tenant_id, kind, id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, pr.Tenant, fmt.Sprintf("%010d", c.Version), mustJSONBytes(c)); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `DELETE FROM tenant_docs WHERE tenant_id = $1 AND kind = 'store_tags_history' AND id < $2`, pr.Tenant, fmt.Sprintf("%010d", c.Version-tagsHistory+1))
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// This node at once; the others when the event arrives (milliseconds).
	s.tags.mu.Lock()
	delete(s.tags.m, pr.Tenant)
	s.tags.mu.Unlock()
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": c})
}

func mustJSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (s *Server) tagsHistory(w http.ResponseWriter, r *http.Request) {
	docs, err := s.DB.ListDocs(r.Context(), principal(r).Tenant, "store_tags_history")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	items := []map[string]any{}
	for i := len(docs) - 1; i >= 0; i-- {
		var c TagsConfig
		if json.Unmarshal(docs[i].Data, &c) != nil {
			continue
		}
		active := 0
		for _, t := range c.Tags {
			if t.Active {
				active++
			}
		}
		items = append(items, map[string]any{"version": c.Version, "publishedAt": c.PublishedAt, "publishedBy": c.PublishedBy, "publisherName": c.PublisherName,
			"restoredFrom": c.RestoredFrom, "changes": c.Changes, "tags": len(c.Tags), "active": active, "meta": len(c.Meta)})
	}
	slices.SortFunc(items, func(a, b map[string]any) int { return int(b["version"].(int64) - a["version"].(int64)) })
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// restoreTags publishes an earlier version again, as a new version.
func (s *Server) restoreTags(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int64 `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	var c TagsConfig
	found, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "store_tags_history", fmt.Sprintf("%010d", in.Version), &c)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !found {
		apierr.Write(w, apierr.Problem{Status: http.StatusNotFound, Code: "not_found", Detail: "Esa versión ya no está en el historial"})
		return
	}
	s.publishTags(w, r, c, in.Version)
}

// ---------- storefront ----------

// tagsJS serves the store's tags at a version: immutable, since a change
// makes a new version and pages point to it at once.
func (s *Server) tagsJS(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) {
		return
	}
	e := s.tagsOf(r.PathValue("tenant"))
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == strconv.FormatInt(e.cfg.Version, 10) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	js := e.js
	// A page in another language asks with ?hl=: the consent banner speaks it.
	if hl := r.URL.Query().Get("hl"); hl != i18n.Default && i18n.Known(hl) {
		if v, ok := e.langs.Load(hl); ok {
			js = v.([]byte)
		} else {
			js = []byte(i18n.Script(string(e.js), i18n.Builtin(hl)))
			e.langs.Store(hl, js)
		}
	}
	_, _ = w.Write(js)
}

func (s *Server) tagCodeJS(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) {
		return
	}
	id := strings.TrimSuffix(r.PathValue("file"), ".js")
	e := s.tagsOf(r.PathValue("tenant"))
	b, ok := e.codes[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == strconv.FormatInt(e.cfg.Version, 10) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(b)
}
