// Package sitecfg holds what a merchant configures about the store itself:
// identity and legal data, checkout rules, policy pages, navigation menus,
// URL redirects and custom domains. Types validate themselves; the storefront
// reads them from the in-memory projection.
package sitecfg

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/logistics"
)

func fe(field, code, msg string) apierr.FieldError {
	return apierr.FieldError{Field: field, Code: code, Message: msg}
}

// Checkout holds the rules of the purchase form.
type Checkout struct {
	GuestCheckout bool   `json:"guestCheckout"` // buy without an account
	PhoneRequired bool   `json:"phoneRequired"`
	NotesField    bool   `json:"notesField"`    // free-text note for the store
	TermsCheckbox bool   `json:"termsCheckbox"` // must accept the terms policy
	TermsPolicy   string `json:"termsPolicy"`   // slug of the policy linked from the checkbox
}

// Settings is the configuration document of a store.
type Settings struct {
	Name              string            `json:"name"`
	LegalName         string            `json:"legalName"`
	RFC               string            `json:"rfc"`
	Email             string            `json:"email"`
	Phone             string            `json:"phone"`
	Address           logistics.Address `json:"address"`
	LogoURL           string            `json:"logoUrl"` // set by the logo upload
	Social            map[string]string `json:"social"`
	Checkout          Checkout          `json:"checkout"`
	OrderPrefix       string            `json:"orderPrefix"`
	Timezone          string            `json:"timezone"`
	LowStockThreshold int64             `json:"lowStockThreshold"`
	Robots            string            `json:"robots"` // extra robots.txt rules
}

// Default returns the settings of a store that has not saved any.
func Default(tenant string) Settings {
	return Settings{
		Name: tenant, Timezone: "America/Mexico_City", LowStockThreshold: 5, Social: map[string]string{},
		Address:  logistics.Address{Country: "MX"},
		Checkout: Checkout{GuestCheckout: true, TermsPolicy: "terminos"},
	}
}

// SocialNetworks are the networks a store can link, in display order.
var SocialNetworks = []string{"facebook", "instagram", "tiktok", "x", "youtube", "whatsapp"}

var (
	rfcRe    = regexp.MustCompile(`^[A-ZÑ&]{3,4}[0-9]{6}[A-Z0-9]{3}$`)
	emailRe  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	prefixRe = regexp.MustCompile(`^[A-Z0-9-]{0,8}$`)
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// ValidSlug reports whether s is a URL slug.
func ValidSlug(s string) bool { return len(s) <= 60 && slugRe.MatchString(s) }

// Validate normalizes and checks the settings.
func (s *Settings) Validate() []apierr.FieldError {
	var errs []apierr.FieldError
	s.Name, s.LegalName = strings.TrimSpace(s.Name), strings.TrimSpace(s.LegalName)
	s.RFC = strings.ToUpper(strings.TrimSpace(s.RFC))
	s.Email, s.Phone = strings.TrimSpace(s.Email), strings.TrimSpace(s.Phone)
	s.OrderPrefix = strings.ToUpper(strings.TrimSpace(s.OrderPrefix))
	if s.Name == "" {
		errs = append(errs, fe("name", "required", "Escribe el nombre de la tienda"))
	}
	if len([]rune(s.Name)) > 80 || len([]rune(s.LegalName)) > 160 {
		errs = append(errs, fe("name", "too_long", "El nombre es demasiado largo"))
	}
	if s.RFC != "" && !rfcRe.MatchString(s.RFC) {
		errs = append(errs, fe("rfc", "invalid", "El RFC debe tener 12 caracteres (empresa) o 13 (persona física), por ejemplo XAXX010101000"))
	}
	if s.Email != "" && (!emailRe.MatchString(s.Email) || len(s.Email) > 200) {
		errs = append(errs, fe("email", "invalid", "Escribe un correo válido"))
	}
	if s.Phone != "" && !logistics.ValidPhone(s.Phone) {
		errs = append(errs, fe("phone", "invalid", "Teléfono no válido"))
	}
	errs = append(errs, s.Address.Validate("address.", false)...)
	if !prefixRe.MatchString(s.OrderPrefix) {
		errs = append(errs, fe("orderPrefix", "invalid", "El prefijo admite hasta 8 letras mayúsculas, números o guiones"))
	}
	if s.Timezone == "" {
		s.Timezone = "America/Mexico_City"
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		errs = append(errs, fe("timezone", "invalid", "Zona horaria desconocida"))
	}
	if s.LowStockThreshold < 0 || s.LowStockThreshold > 1_000_000 {
		errs = append(errs, fe("lowStockThreshold", "out_of_range", "El umbral de stock bajo debe ser 0 o más"))
	}
	clean := map[string]string{}
	for k, v := range s.Social {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		known := false
		for _, n := range SocialNetworks {
			known = known || n == k
		}
		u, err := url.Parse(v)
		if !known || err != nil || u.Scheme != "https" || u.Host == "" || len(v) > 300 {
			errs = append(errs, fe("social."+k, "invalid", "Escribe el enlace completo, por ejemplo https://instagram.com/mitienda"))
			continue
		}
		clean[k] = v
	}
	s.Social = clean
	if len(s.Robots) > 4000 {
		errs = append(errs, fe("robots", "too_long", "Las reglas de robots.txt admiten hasta 4,000 caracteres"))
	}
	for i, line := range strings.Split(s.Robots, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, _, ok := strings.Cut(line, ":")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "user-agent", "allow", "disallow", "crawl-delay":
			if ok {
				continue
			}
		}
		errs = append(errs, fe("robots", "invalid", fmt.Sprintf("Línea %d: usa User-agent, Allow, Disallow o Crawl-delay (el Sitemap se agrega solo)", i+1)))
		break
	}
	if s.Checkout.TermsPolicy == "" {
		s.Checkout.TermsPolicy = "terminos"
	}
	if !ValidSlug(s.Checkout.TermsPolicy) {
		errs = append(errs, fe("checkout.termsPolicy", "invalid", "Elige la política de términos"))
	}
	return errs
}

// Location returns the time zone of the store.
func (s Settings) Location() *time.Location {
	if l, err := time.LoadLocation(s.Timezone); err == nil && s.Timezone != "" {
		return l
	}
	return logistics.MexicoCity
}

// OrderNumber renders the number shown to the shopper.
func (s Settings) OrderNumber(n int64) string {
	if s.OrderPrefix == "" {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%s%d", s.OrderPrefix, n)
}

// ---------- policies ----------

// Policy is an editable page: returns, privacy, terms.
type Policy struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Body      string    `json:"body"` // sanitized HTML
	Published bool      `json:"published"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// StandardPolicies are the pages every Mexican store needs.
var StandardPolicies = []struct{ Slug, Title string }{
	{"devoluciones", "Cambios y devoluciones"},
	{"privacidad", "Aviso de privacidad"},
	{"terminos", "Términos y condiciones"},
	{"envios", "Envíos y entregas"},
}

// Validate sanitizes the body and checks the policy.
func (p *Policy) Validate() []apierr.FieldError {
	var errs []apierr.FieldError
	p.Title = strings.TrimSpace(p.Title)
	if !ValidSlug(p.Slug) {
		errs = append(errs, fe("slug", "invalid", "La dirección usa minúsculas, números y guiones (por ejemplo devoluciones)"))
	}
	if p.Title == "" || len([]rune(p.Title)) > 120 {
		errs = append(errs, fe("title", "required", "Escribe un título (hasta 120 caracteres)"))
	}
	if len(p.Body) > 200_000 {
		errs = append(errs, fe("body", "too_long", "El texto admite hasta 200,000 caracteres"))
		return errs
	}
	p.Body = SanitizeHTML(p.Body)
	if p.Published && strings.TrimSpace(PlainText(p.Body)) == "" {
		errs = append(errs, fe("body", "required", "Escribe el contenido antes de publicar"))
	}
	return errs
}

// ---------- menus ----------

// MenuItem is a link of a navigation menu; items can nest.
type MenuItem struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Type     string     `json:"type"` // home | catalog | category | product | page | policy | url
	Ref      string     `json:"ref,omitempty"`
	URL      string     `json:"url,omitempty"` // type url
	NewTab   bool       `json:"newTab,omitempty"`
	Children []MenuItem `json:"children"`
}

// Menu is a named list of links (header, footer or custom).
type Menu struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Items     []MenuItem `json:"items"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// MaxMenuDepth is how deep links can nest.
const MaxMenuDepth = 3

// Validate checks a menu.
func (m *Menu) Validate() []apierr.FieldError {
	var errs []apierr.FieldError
	m.Name = strings.TrimSpace(m.Name)
	if !ValidSlug(m.ID) {
		errs = append(errs, fe("id", "invalid", "El identificador usa minúsculas, números y guiones"))
	}
	if m.Name == "" || len([]rune(m.Name)) > 60 {
		errs = append(errs, fe("name", "required", "Escribe un nombre (hasta 60 caracteres)"))
	}
	count := 0
	seen := map[string]bool{}
	var walk func(items []MenuItem, path string, depth int)
	walk = func(items []MenuItem, path string, depth int) {
		for i := range items {
			it := &items[i]
			f := fmt.Sprintf("%s[%d]", path, i)
			count++
			it.Label, it.Ref, it.URL = strings.TrimSpace(it.Label), strings.TrimSpace(it.Ref), strings.TrimSpace(it.URL)
			if it.ID == "" || seen[it.ID] {
				it.ID = fmt.Sprintf("m%d", count)
				for seen[it.ID] {
					it.ID += "x"
				}
			}
			seen[it.ID] = true
			if it.Label == "" || len([]rune(it.Label)) > 60 {
				errs = append(errs, fe(f+".label", "required", "Cada enlace necesita un texto (hasta 60 caracteres)"))
			}
			switch it.Type {
			case "home", "catalog":
				it.Ref, it.URL = "", ""
			case "category":
				if it.Ref == "" || len(it.Ref) > 80 {
					errs = append(errs, fe(f+".ref", "required", "Escribe la categoría"))
				}
			case "product", "page", "policy":
				if !ValidSlug(it.Ref) {
					errs = append(errs, fe(f+".ref", "invalid", "Elige a dónde lleva el enlace"))
				}
			case "url":
				if !validTarget(it.URL) {
					errs = append(errs, fe(f+".url", "invalid", "Escribe una URL https completa o una ruta que empiece con /"))
				}
			default:
				errs = append(errs, fe(f+".type", "invalid", "Tipo de enlace desconocido"))
			}
			if it.Children == nil {
				it.Children = []MenuItem{}
			}
			if len(it.Children) > 0 && depth >= MaxMenuDepth {
				errs = append(errs, fe(f+".children", "too_deep", fmt.Sprintf("Los menús admiten hasta %d niveles", MaxMenuDepth)))
				continue
			}
			walk(it.Children, f+".children", depth+1)
		}
	}
	if m.Items == nil {
		m.Items = []MenuItem{}
	}
	walk(m.Items, "items", 1)
	if count > 200 {
		errs = append(errs, fe("items", "too_many", "Un menú admite hasta 200 enlaces"))
	}
	return errs
}

// Link is a resolved menu item, ready to render.
type Link struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	NewTab   bool   `json:"newTab,omitempty"`
	Children []Link `json:"children,omitempty"`
}

// Resolve turns the items into links under base ("/s/tienda/main" or "" on a
// custom domain).
func Resolve(items []MenuItem, base string) []Link { return ResolveWith(items, base, nil) }

// ResolveWith is Resolve that leaves out links to a category or product the
// store no longer has (exists says; nil: all exist), so a shopper never lands
// on a missing page from the menu. Their sub-links take their place.
func ResolveWith(items []MenuItem, base string, exists func(kind, ref string) bool) []Link {
	out := make([]Link, 0, len(items))
	for _, it := range items {
		children := ResolveWith(it.Children, base, exists)
		if exists != nil && (it.Type == "category" && ValidSlug(it.Ref) || it.Type == "product") && !exists(it.Type, it.Ref) {
			out = append(out, children...)
			continue
		}
		l := Link{Label: it.Label, NewTab: it.NewTab, Children: children}
		switch it.Type {
		case "home":
			l.URL = base + "/index"
		case "catalog":
			l.URL = base + "/c"
		case "category":
			// A handle leads to the category page; free text (older menus) searches.
			if ValidSlug(it.Ref) {
				l.URL = base + "/c/" + it.Ref
			} else {
				l.URL = base + "/c?q=" + url.QueryEscape(it.Ref)
			}
		case "product":
			l.URL = base + "/p/" + it.Ref
		case "page":
			l.URL = base + "/" + it.Ref
		case "policy":
			l.URL = base + "/politicas/" + it.Ref
		default:
			l.URL = it.URL
			if strings.HasPrefix(it.URL, "/") {
				l.URL = base + it.URL
			}
		}
		if len(l.Children) == 0 {
			l.Children = nil
		}
		out = append(out, l)
	}
	return out
}

// validTarget accepts an absolute https URL or a site path.
func validTarget(t string) bool {
	if len(t) == 0 || len(t) > 500 || strings.ContainsAny(t, " \t\r\n<>\"") {
		return false
	}
	if strings.HasPrefix(t, "/") {
		return !strings.HasPrefix(t, "//")
	}
	u, err := url.Parse(t)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// ---------- redirects ----------

// Redirect sends an old path to a new place.
type Redirect struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`   // path inside the site, e.g. /tenis-rojos
	Target    string    `json:"target"` // site path or absolute URL
	Code      int       `json:"code"`   // 301 | 302
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
}

// reserved paths can never be redirected: it would break buying.
var reserved = []string{"/cart", "/checkout", "/gracias", "/sitemap.xml", "/robots.txt"}

// NormalizePath cleans a site path: leading slash, no trailing slash, no query.
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	if p == "" {
		p = "/"
	}
	return p
}

// Validate checks a redirect on its own (loops are checked against the set).
func (r *Redirect) Validate() []apierr.FieldError {
	var errs []apierr.FieldError
	raw := strings.TrimSpace(r.From)
	if strings.Contains(raw, "://") {
		errs = append(errs, fe("from", "invalid", "Escribe solo la ruta dentro de tu tienda, por ejemplo /tenis-rojos"))
	}
	r.From, r.Target = NormalizePath(raw), strings.TrimSpace(r.Target)
	if r.Code == 0 {
		r.Code = 301
	}
	if r.Code != 301 && r.Code != 302 {
		errs = append(errs, fe("code", "invalid", "Tipo de redirección: 301 (permanente) o 302 (temporal)"))
	}
	if r.From == "/" || len(r.From) > 300 || strings.ContainsAny(r.From, " \t\r\n<>\"") {
		errs = append(errs, fe("from", "invalid", "Escribe la ruta anterior, por ejemplo /tenis-rojos"))
	}
	for _, p := range reserved {
		if r.From == p {
			errs = append(errs, fe("from", "reserved", "La ruta "+p+" es parte de la compra y no se puede redirigir"))
		}
	}
	if !validTarget(r.Target) {
		errs = append(errs, fe("target", "invalid", "Escribe el destino: una ruta como /p/tenis-rojos-2 o una URL https completa"))
	} else if strings.HasPrefix(r.Target, "/") && NormalizePath(r.Target) == r.From {
		errs = append(errs, fe("target", "loop", "El destino es la misma ruta de origen"))
	}
	return errs
}

// Loop reports whether following target through set comes back to from.
func Loop(set map[string]Redirect, from, target string) bool {
	cur := target
	for i := 0; i < 20; i++ {
		if !strings.HasPrefix(cur, "/") {
			return false
		}
		p := NormalizePath(cur)
		if p == from {
			return true
		}
		next, ok := set[p]
		if !ok || !next.Active {
			return false
		}
		cur = next.Target
	}
	return true
}

// ---------- domains ----------

// Domain is a custom hostname of a store.
type Domain struct {
	ID            string     `json:"id"`
	Hostname      string     `json:"hostname"`
	Site          string     `json:"site"`
	Status        string     `json:"status"` // pending | verified | failed
	Token         string     `json:"token"`
	Primary       bool       `json:"primary"`
	LastError     string     `json:"lastError,omitempty"`
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
	VerifiedAt    *time.Time `json:"verifiedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	Market        string     `json:"market"` // "" = the site's market
}

// VerifyHost is where the TXT record lives.
func (d Domain) VerifyHost() string { return "_apexx-verify." + d.Hostname }

// VerifyValue is the expected TXT value.
func (d Domain) VerifyValue() string { return "apexx-verify=" + d.Token }

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}$`)

// NormalizeHost lowercases a hostname and strips scheme, port, path and dot.
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// ValidHost reports whether h is a public DNS name a store can use.
func ValidHost(h string) bool {
	if len(h) > 253 || !hostRe.MatchString(h) || net.ParseIP(h) != nil {
		return false
	}
	for _, bad := range []string{".local", ".localhost", ".internal", ".test", ".invalid", ".example"} {
		if strings.HasSuffix(h, bad) {
			return false
		}
	}
	return true
}
