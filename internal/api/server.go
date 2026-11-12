// Package api is the HTTP surface of Apexx: authenticated, tenant-isolated,
// with permissions per module and level, resilient to per-tenant overload.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/reports"
	"github.com/helloghc/apexx/internal/resilience"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/webhooks"
)

// Server holds every dependency of the HTTP layer.
type Server struct {
	DB            *store.DB
	Proj          *projection.Projector
	Auth          *auth.Service
	Orders        *orders.Service
	Keys          *crypto.Keyring
	Library       *builder.Library
	Log           *slog.Logger
	Admin         http.Handler // embedded builder editor (optional)
	Bulkhead      *resilience.Bulkhead
	Payments      *PaymentAccounts
	Webhooks      *webhooks.Service
	Customers     *customers.Service
	Notify        *notify.Service
	Reports       *reports.Service // analytics, reports and operations (built from DB and Keys when nil)
	SecureCookies bool
	TrustProxy    bool          // honour X-Forwarded-For (only behind our own proxy)
	MetricsToken  string        // bearer token for /metrics; empty = loopback/private callers only
	Media         media.Storage // media library files; nil = local disk under the system temp dir

	sitesMu sync.Mutex
	sites   map[string]*builder.Site // tenant/site -> site

	loginMu   sync.Mutex
	loginHits map[string][]time.Time

	metrics *metrics

	uploadSlots   chan struct{} // bounds concurrent image processing
	engage        engageState   // search terms and review cache
	patterns      []string      // every registered route, for the access audit
	eventsLimiter *shopLimiter  // analytics beacon: per store and address
}

// Routes builds the HTTP handler.
func (s *Server) Routes() http.Handler {
	s.sites = map[string]*builder.Site{}
	s.loginHits = map[string][]time.Time{}
	s.metrics = newMetrics()
	if s.Bulkhead == nil {
		s.Bulkhead = resilience.NewBulkhead(256)
	}
	// Every node drops its cached copy of a site when any node changes it.
	if s.Proj != nil {
		evict := func(tenant, site string) {
			s.sitesMu.Lock()
			delete(s.sites, tenant+"/"+site)
			s.sitesMu.Unlock()
		}
		s.Proj.On("site.released", evict)
		s.Proj.On("site.draft", evict)
		s.Proj.SetLiveRates(s.LiveRate)
		s.Proj.SetExternalTax(s.ExternalTax)
	}
	mux := http.NewServeMux()
	s.patterns = s.patterns[:0]
	handle := func(pattern string, h http.HandlerFunc) {
		s.patterns = append(s.patterns, pattern)
		mux.Handle(pattern, s.metrics.wrap(pattern, h))
	}

	// Health: liveness says the process runs; readiness says it can serve.
	handle("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	handle("GET /readyz", s.ready)
	handle("GET /metrics", s.metricsGuard(s.serveMetrics))

	// Auth and team (unlimited seats, permissions per module).
	handle("POST /v1/auth/login", s.login)
	handle("POST /v1/auth/logout", s.authed("", "", s.logout))
	handle("GET /v1/me", s.authed("", "", s.me))
	handle("GET /v1/team/members", s.authed("team", auth.Read, s.listMembers))
	handle("POST /v1/team/members", s.authed("team", auth.Admin, s.createMember))
	handle("PATCH /v1/team/members/{id}", s.authed("team", auth.Admin, s.updateMember))
	handle("POST /v1/team/api-keys", s.authed("team", auth.Admin, s.createAPIKey))
	handle("GET /v1/team/api-keys", s.authed("team", auth.Read, s.listAPIKeys))
	handle("DELETE /v1/team/api-keys/{id}", s.authed("team", auth.Admin, s.revokeAPIKey))
	handle("POST /v1/team/members/{id}/password", s.authed("team", auth.Admin, s.resetPassword))
	handle("POST /v1/me/password", s.authed("", "", s.changePassword))

	// Catalog, inventory, pricing, channels, promotions.
	s.routesCatalog(handle) // products, categories, brands, collections, attributes, media, import/export
	handle("GET /v1/inventory/{sku}", s.authed("inventory", auth.Read, s.getInventory))
	handle("PUT /v1/inventory/{sku}/{location}", s.authed("inventory", auth.Edit, s.putInventory))
	handle("PUT /v1/prices/{channel}/{sku}", s.authed("pricing", auth.Edit, s.putPrices))
	handle("GET /v1/prices/{channel}/{sku}", s.authed("pricing", auth.Read, s.getPrices))
	handle("GET /v1/channels/{id}", s.authed("settings", auth.Read, s.getChannel))
	handle("PUT /v1/channels/{id}", s.authed("settings", auth.Admin, s.putChannel))
	handle("POST /v1/carts/simulate", s.authed("catalog", auth.Read, s.simulate))

	s.routesPromotions(handle)

	// Orders and payments.
	handle("POST /v1/checkout", s.authed("", "", s.checkout))
	handle("GET /v1/orders/{id}", s.authed("orders", auth.Read, s.getOrder))
	handle("POST /v1/orders/{id}/fulfill", s.authed("orders", auth.Edit, s.fulfillOrder))
	handle("POST /v1/orders/{id}/cancel", s.authed("orders", auth.Edit, s.cancelOrder))
	handle("GET /v1/payments/account", s.authed("payments", auth.Read, s.getPaymentAccount))
	handle("PUT /v1/payments/account", s.authed("payments", auth.Admin, s.putPaymentAccount))
	handle("POST /v1/stripe/webhook/{tenant}", s.stripeWebhook)

	// Builder.
	handle("GET /v1/builder/sections", s.authed("builder", auth.Read, s.sections))
	handle("GET /v1/builder/themes", s.authed("builder", auth.Read, s.themes))
	handle("POST /v1/sites/{site}/theme", s.authed("builder", auth.Edit, s.applyTheme))
	handle("POST /v1/sites/{site}/restore", s.authed("builder", auth.Edit, s.restoreRevision))
	handle("GET /v1/sites/{site}/templates/{handle}/draft", s.authed("builder", auth.Read, s.getDraft))
	handle("PATCH /v1/sites/{site}/templates/{handle}/draft", s.authed("builder", auth.Edit, s.patchDraft))
	handle("GET /v1/sites/{site}/templates/{handle}/preview", s.authed("builder", auth.Read, s.preview))
	handle("POST /v1/sites/{site}/publish", s.authed("builder", auth.Admin, s.publish))
	handle("POST /v1/sites/{site}/rollback", s.authed("builder", auth.Admin, s.rollback))
	handle("GET /v1/sites/{site}/releases", s.authed("builder", auth.Read, s.releases))

	// Connectors: signed webhooks and pull feed.
	handle("GET /v1/events", s.authed("settings", auth.Read, s.feed))
	handle("GET /v1/webhooks", s.authed("settings", auth.Read, s.listWebhooks))
	handle("POST /v1/webhooks", s.authed("settings", auth.Admin, s.createWebhook))
	handle("DELETE /v1/webhooks/{id}", s.authed("settings", auth.Admin, s.disableWebhook))
	handle("GET /v1/webhooks/{id}/deliveries", s.authed("settings", auth.Read, s.webhookDeliveries))
	handle("POST /v1/webhooks/{id}/replay", s.authed("settings", auth.Admin, s.replayWebhook))

	// Operations.
	handle("GET /v1/system/freshness", s.authed("settings", auth.Read, s.freshness))
	handle("GET /v1/audit/verify", s.authed("settings", auth.Admin, s.verifyAudit))

	// Public storefront pages (static HTML per published revision).
	handle("GET /s/{tenant}/{site}/{handle}", s.storefront)
	// Shopper flow: catalog, product, cart, checkout, confirmation.
	handle("GET /s/{tenant}/{site}/c", s.shopCatalog)
	handle("GET /s/{tenant}/{site}/p/{handle}", s.shopProduct)
	handle("GET /s/{tenant}/{site}/cart", s.shopStatic("cart", "Carrito"))
	handle("GET /s/{tenant}/{site}/checkout", s.shopStatic("checkout", "Pago"))
	handle("GET /s/{tenant}/{site}/gracias", s.shopStatic("thanks", "Gracias"))
	handle("GET /s/{tenant}/{site}/pedido-rapido", s.shopStatic("quickorder", "Pedido rápido"))
	handle("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#1d3fae"/><path d="M9 23 16 8l7 15h-4l-3-7-3 7z" fill="#fff"/></svg>`))
	})
	handle("GET /s/assets/{file}", shopAssets)
	handle("GET /s/fonts/{file}", shopFonts)
	handle("GET /s/{tenant}/api/config", s.shopConfig)
	handle("POST /s/{tenant}/api/simulate", s.shopSimulate)
	handle("GET /s/{tenant}/api/prices", s.shopPrices)
	handle("POST /s/{tenant}/api/checkout", s.shopCheckout)

	// Customers, after-sale and notifications (routes_customers.go).
	s.routesCustomers(handle)
	s.routesSettings(handle)
	s.routesReports(handle)
	s.routesEngage(handle)
	s.routesWallet(handle)
	s.routesSubscriptions(handle)
	s.routesAccount(handle)
	s.routesFeeds(handle)
	s.routesMarketplace(handle)
	s.routesB2B(handle)
	s.routesFees(handle)
	s.routesCarriers(handle)
	s.routesReturnsPolicy(handle)
	s.routesPayMethods(handle)
	s.routesShopCards(handle)
	s.routesTaxService(handle)
	s.routesWarehouses(handle)
	s.routesEntities(handle)
	s.routesQuotes(handle)

	if s.Admin != nil {
		mux.Handle("/admin/", http.StripPrefix("/admin", s.Admin))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apierr.NotFound(w, "No existe la ruta "+r.Method+" "+r.URL.Path)
	})
	return resilience.Recover(s.Log, securityHeaders(httpx.Middleware(s.siteRouting(s.localized(mux)))))
}

// metricsGuard exposes /metrics only to the monitoring agent: a bearer token
// when configured, otherwise callers on loopback or private networks.
func (s *Server) metricsGuard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.MetricsToken != "" {
			if r.Header.Get("Authorization") != "Bearer "+s.MetricsToken {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		} else {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if ip == nil || !(ip.IsLoopback() || ip.IsPrivate()) || r.Header.Get("X-Forwarded-For") != "" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		h(w, r)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		if !strings.HasPrefix(r.URL.Path, "/admin") {
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.App.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.Proj.Stats().LoadedAt == "0001-01-01T00:00:00Z" {
		http.Error(w, "projections loading", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ready"))
}

// authed authenticates the request, checks permission (module/level; empty
// means any authenticated principal), applies the per-tenant bulkhead and a
// request deadline.
func (s *Server) authed(module, level string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			token = strings.TrimPrefix(a, "Bearer ")
		} else if c, err := r.Cookie("apexx_session"); err == nil {
			token = c.Value
		}
		p, err := s.Auth.Authenticate(r.Context(), token)
		if err != nil {
			apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Detail: "Inicia sesión o envía una API key válida"})
			return
		}
		if module != "" && !p.Permissions.Allows(module, level) {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden",
				Detail: fmt.Sprintf("Tu acceso a %s no permite esta acción (necesitas %s)", module, level)})
			return
		}
		release, ok := s.Bulkhead.Acquire(p.Tenant)
		if !ok {
			w.Header().Set("Retry-After", "1")
			apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "tenant_busy", Detail: "La tienda tiene demasiadas solicitudes simultáneas; reintenta en un segundo"})
			return
		}
		defer release()
		ctx, cancel := context.WithTimeout(auth.With(r.Context(), p), 15*time.Second)
		defer cancel()
		h(w, r.WithContext(ctx))
	}
}

// staff is authed for management routes: the publishable storefront key is
// refused even when its read permissions would cover the module, because
// these answers are for the people who run the store.
func (s *Server) staff(module, level string, h http.HandlerFunc) http.HandlerFunc {
	return s.authed(module, level, func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Storefront {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Esta información es del equipo de la tienda; una llave pública de tienda no puede leerla"})
			return
		}
		h(w, r)
	})
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.From(r.Context())
	return p
}

// tenantView returns the read models of the caller's tenant.
func (s *Server) tenantView(w http.ResponseWriter, r *http.Request) (*projection.Tenant, bool) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "tenant_loading", Detail: "La tienda se está cargando; reintenta en unos segundos"})
		return nil, false
	}
	return t, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httpx.Decode(r, dst); err != nil {
		apierr.BadRequest(w, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

// unavailable reports whether err means the database could not be reached
// (down, restarting, or the request ran out of time waiting for it).
func unavailable(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) || errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pe *pgconn.PgError // 57P01..57P03: shutting down or starting up; 08xxx: connection lost
	if errors.As(err, &pe) {
		return strings.HasPrefix(pe.Code, "57P") || strings.HasPrefix(pe.Code, "08")
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") || strings.Contains(msg, "conn closed") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset")
}

func internalError(w http.ResponseWriter, log *slog.Logger, err error) {
	if unavailable(err) {
		// Nothing was written: every change is one transaction. Tell the person so.
		if log != nil {
			log.Warn("database unavailable", "err", err)
		}
		w.Header().Set("Retry-After", "5")
		apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "temporarily_unavailable",
			Detail: "El servicio no está disponible por un momento. No se guardó ni se cobró nada; intenta de nuevo en unos segundos"})
		return
	}
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	ref := hex.EncodeToString(id)
	log.Error("request failed", "ref", ref, "err", err)
	apierr.Write(w, apierr.Problem{Status: http.StatusInternalServerError, Code: apierr.CodeInternal, Detail: "Error interno (referencia " + ref + ")"})
}

// ---------- auth handlers ----------

// clientIP returns the caller address. X-Forwarded-For is honoured only when
// the server sits behind our own reverse proxy (Caddy / load balancer), and
// then only its last entry, which the proxy appended; earlier entries are
// client-controlled and could be spoofed to dodge rate limits.
func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			parts := strings.Split(f, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

// loginBlocked reports whether key had 10 failed attempts in the last minute.
func (s *Server) loginBlocked(key string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	hits := s.loginHits[key][:0]
	for _, t := range s.loginHits[key] {
		if now.Sub(t) < time.Minute {
			hits = append(hits, t)
		}
	}
	s.loginHits[key] = hits
	return len(hits) >= 10
}

// loginFailed records a failed attempt; successful logins never count.
func (s *Server) loginFailed(key string) {
	s.loginMu.Lock()
	s.loginHits[key] = append(s.loginHits[key], time.Now())
	s.loginMu.Unlock()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Store    string `json:"store"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"` // two-step verification: app code or a recovery code
	}
	if !decode(w, r, &body) {
		return
	}
	limitKey := s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(body.Email))
	if s.loginBlocked(limitKey) {
		w.Header().Set("Retry-After", "60")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "too_many_attempts", Detail: "Demasiados intentos; espera un minuto"})
		return
	}
	token, m, err := s.Auth.Login(r.Context(), body.Store, body.Email, body.Password, body.Code, s.clientIP(r), r.UserAgent())
	if errors.Is(err, auth.ErrBadCredentials) {
		s.loginFailed(limitKey)
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "invalid_credentials", Detail: "Correo o contraseña incorrectos"})
		return
	}
	if errors.Is(err, auth.ErrTOTPRequired) {
		// Not a failed attempt: the password was right, the second step is missing.
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "totp_required", Detail: "Escribe el código de tu app de verificación"})
		return
	}
	if errors.Is(err, auth.ErrBadCode) {
		s.loginFailed(limitKey)
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "invalid_code", Detail: "El código de verificación no es correcto"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "apexx_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int((12 * time.Hour).Seconds())})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"token": token, "member": m})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("apexx_session"); err == nil {
		_ = s.Auth.Logout(r.Context(), c.Value)
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer aps_") {
		_ = s.Auth.Logout(r.Context(), strings.TrimPrefix(a, "Bearer "))
	}
	http.SetCookie(w, &http.Cookie{Name: "apexx_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	out := struct {
		auth.Principal
		TwoStep bool `json:"twoStep"` // two-step verification is on for this member
	}{Principal: p}
	if p.Kind == "member" {
		_ = s.DB.System.QueryRow(r.Context(), `SELECT totp_enabled_at IS NOT NULL FROM members WHERE tenant_id=$1 AND id=$2`, p.Tenant, p.ID).Scan(&out.TwoStep)
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	ms, err := s.Auth.Members(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ms, "seats": "unlimited", "modules": auth.Modules})
}

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string           `json:"email"`
		Name        string           `json:"name"`
		Password    string           `json:"password"`
		Permissions auth.Permissions `json:"permissions"`
		Owner       bool             `json:"owner"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	if body.Owner && !p.Owner {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Solo un dueño puede crear otro dueño"})
		return
	}
	m, err := s.Auth.CreateMember(r.Context(), p.Tenant, p.Actor(), body.Email, body.Name, body.Password, body.Permissions, body.Owner)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Ya existe un miembro con ese correo"})
			return
		}
		apierr.BadRequest(w, err.Error())
		return
	}
	httpx.JSON(w, r, http.StatusCreated, m)
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Permissions auth.Permissions `json:"permissions"`
		Status      string           `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	m, err := s.Auth.UpdateMember(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.Permissions, body.Status)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		apierr.NotFound(w, "Miembro no encontrado")
	case errors.Is(err, auth.ErrLastOwner):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "last_owner", Detail: "La tienda debe conservar al menos un dueño activo"})
	case err != nil:
		apierr.BadRequest(w, err.Error())
	default:
		httpx.JSON(w, r, http.StatusOK, m)
	}
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch body.Role {
	case "admin", "editor", "publisher", "storefront", "viewer":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "role", Code: "invalid", Message: "role: admin, editor, publisher, storefront o viewer"})
		return
	}
	key, err := s.DB.CreateAPIKey(r.Context(), principal(r).Tenant, body.Name, body.Role)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"key": key, "role": body.Role, "notice": "Guarda la llave ahora; no se volverá a mostrar"})
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	ks, err := s.DB.ListAPIKeys(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ks})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.Kind == "api_key" && p.ID == r.PathValue("id") {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Una llave no puede revocarse a sí misma"})
		return
	}
	err := s.DB.RevokeAPIKey(r.Context(), p.Tenant, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Llave no encontrada o ya revocada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	err := s.Auth.SetPassword(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), "", body.Password)
	s.passwordResult(w, err)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	if p.Kind != "member" {
		apierr.BadRequest(w, "Solo los miembros del equipo tienen contraseña")
		return
	}
	if body.Current == "" {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "current", Code: "required", Message: "Escribe tu contraseña actual"})
		return
	}
	s.passwordResult(w, s.Auth.SetPassword(r.Context(), p.Tenant, p.Actor(), p.ID, body.Current, body.New))
}

func (s *Server) passwordResult(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrBadCredentials):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "invalid_credentials", Detail: "La contraseña actual no es correcta"})
	case errors.Is(err, auth.ErrNotFound):
		apierr.NotFound(w, "Miembro no encontrado")
	case err != nil && strings.Contains(err.Error(), "contraseña"):
		apierr.BadRequest(w, "", apierr.FieldError{Field: "password", Code: "weak", Message: err.Error()})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		w.WriteHeader(http.StatusNoContent) // every session of that member was closed
	}
}

func (s *Server) freshness(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, s.Proj.Stats())
}

func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	broken, err := s.DB.VerifyAudit(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"intact": broken == 0, "firstBrokenSeq": broken})
}

func writeJSONRaw(w http.ResponseWriter, status int, b json.RawMessage, problem bool) {
	if problem {
		w.Header().Set("Content-Type", "application/problem+json")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
