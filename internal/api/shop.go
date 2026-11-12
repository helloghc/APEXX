package api

import (
	"embed"
	"encoding/json"
	"errors"
	"github.com/helloghc/apexx/internal/i18n"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

//go:embed shop/*.tmpl shop/*.css shop/*.js shop/fonts/*.woff2
var shopFS embed.FS

// shopFuncs are the helpers the storefront templates can call.
var shopFuncs = template.FuncMap{
	// stars renders a rating (1 to 5, int or average) as five characters.
	"stars": func(v any) string {
		n := 0
		switch x := v.(type) {
		case int:
			n = x
		case float64:
			n = int(x + 0.5)
		}
		n = max(0, min(5, n))
		return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
	},
	"stars5": func() []int { return []int{5, 4, 3, 2, 1} },
	"day":    func(t time.Time) string { return shortDate(t) },
	"cents":  func(n int64) string { return pesos(money.New(n, "MXN")) },
	"plain":  pesosPlain,
	"levels": func() []int { return []int{1, 2, 3, 4, 5} },
	"minus":  func(a, b int) int { return a - b },
}

var shopPages = localizedPages("catalog", "product", "cart", "checkout", "thanks", "policy", "favorites", "subscription", "form", "approval", "quote", "quickorder", "compare", "blog", "notice", "registry")

// shopCSP allows only our own assets plus Stripe for the payment form.
const shopCSP = "default-src 'self'; img-src * data:; style-src 'self' 'unsafe-inline'; font-src 'self'; script-src 'self' https://js.stripe.com; " +
	"frame-src https://js.stripe.com https://hooks.stripe.com; connect-src 'self' https://api.stripe.com; base-uri 'none'; form-action 'self'"

type pageData struct {
	Title, Description, Store, Tenant, Base, Channel, Query string
	Canonical                                               string // path of the page without filters, for search engines
	NoIndex                                                 bool   // filtered or paginated views are not indexed
	Origin, OGImage                                         string // absolute origin and image for social previews
	Chrome                                                  builder.Chrome
	Registries                                              bool // the store offers gift registries
}

type cardView struct {
	Handle, Title, Brand, Initial, Price, CompareAt, SKU, Base string
	Tone                                                       int
	SoldOut                                                    bool
	Img                                                        template.HTML // cover image; empty shows the placeholder
}

func initial(s string) string {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(s))
	if r == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(r))
}

func pesos(m money.Money) string {
	whole, cents := m.Amount/100, m.Amount%100
	s := strconv.FormatInt(whole, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return "$" + s + "." + leftPad(strconv.FormatInt(cents, 10)) + " " + string(m.Currency)
}

// shopContext resolves the tenant, its read models and the default channel.
func (s *Server) shopContext(w http.ResponseWriter, r *http.Request) (*projection.Tenant, pageData, bool) {
	tenant := r.PathValue("tenant")
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		http.NotFound(w, r)
		return nil, pageData{}, false
	}
	ch := mainChannel
	if mk := s.marketFor(r, t, tenant, r.PathValue("site")); mk != "" {
		ch = mk
	}
	if _, ok := t.Channel(ch); !ok {
		http.Error(w, "La tienda no tiene canal de venta configurado", http.StatusServiceUnavailable)
		return nil, pageData{}, false
	}
	st, err := s.openSite(tenant, r.PathValue("site"))
	if errors.Is(err, errNoSite) {
		http.NotFound(w, r)
		return nil, pageData{}, false
	}
	if err != nil {
		internalError(w, s.Log, err)
		return nil, pageData{}, false
	}
	pd := pageData{Store: tenant, Tenant: tenant, Base: "/s/" + tenant + "/" + r.PathValue("site"), Channel: ch, Chrome: st.LiveChrome(), Registries: t.GiftOptions().Registries}
	return t, s.decorate(r, t, pd), true
}

// tenantID lets the page renderer find the store of any page data.
func (p pageData) tenantID() string { return p.Tenant }

// pagesWithoutScripts never load the store's third-party scripts: the
// payment and the shopper's data stay out of their reach.
var pagesWithoutScripts = map[string]bool{"checkout": true, "pay": true, "approval": true, "quote": true}

func (s *Server) renderShop(w http.ResponseWriter, page string, data any, cache bool) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	tenant := ""
	if d, ok := data.(interface{ tenantID() string }); ok {
		tenant = d.tenantID()
	}
	h.Set("Content-Security-Policy", s.shopCSPFor(tenant, !pagesWithoutScripts[page]))
	if cache {
		h.Set("Cache-Control", "public, max-age=0, s-maxage=30, stale-while-revalidate=60")
	} else {
		h.Set("Cache-Control", "no-store")
	}
	if err := shopPages[pageLang(w)][page].ExecuteTemplate(w, "layout", data); err != nil {
		s.Log.Error("render shop page", "page", page, "err", err)
	}
}

func cardOf(t *projection.Tenant, ch string, p catalog.Product) cardView {
	c := cardView{Handle: p.Handle, Title: p.Title, Brand: p.Brand, Initial: initial(p.Title), Tone: builder.ToneOf(p.Title), SoldOut: true}
	for _, v := range p.Variants {
		if t.Inventory.Available(v.SKU) > 0 {
			if c.SoldOut {
				c.SKU = v.SKU
			}
			c.SoldOut = false
		}
	}
	if len(p.Variants) > 0 {
		if q, ok := t.Pricing.Resolve(ch, p.Variants[0].SKU, 1); ok {
			c.Price = pesos(q.Unit)
			if q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
				c.CompareAt = pesos(*q.CompareAt)
			}
		}
	}
	return c
}

func firstSKU(p catalog.Product) string {
	if len(p.Variants) > 0 {
		return p.Variants[0].SKU
	}
	return ""
}

func priceNumber(t *projection.Tenant, ch string, p catalog.Product) string {
	if q, ok := t.Pricing.Resolve(ch, firstSKU(p), 1); ok {
		return strconv.FormatInt(q.Unit.Amount/100, 10) + "." + leftPad(strconv.FormatInt(q.Unit.Amount%100, 10))
	}
	return "0"
}

func (s *Server) shopStatic(page, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, pd, ok := s.shopContext(w, r)
		if !ok {
			return
		}
		pd.Title = title
		s.renderShop(w, page, pd, false)
	}
}

// shopAssets serves the storefront CSS and script (versioned with ?v=).
func shopAssets(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	ctype := ""
	switch {
	case strings.HasSuffix(name, ".css"):
		ctype = "text/css; charset=utf-8"
	case name == "shop.js":
		ctype = "text/javascript; charset=utf-8"
	}
	b, err := shopFS.ReadFile("shop/" + name)
	if name == "shop.js" && err == nil {
		if hl := r.URL.Query().Get("hl"); i18n.Known(hl) {
			b = shopScript(hl)
		}
	}
	if ctype == "" || err != nil || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// shopFonts serves the bundled woff2 fonts with a one-year immutable cache.
func shopFonts(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	b, err := shopFS.ReadFile("shop/fonts/" + name)
	if err != nil || !strings.HasSuffix(name, ".woff2") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(b)
}

// ---------- public storefront API ----------

// shopLimiter allows n requests per minute per key (IP), in memory per node.
type shopLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	n    int
}

// reset forgets every hit (tests start each run clean).
func (l *shopLimiter) reset() {
	l.mu.Lock()
	l.hits = nil
	l.mu.Unlock()
}

func (l *shopLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	now := time.Now()
	h := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < time.Minute {
			h = append(h, t)
		}
	}
	if len(h) >= l.n {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}

var (
	simLimiter      = &shopLimiter{n: 240}
	couponLimiter   = &shopLimiter{n: 30}
	checkoutLimiter = &shopLimiter{n: 20}
)

func (s *Server) publicTenant(w http.ResponseWriter, r *http.Request, lim *shopLimiter) (*projection.Tenant, func(), bool) {
	tenant := r.PathValue("tenant")
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return nil, nil, false
	}
	if !lim.allow(tenant + "|" + s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "rate_limited", Detail: "Demasiadas solicitudes; espera un minuto"})
		return nil, nil, false
	}
	release, ok := s.Bulkhead.Acquire(tenant)
	if !ok {
		w.Header().Set("Retry-After", "1")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "tenant_busy", Detail: "La tienda está muy ocupada; reintenta en un segundo"})
		return nil, nil, false
	}
	return t, release, true
}

func (s *Server) shopSimulate(w http.ResponseWriter, r *http.Request) {
	t, release, ok := s.publicTenant(w, r, simLimiter)
	if !ok {
		return
	}
	defer release()
	var req cart.Request
	if !decode(w, r, &req) {
		return
	}
	if errs := append(validateItems(req.Channel, req.Items), validateCartExtras(&req)...); len(errs) > 0 {
		apierr.BadRequest(w, "El carrito tiene errores", errs...)
		return
	}
	if !shopChannel(t, req.Channel) {
		apierr.BadRequest(w, "Canal de venta no disponible")
		return
	}
	// Trying codes is limited separately, so coupons cannot be guessed in bulk.
	if len(req.Coupons) > 0 && !couponLimiter.allow(r.PathValue("tenant")+"|"+s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "rate_limited", Detail: "Demasiados intentos con cupones; espera un minuto"})
		return
	}
	if err := s.resolveCart(r, r.PathValue("tenant"), &req); err != nil {
		internalError(w, s.Log, err)
		return
	}
	comp, _ := s.shopperCompany(r)
	if c := comp; c != nil {
		req.Extra = append(req.Extra, orders.CompanyPromo(c)...)
		req.Group = orders.CompanyGroup(c)
		req.RestrictGroup = c.CatalogRestricted
		req.TaxExempt = c.TaxExempt
	}
	if _, fe := s.applyRegistry(r.Context(), r.PathValue("tenant"), req.Registry, req.ShipToRegistry, &req.PostalCode, nil); fe != nil {
		apierr.BadRequest(w, "", *fe)
		return
	}
	res, err := t.Simulate(req)
	if fe, ok := giftFieldError(err); ok {
		apierr.BadRequest(w, "", fe)
		return
	}
	if err != nil {
		apierr.BadRequest(w, "Canal de venta no disponible")
		return
	}
	// The buyer's company may pay only some ways: the page hides the rest.
	if c := comp; c != nil && len(c.AllowedPayments) > 0 {
		for _, k := range []string{"card", "oxxo", "spei"} {
			if !c.AllowsPayment(k) && !slices.Contains(res.HiddenPayments, k) {
				res.HiddenPayments = append(res.HiddenPayments, k)
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) shopCheckout(w http.ResponseWriter, r *http.Request) {
	t, release, ok := s.publicTenant(w, r, checkoutLimiter)
	if !ok {
		return
	}
	defer release()
	if !t.Site().Settings.Checkout.GuestCheckout {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "account_required", Detail: "Esta tienda solo vende a clientes con cuenta"})
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 200 {
		apierr.BadRequest(w, "Falta Idempotency-Key")
		return
	}
	var req orders.CheckoutRequest
	if !decode(w, r, &req) {
		return
	}
	req.ClientIP = s.clientIP(r)
	req.Lang = pageLang(w)
	// A shopper gets company price and credit only from their signed-in account.
	req.CompanyID = ""
	if c, email := s.shopperCompany(r); c != nil {
		req.Company, req.Customer.Email = c, email
	}
	// Saved cards belong to the signed-in customer only.
	req.SaveFor, req.Saved = "", nil
	if req.SaveCard || req.SavedCard != "" {
		cid, _ := s.shopper(r)
		switch {
		case cid == "" && req.SavedCard != "":
			apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Detail: "Entra a tu cuenta para pagar con tu tarjeta guardada"})
			return
		case cid == "":
			req.SaveCard = false
		case req.SavedCard != "":
			tok, err := s.Orders.CardToken(r.Context(), r.PathValue("tenant"), cid, req.SavedCard)
			if err != nil {
				apierr.Write(w, apierr.Problem{Status: http.StatusBadRequest, Code: "card_not_found", Detail: "Esa tarjeta ya no está guardada; elige otra"})
				return
			}
			req.Saved, req.SaveCard, req.PaymentMethod = tok, false, "pm_saved"
		default:
			req.SaveFor = cid
		}
	}
	errs := validateItems(req.Channel, req.Items)
	if !shopChannel(t, req.Channel) {
		errs = append(errs, apierr.FieldError{Field: "channel", Code: "invalid", Message: "Canal de venta no disponible en la tienda"})
	}
	if !strings.Contains(req.Customer.Email, "@") || len(req.Customer.Email) > 200 {
		errs = append(errs, apierr.FieldError{Field: "customer.email", Code: "invalid", Message: "Correo inválido"})
	}
	if strings.TrimSpace(req.Customer.Name) == "" || len(req.Customer.Name) > 200 || len(req.Customer.Address) > 500 {
		errs = append(errs, apierr.FieldError{Field: "customer.name", Code: "invalid", Message: "Revisa tu nombre y dirección"})
	}
	if _, voucher := payments.VoucherMethods[req.PaymentMethod]; !voucher && !strings.HasPrefix(req.PaymentMethod, "pm_") && len(req.GiftCards) == 0 && req.PaymentMethod != "credit" {
		errs = append(errs, apierr.FieldError{Field: "paymentMethod", Code: "invalid", Message: "Método de pago inválido"})
	}
	errs = append(errs, validateCartExtras(&cart.Request{Coupons: req.Coupons})...)
	if fe := s.registryCheckout(r.Context(), r.PathValue("tenant"), &req); fe != nil {
		errs = append(errs, *fe)
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "La compra tiene errores", errs...)
		return
	}
	tenant := r.PathValue("tenant")
	// A representative shopping for the buyer is the actor of the order.
	actor, acting := "storefront:web", s.shopperActing(r)
	if acting != nil {
		actor = "member:" + acting.MemberID
	}
	res, err := s.Orders.Checkout(r.Context(), tenant, actor, key, req)
	if err == nil && acting != nil && res.Status == http.StatusCreated {
		var o struct {
			ID         string `json:"id"`
			CustomerID string `json:"customerId"`
		}
		if json.Unmarshal(res.Body, &o) == nil && o.ID != "" {
			s.repOrderAudit(r, acting, o.ID, o.CustomerID)
			_, _ = s.Orders.AddNote(r.Context(), tenant, "member:"+acting.MemberID, acting.Name, o.ID, "internal", "Pedido hecho por el representante "+acting.Name+" en nombre del comprador.")
		}
	}
	switch {
	case errors.Is(err, orders.ErrIdempotencyReuse):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Detail: "Esta compra ya se envió con otro contenido"})
	case errors.Is(err, orders.ErrInProgress):
		w.Header().Set("Retry-After", "2")
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_progress", Detail: "Tu compra se está procesando"})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		writeJSONRaw(w, res.Status, res.Body, res.Status >= 400)
	}
}

// shopConfig tells the browser how to collect the payment (publishable data only).
func (s *Server) shopConfig(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if _, ok := s.Proj.Tenant(tenant); !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	out := map[string]any{"mode": "sandbox", "currency": "MXN"}
	if s.Payments != nil && s.Payments.Sandbox == nil {
		a, err := s.DB.GetPaymentAccount(r.Context(), tenant)
		switch {
		case errors.Is(err, store.ErrNotFound):
			out["mode"] = "unavailable"
		case err != nil:
			internalError(w, s.Log, err)
			return
		case a.Mode == "own_keys":
			out["mode"], out["publishableKey"] = "stripe", a.PublishableKey
		default:
			out["mode"], out["publishableKey"], out["stripeAccount"] = "stripe", s.Payments.PlatformPublishableKey, a.AccountID
		}
	}
	s.extendShopConfig(tenant, out)
	if s.Orders != nil {
		if inst, _, err := s.Orders.PaymentRules(r.Context(), tenant); err == nil && inst.Enabled {
			out["installments"] = inst
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	writeJSONRaw(w, status, b, false)
}

// shopChannel reports whether shoppers may buy through a channel: the main
// one or a market the store offers. Wholesale or private channels are only
// reachable with a key of the store.
func shopChannel(t interface {
	Markets(string) []cart.Channel
}, ch string) bool {
	if ch == "" || ch == mainChannel {
		return true
	}
	return marketParam(t, ch) == ch
}

// shopPrices answers the price of products in a market, so pages published
// once (the designer's pages) show the right currency without re-publishing.
func (s *Server) shopPrices(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(r.PathValue("tenant"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ch := r.URL.Query().Get("channel")
	if !shopChannel(t, ch) {
		apierr.BadRequest(w, "Canal de venta no disponible")
		return
	}
	if ch == "" {
		ch = mainChannel
	}
	out := map[string]map[string]string{}
	for i, h := range strings.Split(r.URL.Query().Get("handles"), ",") {
		if i >= 60 {
			break
		}
		p, ok := t.Catalog.ByHandle(strings.TrimSpace(h))
		if !ok {
			continue
		}
		c := cardOf(t, ch, p)
		out[p.Handle] = map[string]string{"price": c.Price, "compareAt": c.CompareAt}
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{"channel": ch, "items": out})
}
