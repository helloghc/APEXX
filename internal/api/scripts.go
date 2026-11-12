package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
)

// Script manager with consent: the store's third-party scripts (analytics,
// ads pixels, chat) load only after the shopper agrees to their category,
// never on checkout, payment or account pages, and only from the origins the
// store declared (the page's security policy allows exactly those).

// Script categories.
var scriptCategories = []string{"necessary", "analytics", "marketing"}

// StoreScript is one third-party script.
type StoreScript struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Src      string `json:"src"`
	Active   bool   `json:"active"`
}

// ScriptsConfig is the store's scripts and consent banner.
type ScriptsConfig struct {
	Items      []StoreScript `json:"items"`
	BannerText string        `json:"bannerText"`
}

type scriptsCache struct {
	mu sync.Mutex
	m  map[string]scriptsEntry
}

type scriptsEntry struct {
	cfg     ScriptsConfig
	origins string // extra origins for the security policy
	until   time.Time
}

func (s *Server) routesScripts(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/scripts", s.authed("builder", auth.Read, s.getScripts))
	handle("PUT /v1/settings/scripts", s.authed("builder", auth.Admin, s.putScripts))
	handle("GET /s/{tenant}/api/scripts", s.shopScripts)
}

func (s *Server) scriptsOf(tenant string) scriptsEntry {
	s.scripts.mu.Lock()
	e, ok := s.scripts.m[tenant]
	s.scripts.mu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e
	}
	var c ScriptsConfig
	_, _ = s.DB.GetDoc(context.Background(), tenant, "store_scripts", "config", &c)
	var origins []string
	for _, it := range c.Items {
		if u, err := url.Parse(it.Src); it.Active && err == nil && u.Scheme == "https" && u.Host != "" {
			o := "https://" + u.Host
			if !slices.Contains(origins, o) {
				origins = append(origins, o)
			}
		}
	}
	e = scriptsEntry{cfg: c, origins: strings.Join(origins, " "), until: time.Now().Add(30 * time.Second)}
	s.scripts.mu.Lock()
	if s.scripts.m == nil {
		s.scripts.m = map[string]scriptsEntry{}
	}
	s.scripts.m[tenant] = e
	s.scripts.mu.Unlock()
	return e
}

// shopCSPFor is the storefront security policy of a page: the base policy,
// plus the declared script origins on pages that may load them.
func (s *Server) shopCSPFor(tenant string, thirdParty bool) string {
	if !thirdParty || tenant == "" {
		return shopCSP
	}
	o := s.scriptsOf(tenant).origins
	if o == "" {
		return shopCSP
	}
	csp := strings.Replace(shopCSP, "script-src 'self' https://js.stripe.com;", "script-src 'self' https://js.stripe.com "+o+";", 1)
	csp = strings.Replace(csp, "connect-src 'self' https://api.stripe.com;", "connect-src 'self' https://api.stripe.com "+o+";", 1)
	return strings.Replace(csp, "frame-src https://js.stripe.com https://hooks.stripe.com;", "frame-src https://js.stripe.com https://hooks.stripe.com "+o+";", 1)
}

func (s *Server) getScripts(w http.ResponseWriter, r *http.Request) {
	var c ScriptsConfig
	if _, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "store_scripts", "config", &c); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if c.Items == nil {
		c.Items = []StoreScript{}
	}
	httpx.JSON(w, r, http.StatusOK, c)
}

func (s *Server) putScripts(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var c ScriptsConfig
	if !decode(w, r, &c) {
		return
	}
	var errs []apierr.FieldError
	if len(c.Items) > 30 {
		errs = append(errs, apierr.FieldError{Field: "items", Code: "too_many", Message: "Hasta 30 scripts"})
	}
	c.BannerText = strings.TrimSpace(c.BannerText)
	if len([]rune(c.BannerText)) > 300 {
		errs = append(errs, apierr.FieldError{Field: "bannerText", Code: "too_long", Message: "Texto de hasta 300 caracteres"})
	}
	ids := map[string]bool{}
	for i := range c.Items {
		it := &c.Items[i]
		f := "items[" + strconv.Itoa(i) + "]"
		it.Name, it.Src = strings.TrimSpace(it.Name), strings.TrimSpace(it.Src)
		if it.ID == "" || ids[it.ID] {
			it.ID = newRandomID("scr_")
		}
		ids[it.ID] = true
		if it.Name == "" || len([]rune(it.Name)) > 60 {
			errs = append(errs, apierr.FieldError{Field: f + ".name", Code: "invalid", Message: "Escribe un nombre de hasta 60 caracteres"})
		}
		if !slices.Contains(scriptCategories, it.Category) {
			errs = append(errs, apierr.FieldError{Field: f + ".category", Code: "invalid", Message: "Categoría: necesaria, analítica o publicidad"})
		}
		u, err := url.Parse(it.Src)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(it.Src) > 500 || (s.Webhooks != nil && !s.Webhooks.AllowPrivate && s.Webhooks.CheckURL(it.Src) != nil) {
			errs = append(errs, apierr.FieldError{Field: f + ".src", Code: "invalid", Message: "La dirección del script empieza con https:// y es pública"})
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los scripts", errs...)
		return
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "store_scripts", "config", "", c); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.scripts.mu.Lock()
	delete(s.scripts.m, pr.Tenant)
	s.scripts.mu.Unlock()
	httpx.JSON(w, r, http.StatusOK, c)
}

// shopScripts tells the storefront which scripts exist, by category, so it
// asks for consent and loads only what the shopper accepted.
func (s *Server) shopScripts(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) {
		return
	}
	e := s.scriptsOf(r.PathValue("tenant"))
	items := []map[string]string{}
	for _, it := range e.cfg.Items {
		if it.Active {
			items = append(items, map[string]string{"id": it.ID, "category": it.Category, "src": it.Src})
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "bannerText": e.cfg.BannerText})
}
