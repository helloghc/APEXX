package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/webhooks"
)

// Checkout rules: declarative rules plus, optionally, the store's app that
// answers each cart. The app has at most 600 ms; if it is slow, fails or
// answers nonsense, the cart goes on with the declarative rules only.

// RulesApp is the store's rules service.
type RulesApp struct {
	Active   bool   `json:"active"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

type checkoutRulesDoc struct {
	Rules []cart.CheckoutRule `json:"rules"`
	App   RulesApp            `json:"app"`
}

var rulesHTTPClient *http.Client // tests

// rulesCache keeps, per server, each store's app settings (30 s) and its
// answers per identical cart (1 minute; failures 30 s).
type rulesCache struct {
	mu      sync.Mutex
	cfg     map[string]rulesCfgEntry
	answers sync.Map // cache key -> rulesAnswer
}

type rulesCfgEntry struct {
	app   RulesApp
	until time.Time
}

type rulesAnswer struct {
	res   cart.RulesResult
	ok    bool
	until time.Time
}

const rulesTimeout = 600 * time.Millisecond

func (s *Server) routesCheckoutRules(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/checkout-rules", s.authed("settings", auth.Read, s.getCheckoutRules))
	handle("PUT /v1/settings/checkout-rules", s.authed("settings", auth.Admin, s.putCheckoutRules))
}

func (s *Server) rulesSecret(tenant string) []byte {
	b, err := s.Keys.Blind(tenant, "rules-signing", "app")
	if err != nil {
		return nil
	}
	return []byte(hex.EncodeToString(b))
}

func (s *Server) getCheckoutRules(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var d checkoutRulesDoc
	if _, err := s.DB.GetDoc(r.Context(), pr.Tenant, "checkout_rules", "config", &d); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if d.Rules == nil {
		d.Rules = []cart.CheckoutRule{}
	}
	out := map[string]any{"rules": d.Rules, "app": d.App, "paymentKinds": cart.PaymentKinds}
	if pr.Permissions.Allows("settings", auth.Admin) {
		out["secret"] = string(s.rulesSecret(pr.Tenant))
		out["contract"] = "POST {endpoint}/rules con JSON {channel, currency, subtotal, postalCode, group, lines:[{sku, qty, unit}]}; responde {hidePayments:[card|oxxo|spei|credit|gift_card], hideShipping:[id de opción], messages:[texto]} en menos de 600 ms. Firma: X-Apexx-Signature = hex(HMAC-SHA256(secreto, timestamp + \".\" + cuerpo))."
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) putCheckoutRules(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in checkoutRulesDoc
	if !decode(w, r, &in) {
		return
	}
	rules, errs := cart.CleanCheckoutRules(in.Rules)
	out := make([]apierr.FieldError, 0, len(errs))
	for _, e := range errs {
		out = append(out, apierr.FieldError{Field: e.Field, Code: e.Code, Message: e.Message})
	}
	in.App.Name, in.App.Endpoint = strings.TrimSpace(in.App.Name), strings.TrimSpace(in.App.Endpoint)
	if in.App.Endpoint != "" {
		if u, err := url.Parse(in.App.Endpoint); err != nil || u.Host == "" || len(in.App.Endpoint) > 300 || (s.Webhooks != nil && s.Webhooks.CheckURL(in.App.Endpoint) != nil) {
			out = append(out, apierr.FieldError{Field: "app.endpoint", Code: "invalid", Message: "Escribe la dirección https pública de la app"})
		}
	}
	if in.App.Active && in.App.Endpoint == "" {
		out = append(out, apierr.FieldError{Field: "app.endpoint", Code: "required", Message: "Para activarla escribe la dirección de la app"})
	}
	if len(out) > 0 {
		apierr.BadRequest(w, "Revisa las reglas", out...)
		return
	}
	in.Rules = rules
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "checkout_rules", "config", store.KeyCheckoutRules, in); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.rules.mu.Lock()
	delete(s.rules.cfg, pr.Tenant)
	s.rules.mu.Unlock()
	httpx.JSON(w, r, http.StatusOK, in)
}

func (s *Server) rulesApp(tenant string) RulesApp {
	s.rules.mu.Lock()
	e, ok := s.rules.cfg[tenant]
	s.rules.mu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e.app
	}
	var d checkoutRulesDoc
	_, _ = s.DB.GetDoc(context.Background(), tenant, "checkout_rules", "config", &d)
	s.rules.mu.Lock()
	if s.rules.cfg == nil {
		s.rules.cfg = map[string]rulesCfgEntry{}
	}
	s.rules.cfg[tenant] = rulesCfgEntry{d.App, time.Now().Add(30 * time.Second)}
	s.rules.mu.Unlock()
	return d.App
}

// ExternalRules asks the store's app about a cart (installed in the projector).
// Answers are kept a minute (failures 30 seconds) per identical cart.
func (s *Server) ExternalRules(tenant string, in cart.RulesInput) (cart.RulesResult, bool) {
	app := s.rulesApp(tenant)
	if !app.Active || app.Endpoint == "" {
		return cart.RulesResult{}, false
	}
	body, _ := json.Marshal(in)
	sum := sha256.Sum256(append([]byte(tenant+"|"+app.Endpoint+"|"), body...))
	key := hex.EncodeToString(sum[:])
	if v, ok := s.rules.answers.Load(key); ok && time.Now().Before(v.(rulesAnswer).until) {
		return v.(rulesAnswer).res, v.(rulesAnswer).ok
	}
	res, ok := s.askRulesApp(tenant, app.Endpoint, body)
	ttl := time.Minute
	if !ok {
		ttl = 30 * time.Second
	}
	s.rules.answers.Store(key, rulesAnswer{res, ok, time.Now().Add(ttl)})
	return res, ok
}

func (s *Server) askRulesApp(tenant, endpoint string, body []byte) (cart.RulesResult, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), rulesTimeout)
	defer cancel()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/rules", bytes.NewReader(body))
	if err != nil {
		return cart.RulesResult{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Apexx-Timestamp", ts)
	req.Header.Set("X-Apexx-Signature", carriers.Sign(s.rulesSecret(tenant), ts, body))
	client := rulesHTTPClient
	if client == nil {
		client = webhooks.Client(s.Webhooks != nil && s.Webhooks.AllowPrivate)
	}
	res, err := client.Do(req)
	if err != nil {
		return cart.RulesResult{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return cart.RulesResult{}, false
	}
	var out cart.RulesResult
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 64<<10)).Decode(&out); err != nil || len(out.HidePayments) > 10 || len(out.HideShipping) > 50 || len(out.Messages) > 5 {
		return cart.RulesResult{}, false
	}
	return out, true
}
