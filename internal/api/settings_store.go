package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/helloghc/apexx/internal/i18n"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/sitecfg"
	"github.com/helloghc/apexx/internal/store"
)

// ---------- sales channels ----------

func (s *Server) channelList(r *http.Request) ([]cart.Channel, error) {
	raw, err := s.DB.Channels(r.Context(), principal(r).Tenant)
	if err != nil {
		return nil, err
	}
	out := make([]cart.Channel, 0, len(raw))
	for id, b := range raw {
		var c cart.Channel
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		c.ID = id
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	chs, err := s.channelList(r)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	c, err := s.DB.LoadLogistics(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": chs, "taxRegions": nonNil(c.TaxRegions), "main": mainChannel})
}

// mainChannel is the channel the storefront sells on.
const mainChannel = "mx"

var (
	channelIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)
	currencyRe  = regexp.MustCompile(`^[A-Z]{3}$`)
	localeRe    = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)
	countryRe   = regexp.MustCompile(`^[A-Z]{2}$`)
)

// validateChannel checks a channel before it is stored.
func (s *Server) validateChannel(r *http.Request, c *cart.Channel) ([]apierr.FieldError, error) {
	var errs []apierr.FieldError
	add := func(f, code, msg string) { errs = append(errs, apierr.FieldError{Field: f, Code: code, Message: msg}) }
	c.Name = strings.TrimSpace(c.Name)
	if !channelIDRe.MatchString(c.ID) {
		add("id", "invalid", "El identificador usa minúsculas, números y guiones (por ejemplo mx o mayoreo)")
	}
	if len([]rune(c.Name)) > 60 {
		add("name", "too_long", "El nombre admite hasta 60 caracteres")
	}
	if !currencyRe.MatchString(string(c.Currency)) {
		add("currency", "invalid", "Moneda ISO de 3 letras, por ejemplo MXN")
	}
	if c.Locale != "" && !localeRe.MatchString(c.Locale) {
		add("locale", "invalid", "Idioma y país, por ejemplo es-MX")
	}
	if c.Country != "" && !countryRe.MatchString(c.Country) {
		add("country", "invalid", "País de 2 letras, por ejemplo MX")
	}
	if c.TaxBps < 0 || c.TaxBps > 5000 {
		add("taxBps", "out_of_range", "La tasa debe estar entre 0% y 50%")
	}
	if c.MaxQtyPerItem < 0 || c.MaxQtyPerItem > 100000 {
		add("maxQtyPerItem", "out_of_range", "El máximo por producto no puede ser negativo")
	}
	if len(c.PriceList) > 60 {
		add("priceList", "too_long", "La lista de precios admite hasta 60 caracteres")
	}
	if c.TaxRegion != "" {
		cfg, err := s.DB.LoadLogistics(r.Context(), principal(r).Tenant)
		if err != nil {
			return nil, err
		}
		reg, ok := find(cfg.TaxRegions, func(x logistics.TaxRegion) bool { return x.ID == c.TaxRegion })
		switch {
		case !ok:
			add("taxRegion", "unknown", "La región fiscal no existe")
		case len(reg.States) > 0 || len(reg.PostalRanges) > 0:
			add("taxRegion", "invalid", "Elige una región que cubra todo el país; las regiones por código postal se aplican solas")
		}
	}
	if c.Shipping.Base.Currency != "" && c.Shipping.Base.Currency != c.Currency {
		add("shipping", "invalid", "La tarifa de envío debe estar en la moneda del canal")
	}
	// Market fields.
	switch c.Rounding {
	case "", "unit", "99", "ten":
	default:
		add("rounding", "invalid", "Redondeo: sin redondeo, a la unidad, terminación .99 o a la decena")
	}
	if c.Language != "" && !i18n.Known(c.Language) {
		add("language", "invalid", "Idioma no disponible")
	}
	if c.PriceFrom != "" {
		src, ok := channelOf(s, r, c.PriceFrom)
		switch {
		case c.PriceFrom == c.ID:
			add("priceFrom", "invalid", "Un canal no puede tomar los precios de sí mismo")
		case !ok:
			add("priceFrom", "unknown", "El canal de origen no existe")
		case src.PriceFrom != "":
			add("priceFrom", "invalid", "El canal de origen también convierte precios; elige uno con precios propios")
		}
		if c.RateMicros <= 0 || c.RateMicros > 1_000_000_000_000 {
			add("rateMicros", "out_of_range", "Escribe el tipo de cambio: cuánto vale 1 unidad de la moneda de origen")
		}
	} else if c.RateMicros != 0 {
		c.RateMicros = 0
	}
	if c.Market && c.Country == "" {
		add("country", "required", "Un mercado necesita su país")
	}
	// Import duties.
	if c.DutyBps < 0 || c.DutyBps > 10000 || c.DutyMinimis < 0 {
		add("dutyBps", "out_of_range", "El arancel va de 0% a 100% y el monto exento no puede ser negativo")
	}
	if len(c.DutyRules) > 100 {
		add("dutyRules", "too_many", "Máximo 100 tasas por código arancelario")
	}
	for i := range c.DutyRules {
		d := &c.DutyRules[i]
		d.HSPrefix = strings.ReplaceAll(strings.TrimSpace(d.HSPrefix), ".", "")
		if d.HSPrefix == "" || len(d.HSPrefix) > 10 || strings.Trim(d.HSPrefix, "0123456789") != "" || d.Bps < 0 || d.Bps > 10000 {
			add("dutyRules", "invalid", "Cada tasa lleva el inicio del código arancelario (solo dígitos) y un porcentaje de 0 a 100")
			break
		}
	}
	if (c.DutyBps > 0 || len(c.DutyRules) > 0) && c.Country == "" {
		add("country", "required", "Para cobrar aranceles el canal necesita su país")
	}
	return errs, nil
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	if id == mainChannel {
		conflict(w, "El canal "+mainChannel+" es el canal de la tienda en línea y no se puede eliminar")
		return
	}
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		var prices int64
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM prices WHERE tenant_id=$1 AND channel=$2`, pr.Tenant, id).Scan(&prices); err != nil {
			return err
		}
		if prices > 0 {
			return fmt.Errorf("%w: el canal tiene %d precios; elimínalos antes", store.ErrInUse, prices)
		}
		ct, err := tx.Exec(r.Context(), `DELETE FROM channels WHERE tenant_id=$1 AND id=$2`, pr.Tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err := store.Emit(r.Context(), tx, pr.Tenant, store.TopicChannel, id, nil); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "channel.delete", id, map[string]any{})
	})
	if s.storeError(w, err, "Canal no encontrado") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- store settings ----------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	st, at, err := s.DB.Settings(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := map[string]any{"settings": st, "states": logistics.States, "socialNetworks": sitecfg.SocialNetworks, "storefront": "/s/" + tenant + "/main"}
	if !at.IsZero() {
		out["updatedAt"] = at
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// putSettings saves identity, contact, address, social links and SEO rules.
// Checkout rules and the logo have their own endpoints and are kept.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in sitecfg.Settings
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	var errs []apierr.FieldError
	out, err := s.DB.UpdateSettings(r.Context(), pr.Tenant, pr.Actor(), func(cur *sitecfg.Settings) error {
		in.Checkout, in.OrderPrefix, in.LogoURL = cur.Checkout, cur.OrderPrefix, cur.LogoURL
		if errs = in.Validate(); len(errs) > 0 {
			return errValidation
		}
		*cur = in
		return nil
	})
	if errors.Is(err, errValidation) {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"settings": out})
}

var errValidation = errors.New("validation")

// putCheckout saves the checkout rules and the order number prefix.
func (s *Server) putCheckout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Checkout    sitecfg.Checkout `json:"checkout"`
		OrderPrefix string           `json:"orderPrefix"`
	}
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	var errs []apierr.FieldError
	out, err := s.DB.UpdateSettings(r.Context(), pr.Tenant, pr.Actor(), func(cur *sitecfg.Settings) error {
		next := *cur
		next.Checkout, next.OrderPrefix = in.Checkout, in.OrderPrefix
		for _, e := range next.Validate() {
			if e.Field == "orderPrefix" || strings.HasPrefix(e.Field, "checkout.") {
				errs = append(errs, e)
			}
		}
		if len(errs) > 0 {
			return errValidation
		}
		cur.Checkout, cur.OrderPrefix = next.Checkout, next.OrderPrefix
		return nil
	})
	if errors.Is(err, errValidation) {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"settings": out})
}

const maxLogoBytes = 512 << 10

var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

// putLogo stores the logo. The body is the image itself (PNG, JPEG, WebP or
// GIF up to 512 KB); the type is checked from the bytes, not from the header.
func (s *Server) putLogo(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLogoBytes+1))
	if err != nil || len(body) > maxLogoBytes {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Detail: "El logo debe pesar menos de 512 KB"})
		return
	}
	ct := http.DetectContentType(body)
	if !logoTypes[ct] {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "logo", Code: "invalid", Message: "Sube una imagen PNG, JPG, WebP o GIF"})
		return
	}
	pr := principal(r)
	sha, err := s.DB.PutAsset(r.Context(), pr.Tenant, pr.Actor(), "logo", ct, body)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	url := "/s/" + pr.Tenant + "/assets/logo?v=" + sha
	out, err := s.DB.UpdateSettings(r.Context(), pr.Tenant, pr.Actor(), func(cur *sitecfg.Settings) error {
		cur.LogoURL = url
		return nil
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"settings": out})
}

func (s *Server) deleteLogo(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.DB.DeleteAsset(r.Context(), pr.Tenant, pr.Actor(), "logo"); err != nil {
		internalError(w, s.Log, err)
		return
	}
	out, err := s.DB.UpdateSettings(r.Context(), pr.Tenant, pr.Actor(), func(cur *sitecfg.Settings) error {
		cur.LogoURL = ""
		return nil
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"settings": out})
}

// ---------- policies ----------

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	ps, err := s.DB.Policies(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	type std struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	var standard []std
	for _, x := range sitecfg.StandardPolicies {
		standard = append(standard, std{x.Slug, x.Title})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(ps), "standard": standard, "base": "/s/" + tenant + "/main/politicas/"})
}

func (s *Server) putPolicy(w http.ResponseWriter, r *http.Request) {
	var p sitecfg.Policy
	if !decode(w, r, &p) {
		return
	}
	p.Slug = r.PathValue("slug")
	if errs := p.Validate(); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	pr := principal(r)
	out, err := s.DB.PutPolicy(r.Context(), pr.Tenant, pr.Actor(), p)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	slug := r.PathValue("slug")
	st, _, err := s.DB.Settings(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if st.Checkout.TermsCheckbox && st.Checkout.TermsPolicy == slug {
		inUse(w, "El checkout pide aceptar esta política. Cambia la casilla de términos en Checkout antes de eliminarla.")
		return
	}
	if s.storeError(w, s.DB.DeletePolicy(r.Context(), pr.Tenant, pr.Actor(), slug), "Política no encontrada") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- menus ----------

func (s *Server) listMenus(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	ms, err := s.DB.Menus(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// The storefront has two fixed menus; they show up even before being saved.
	for _, std := range []sitecfg.Menu{{ID: "header", Name: "Menú principal"}, {ID: "footer", Name: "Pie de página"}} {
		found := false
		for _, m := range ms {
			found = found || m.ID == std.ID
		}
		if !found {
			std.Items = []sitecfg.MenuItem{}
			ms = append(ms, std)
		}
	}
	sort.SliceStable(ms, func(i, j int) bool { return rank(ms[i].ID) < rank(ms[j].ID) })
	// Targets a link can point to, so the editor offers real choices.
	type target struct {
		Ref   string `json:"ref"`
		Label string `json:"label"`
	}
	targets := map[string][]target{"category": {}, "policy": {}, "page": {}}
	ps, err := s.DB.Policies(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for _, p := range ps {
		targets["policy"] = append(targets["policy"], target{p.Slug, p.Title})
	}
	if t, ok := s.Proj.Tenant(tenant); ok {
		// Categories are offered by name and stored by handle, which is what the storefront URL uses.
		for _, c := range t.Taxo.Categories() {
			if c.Active && c.Handle != "" {
				name := c.Name
				if path := t.Taxo.Path(c.ID); len(path) > 1 {
					names := make([]string, len(path))
					for i, x := range path {
						names[i] = x.Name
					}
					name = strings.Join(names, " › ")
				}
				targets["category"] = append(targets["category"], target{c.Handle, name})
			}
		}
		sort.Slice(targets["category"], func(i, j int) bool { return targets["category"][i].Label < targets["category"][j].Label })
	}
	if st, err := s.openSite(tenant, "main"); err == nil {
		for h, tpl := range st.Templates() {
			targets["page"] = append(targets["page"], target{h, tpl.Name})
		}
		sort.Slice(targets["page"], func(i, j int) bool { return targets["page"][i].Ref < targets["page"][j].Ref })
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(ms), "targets": targets, "maxDepth": sitecfg.MaxMenuDepth})
}

func (s *Server) putMenu(w http.ResponseWriter, r *http.Request) {
	var m sitecfg.Menu
	if !decode(w, r, &m) {
		return
	}
	m.ID = r.PathValue("id")
	if errs := m.Validate(); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los enlaces marcados", errs...)
		return
	}
	pr := principal(r)
	if err := s.DB.PutDoc(r.Context(), pr.Tenant, pr.Actor(), "menus", m.ID, map[string]any{"name": m.Name, "items": m.Items}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	m.UpdatedAt = time.Now()
	httpx.JSON(w, r, http.StatusOK, m)
}

func (s *Server) deleteMenu(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.storeError(w, s.DB.DeleteDoc(r.Context(), pr.Tenant, pr.Actor(), "menus", r.PathValue("id"), nil), "Menú no encontrado") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- redirects ----------

func (s *Server) listRedirects(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 50, 200)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rs, total, err := s.DB.Redirects(r.Context(), principal(r).Tenant, q, r.URL.Query().Get("cursor"), limit+1)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(rs) > limit {
		rs = rs[:limit]
		next = rs[limit-1].From
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(rs), "nextCursor": next, "total": total})
}

func loopCheck(rs []sitecfg.Redirect) func(map[string]sitecfg.Redirect) error {
	return func(all map[string]sitecfg.Redirect) error {
		for _, r := range rs {
			if r.Active && sitecfg.Loop(all, r.From, r.Target) {
				return fmt.Errorf("%w: %s → %s forma un ciclo con otras redirecciones", store.ErrConflict, r.From, r.Target)
			}
		}
		return nil
	}
}

// createRedirects accepts one redirect or {"items": [...]} for bulk import.
func (s *Server) createRedirects(w http.ResponseWriter, r *http.Request) {
	var body struct {
		sitecfg.Redirect
		Items []sitecfg.Redirect `json:"items"`
	}
	body.Active = true
	if !decode(w, r, &body) {
		return
	}
	bulk := body.Items != nil
	rs := body.Items
	if !bulk {
		rs = []sitecfg.Redirect{body.Redirect}
	}
	if len(rs) == 0 || len(rs) > 1000 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "items", Code: "out_of_range", Message: "Envía entre 1 y 1,000 redirecciones"})
		return
	}
	var errs []apierr.FieldError
	seen := map[string]int{}
	for i := range rs {
		rs[i].ID = ""
		if bulk {
			rs[i].Active = true
		}
		for _, e := range rs[i].Validate() {
			if bulk {
				e.Field = fmt.Sprintf("items[%d].%s", i, e.Field)
				e.Message = fmt.Sprintf("Fila %d: %s", i+1, e.Message)
			}
			errs = append(errs, e)
		}
		if j, dup := seen[rs[i].From]; dup {
			errs = append(errs, apierr.FieldError{Field: fmt.Sprintf("items[%d].from", i), Code: "duplicate", Message: fmt.Sprintf("Fila %d: repite el origen de la fila %d (%s)", i+1, j+1, rs[i].From)})
		}
		seen[rs[i].From] = i
	}
	if len(errs) > 0 {
		if len(errs) > 20 {
			errs = errs[:20]
		}
		apierr.BadRequest(w, "Revisa las redirecciones", errs...)
		return
	}
	pr := principal(r)
	out, err := s.DB.SaveRedirects(r.Context(), pr.Tenant, pr.Actor(), rs, loopCheck(rs))
	if s.storeError(w, err, "Redirección no encontrada") {
		return
	}
	if bulk {
		httpx.JSON(w, r, http.StatusCreated, map[string]any{"items": out})
		return
	}
	httpx.JSON(w, r, http.StatusCreated, out[0])
}

func (s *Server) updateRedirect(w http.ResponseWriter, r *http.Request) {
	var rd sitecfg.Redirect
	if !decode(w, r, &rd) {
		return
	}
	rd.ID = r.PathValue("id")
	if errs := rd.Validate(); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	pr := principal(r)
	out, err := s.DB.SaveRedirects(r.Context(), pr.Tenant, pr.Actor(), []sitecfg.Redirect{rd}, loopCheck([]sitecfg.Redirect{rd}))
	if s.storeError(w, err, "Redirección no encontrada") {
		return
	}
	httpx.JSON(w, r, http.StatusOK, out[0])
}

func (s *Server) deleteRedirects(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 1000 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "ids", Code: "out_of_range", Message: "Envía entre 1 y 1,000 identificadores"})
		return
	}
	pr := principal(r)
	n, err := s.DB.DeleteRedirects(r.Context(), pr.Tenant, pr.Actor(), body.IDs)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"deleted": n})
}

// testRedirect explains what the storefront does with a path: each hop of
// the chain and the final destination.
func (s *Server) testRedirect(w http.ResponseWriter, r *http.Request) {
	path := sitecfg.NormalizePath(r.URL.Query().Get("path"))
	rs, _, err := s.DB.Redirects(r.Context(), principal(r).Tenant, "", "", 0)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	set := map[string]sitecfg.Redirect{}
	for _, x := range rs {
		if x.Active {
			set[x.From] = x
		}
	}
	type hop struct {
		From   string `json:"from"`
		Target string `json:"target"`
		Code   int    `json:"code"`
	}
	hops := []hop{}
	cur := path
	for i := 0; i < 10; i++ {
		x, ok := set[cur]
		if !ok {
			break
		}
		hops = append(hops, hop{x.From, x.Target, x.Code})
		if !strings.HasPrefix(x.Target, "/") {
			cur = x.Target
			break
		}
		cur = sitecfg.NormalizePath(x.Target)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"path": path, "redirects": len(hops) > 0, "hops": hops, "final": cur})
}

// ---------- domains ----------

// LookupTXT resolves TXT records; tests replace it.
var LookupTXT = func(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// edgeTarget is the hostname custom domains must point to (CNAME).
func edgeTarget() string {
	if t := os.Getenv("APEXX_EDGE_TARGET"); t != "" {
		return t
	}
	return "tiendas.apexx.dev"
}

type domainView struct {
	sitecfg.Domain
	TXTHost  string `json:"txtHost"`
	TXTValue string `json:"txtValue"`
	CNAME    string `json:"cnameTarget"`
}

func viewDomain(d sitecfg.Domain) domainView {
	return domainView{Domain: d, TXTHost: d.VerifyHost(), TXTValue: d.VerifyValue(), CNAME: edgeTarget()}
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	ds, err := s.DB.Domains(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]domainView, 0, len(ds))
	for _, d := range ds {
		out = append(out, viewDomain(d))
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "cnameTarget": edgeTarget(), "defaultUrl": "/s/" + tenant + "/main/index"})
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hostname string `json:"hostname"`
		Site     string `json:"site"`
	}
	if !decode(w, r, &body) {
		return
	}
	host := sitecfg.NormalizeHost(body.Hostname)
	if body.Site == "" {
		body.Site = "main"
	}
	var errs []apierr.FieldError
	if !sitecfg.ValidHost(host) {
		errs = append(errs, apierr.FieldError{Field: "hostname", Code: "invalid", Message: "Escribe un dominio válido, por ejemplo www.mitienda.mx"})
	} else if host == edgeTarget() || strings.HasSuffix(host, "."+edgeTarget()) {
		errs = append(errs, apierr.FieldError{Field: "hostname", Code: "reserved", Message: "Ese dominio pertenece a la plataforma"})
	}
	pr := principal(r)
	if _, err := s.openSite(pr.Tenant, body.Site); err != nil {
		errs = append(errs, apierr.FieldError{Field: "site", Code: "unknown", Message: "El sitio no existe"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	d, err := s.DB.CreateDomain(r.Context(), pr.Tenant, pr.Actor(), host, body.Site)
	if s.storeError(w, err, "") {
		return
	}
	httpx.JSON(w, r, http.StatusCreated, viewDomain(d))
}

// verifyDomain looks for the TXT record that proves the merchant controls
// the domain. Only then the host is routed to the store and Caddy may issue
// a certificate for it.
func (s *Server) verifyDomain(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	d, err := s.DB.GetDomain(r.Context(), pr.Tenant, r.PathValue("id"))
	if s.storeError(w, err, "Dominio no encontrado") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	txts, lerr := LookupTXT(ctx, d.VerifyHost())
	cancel()
	found, reason := false, ""
	for _, t := range txts {
		found = found || strings.TrimSpace(t) == d.VerifyValue()
	}
	switch {
	case found:
	case len(txts) == 0:
		var dnsErr *net.DNSError
		if lerr == nil || (errors.As(lerr, &dnsErr) && dnsErr.IsNotFound) {
			reason = "No encontramos el registro TXT en " + d.VerifyHost() + ". Los cambios de DNS pueden tardar hasta 48 horas."
		} else {
			reason = "No pudimos consultar el DNS de " + d.VerifyHost() + ". Intenta de nuevo en unos minutos."
		}
	default:
		reason = "El registro TXT de " + d.VerifyHost() + " existe pero su valor no coincide. Copia el valor exacto."
	}
	out, err := s.DB.SetDomainStatus(r.Context(), pr.Tenant, pr.Actor(), d.ID, found, reason)
	if s.storeError(w, err, "Dominio no encontrado") {
		return
	}
	httpx.JSON(w, r, http.StatusOK, viewDomain(out))
}

func (s *Server) primaryDomain(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.storeError(w, s.DB.SetPrimaryDomain(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")), "Dominio no encontrado") {
		return
	}
	s.listDomains(w, r)
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.storeError(w, s.DB.DeleteDomain(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")), "Dominio no encontrado") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rank orders the fixed menus first.
func rank(id string) int {
	switch id {
	case "header":
		return 0
	case "footer":
		return 1
	}
	return 2
}

// channelOf returns a saved channel of the store making the request.
func channelOf(s *Server, r *http.Request, id string) (cart.Channel, bool) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		return cart.Channel{}, false
	}
	return t.Channel(id)
}
