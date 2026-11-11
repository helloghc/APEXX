package api

import (
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
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
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

//go:embed shop/*.tmpl shop/*.css shop/*.js shop/fonts/*.woff2
var shopFS embed.FS

var shopPages = func() map[string]*template.Template {
	m := map[string]*template.Template{}
	for _, p := range []string{"catalog", "product", "cart", "checkout", "thanks"} {
		m[p] = template.Must(template.ParseFS(shopFS, "shop/layout.tmpl", "shop/"+p+".tmpl"))
	}
	return m
}()

// shopCSP allows only our own assets plus Stripe for the payment form.
const shopCSP = "default-src 'self'; img-src * data:; style-src 'self' 'unsafe-inline'; font-src 'self'; script-src 'self' https://js.stripe.com; " +
	"frame-src https://js.stripe.com https://hooks.stripe.com; connect-src 'self' https://api.stripe.com; base-uri 'none'; form-action 'self'"

type pageData struct {
	Title, Description, Store, Tenant, Base, Channel, Query string
	Canonical                                               string // path of the page without filters, for search engines
	NoIndex                                                 bool   // filtered or paginated views are not indexed
	Chrome                                                  builder.Chrome
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
	ch := "mx"
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
	return t, pageData{Store: tenant, Tenant: tenant, Base: "/s/" + tenant + "/" + r.PathValue("site"), Channel: ch, Chrome: st.LiveChrome()}, true
}

func (s *Server) renderShop(w http.ResponseWriter, page string, data any, cache bool) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", shopCSP)
	if cache {
		h.Set("Cache-Control", "public, max-age=0, s-maxage=30, stale-while-revalidate=60")
	} else {
		h.Set("Cache-Control", "no-store")
	}
	if err := shopPages[page].ExecuteTemplate(w, "layout", data); err != nil {
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
	res, err := t.Simulate(req)
	if err != nil {
		apierr.BadRequest(w, "Canal de venta no disponible")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) shopCheckout(w http.ResponseWriter, r *http.Request) {
	_, release, ok := s.publicTenant(w, r, checkoutLimiter)
	if !ok {
		return
	}
	defer release()
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 200 {
		apierr.BadRequest(w, "Falta Idempotency-Key")
		return
	}
	var req orders.CheckoutRequest
	if !decode(w, r, &req) {
		return
	}
	errs := validateItems(req.Channel, req.Items)
	if !strings.Contains(req.Customer.Email, "@") || len(req.Customer.Email) > 200 {
		errs = append(errs, apierr.FieldError{Field: "customer.email", Code: "invalid", Message: "Correo inválido"})
	}
	if strings.TrimSpace(req.Customer.Name) == "" || len(req.Customer.Name) > 200 || len(req.Customer.Address) > 500 {
		errs = append(errs, apierr.FieldError{Field: "customer.name", Code: "invalid", Message: "Revisa tu nombre y dirección"})
	}
	if !strings.HasPrefix(req.PaymentMethod, "pm_") {
		errs = append(errs, apierr.FieldError{Field: "paymentMethod", Code: "invalid", Message: "Método de pago inválido"})
	}
	errs = append(errs, validateCartExtras(&cart.Request{Coupons: req.Coupons})...)
	if len(errs) > 0 {
		apierr.BadRequest(w, "La compra tiene errores", errs...)
		return
	}
	tenant := r.PathValue("tenant")
	res, err := s.Orders.Checkout(r.Context(), tenant, "storefront:web", key, req)
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
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	writeJSONRaw(w, status, b, false)
}
