package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/store"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns "Zona Norte (MX)" into "zona-norte-mx".
func slugify(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, strings.ToLower(s))
	out = strings.Trim(nonSlug.ReplaceAllString(out, "-"), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// newSlugID returns a readable id for name that is not taken yet.
func newSlugID(name string, taken func(string) bool) string {
	base := slugify(name)
	if base == "" {
		return store.NewID("x")
	}
	id := base
	for i := 2; taken(id); i++ {
		id = base + "-" + strconv.Itoa(i)
	}
	return id
}

func inUse(w http.ResponseWriter, detail string) {
	apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_use", Detail: detail})
}

func conflict(w http.ResponseWriter, detail string) {
	apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: detail})
}

// storeError maps common store errors; it reports whether it wrote a response.
func (s *Server) storeError(w http.ResponseWriter, err error, notFound string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, notFound)
	case errors.Is(err, store.ErrInUse):
		inUse(w, strings.TrimPrefix(err.Error(), store.ErrInUse.Error()+": "))
	case errors.Is(err, store.ErrConflict):
		conflict(w, strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": "))
	default:
		internalError(w, s.Log, err)
	}
	return true
}

type shippingView struct {
	Zones     []logistics.Zone     `json:"zones"`
	Profiles  []logistics.Profile  `json:"profiles"`
	Carriers  []logistics.Carrier  `json:"carriers"`
	Options   []logistics.Option   `json:"options"`
	Holidays  []logistics.Holiday  `json:"holidays"`
	Locations []logistics.Location `json:"locations"`
	States    []logistics.State    `json:"states"`
	// ProductsByProfile counts products assigned to each non-default profile.
	ProductsByProfile map[string]int `json:"productsByProfile"`
	Timezone          string         `json:"timezone"`
	// Legacy is the single rate of the main channel, used while no option exists.
	Legacy any `json:"legacy,omitempty"`
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (s *Server) getShipping(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	c, err := s.DB.LoadLogistics(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	v := shippingView{Zones: nonNil(c.Zones), Profiles: nonNil(c.Profiles), Carriers: nonNil(c.Carriers), Options: nonNil(c.Options),
		Holidays: nonNil(c.Holidays), Locations: nonNil(c.Locations), States: logistics.States, ProductsByProfile: map[string]int{}, Timezone: c.Location().String()}
	for _, pc := range c.Products {
		if pc.ShippingProfile != "" {
			v.ProductsByProfile[pc.ShippingProfile]++
		}
	}
	if !c.HasOptions() {
		if t, ok := s.Proj.Tenant(tenant); ok {
			if ch, ok := t.Channel("mx"); ok {
				v.Legacy = ch.Shipping
			}
		}
	}
	httpx.JSON(w, r, http.StatusOK, v)
}

// putShippingDoc creates (POST, id from the name) or updates (PUT) a zone,
// profile, carrier or option.
func (s *Server) putShippingDoc(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	kind, id := r.PathValue("kind"), r.PathValue("id")
	c, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var doc any
	var errs []apierr.FieldError
	var name string
	exists := map[string]bool{}
	switch kind {
	case "zones":
		var z logistics.Zone
		if !decode(w, r, &z) {
			return
		}
		errs, name, doc = z.Validate(), z.Name, &z
		for _, x := range c.Zones {
			exists[x.ID] = true
		}
	case "profiles":
		var p logistics.Profile
		if !decode(w, r, &p) {
			return
		}
		errs, name, doc = p.Validate(), p.Name, &p
		for _, x := range c.Profiles {
			exists[x.ID] = true
			if x.Kind == "default" && p.Kind == "default" && x.ID != id {
				errs = append(errs, apierr.FieldError{Field: "kind", Code: "conflict", Message: "Ya existe un perfil estándar: " + x.Name})
			}
		}
	case "carriers":
		var x logistics.Carrier
		if !decode(w, r, &x) {
			return
		}
		errs, name, doc = x.Validate(), x.Name, &x
		for _, y := range c.Carriers {
			exists[y.ID] = true
		}
	case "options":
		var o logistics.Option
		if !decode(w, r, &o) {
			return
		}
		errs, name, doc = o.Validate(c), o.Name, &o
		for _, y := range c.Options {
			exists[y.ID] = true
		}
		if id == "" && o.Position == 0 {
			o.Position = len(c.Options) + 1
		}
	default:
		apierr.NotFound(w, "Tipo desconocido: usa zones, profiles, carriers u options")
		return
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	status := http.StatusOK
	if id == "" {
		id, status = newSlugID(name, func(x string) bool { return exists[x] }), http.StatusCreated
	} else if !exists[id] {
		apierr.NotFound(w, "No existe ese registro")
		return
	}
	// The id lives in the key, not in the document.
	b, _ := json.Marshal(doc)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	delete(m, "id")
	if err := s.DB.PutDoc(r.Context(), pr.Tenant, pr.Actor(), kind, id, m); err != nil {
		internalError(w, s.Log, err)
		return
	}
	m["id"] = id
	httpx.JSON(w, r, status, m)
}

func (s *Server) deleteShippingDoc(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	kind, id := r.PathValue("kind"), r.PathValue("id")
	c, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var users []string
	switch kind {
	case "zones":
		for _, o := range c.Options {
			if o.ZoneID == id {
				users = append(users, o.Name)
			}
		}
	case "profiles":
		for _, o := range c.Options {
			if o.ProfileID == id {
				users = append(users, o.Name)
			}
		}
		n := 0
		for _, pc := range c.Products {
			if pc.ShippingProfile == id {
				n++
			}
		}
		if n > 0 {
			inUse(w, "Hay "+strconv.Itoa(n)+" productos con este perfil. Cámbialos de perfil antes de eliminarlo.")
			return
		}
	case "carriers":
		for _, o := range c.Options {
			if o.CarrierID == id {
				users = append(users, o.Name)
			}
		}
	case "options":
	default:
		apierr.NotFound(w, "Tipo desconocido")
		return
	}
	if len(users) > 0 {
		inUse(w, "Lo usan las opciones de envío: "+strings.Join(users, ", ")+". Cámbialas o elimínalas primero.")
		return
	}
	if s.storeError(w, s.DB.DeleteDoc(r.Context(), pr.Tenant, pr.Actor(), kind, id, nil), "No existe ese registro") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shippingDefaults installs the recommended setup for Mexico. It only adds
// what is missing, so running it twice changes nothing.
func (s *Server) shippingDefaults(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	added, err := InstallShippingDefaults(r.Context(), s.DB, pr.Tenant, pr.Actor())
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"added": added})
}

// InstallShippingDefaults writes zones, profiles, carriers, options and this
// year's and next year's holidays for a store in Mexico (missing rows only).
func InstallShippingDefaults(ctx context.Context, db *store.DB, tenant, actor string) (int, error) {
	c, err := db.LoadLogistics(ctx, tenant)
	if err != nil {
		return 0, err
	}
	d := logistics.MexicoDefaults()
	added := 0
	err = db.Tx(ctx, tenant, func(tx pgx.Tx) error {
		put := func(kind, id string, has bool, doc any) error {
			if has {
				return nil
			}
			b, _ := json.Marshal(doc)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			delete(m, "id")
			added++
			return store.PutDocTx(ctx, tx, tenant, actor, kind, id, m)
		}
		for _, z := range d.Zones {
			_, has := find(c.Zones, func(x logistics.Zone) bool { return x.ID == z.ID })
			if err := put("zones", z.ID, has, z); err != nil {
				return err
			}
		}
		for _, p := range d.Profiles {
			_, has := find(c.Profiles, func(x logistics.Profile) bool { return x.ID == p.ID || (p.Kind == "default" && x.Kind == "default") })
			if err := put("profiles", p.ID, has, p); err != nil {
				return err
			}
		}
		for _, x := range d.Carriers {
			_, has := find(c.Carriers, func(y logistics.Carrier) bool { return y.ID == x.ID })
			if err := put("carriers", x.ID, has, x); err != nil {
				return err
			}
		}
		for _, o := range d.Options {
			_, has := find(c.Options, func(y logistics.Option) bool { return y.ID == o.ID })
			if err := put("options", o.ID, has, o); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if len(c.Holidays) == 0 {
		y := time.Now().In(c.Location()).Year()
		hs := append(logistics.MexicanHolidays(y), logistics.MexicanHolidays(y+1)...)
		if err := db.SetHoliday(ctx, tenant, actor, hs...); err != nil {
			return added, err
		}
		added += len(hs)
	}
	return added, nil
}

func find[T any](xs []T, ok func(T) bool) (T, bool) {
	for _, x := range xs {
		if ok(x) {
			return x, true
		}
	}
	var zero T
	return zero, false
}

func (s *Server) putHoliday(w http.ResponseWriter, r *http.Request) {
	var h logistics.Holiday
	if !decode(w, r, &h) {
		return
	}
	h.Date = r.PathValue("date")
	if errs := h.Validate(); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	pr := principal(r)
	if err := s.DB.SetHoliday(r.Context(), pr.Tenant, pr.Actor(), h); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, h)
}

func (s *Server) deleteHoliday(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if _, err := time.Parse("2006-01-02", r.PathValue("date")); err != nil {
		apierr.NotFound(w, "Fecha no válida")
		return
	}
	if s.storeError(w, s.DB.DeleteHoliday(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("date")), "Ese día no está en el calendario") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// holidayDefaults adds the official Mexican holidays of ?year=.
func (s *Server) holidayDefaults(w http.ResponseWriter, r *http.Request) {
	year, err := strconv.Atoi(r.URL.Query().Get("year"))
	if err != nil || year < 2020 || year > 2100 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "year", Code: "invalid", Message: "Indica un año entre 2020 y 2100"})
		return
	}
	pr := principal(r)
	hs := logistics.MexicanHolidays(year)
	if err := s.DB.SetHoliday(r.Context(), pr.Tenant, pr.Actor(), hs...); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": hs})
}

// shippingQuote is the simulator of the admin: every option, available or
// not, with the reason, the price formula and the delivery estimate.
func (s *Server) shippingQuote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PostalCode  string `json:"postalCode"`
		Subtotal    int64  `json:"subtotal"`
		WeightGrams int64  `json:"weightGrams"`
		Profile     string `json:"profile"`
		At          string `json:"at"` // optional RFC 3339 instant, to test cut-off and holidays
	}
	if !decode(w, r, &body) {
		return
	}
	var errs []apierr.FieldError
	if body.PostalCode != "" && !logistics.ValidPostalCode(body.PostalCode) {
		errs = append(errs, apierr.FieldError{Field: "postalCode", Code: "invalid", Message: "El código postal debe tener 5 dígitos"})
	}
	if body.Subtotal < 0 || body.WeightGrams < 0 {
		errs = append(errs, apierr.FieldError{Field: "subtotal", Code: "out_of_range", Message: "Monto y peso no pueden ser negativos"})
	}
	now := time.Now()
	if body.At != "" {
		t, err := time.Parse(time.RFC3339, body.At)
		if err != nil {
			errs = append(errs, apierr.FieldError{Field: "at", Code: "invalid", Message: "Fecha y hora no válidas"})
		}
		now = t
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	c, err := s.DB.LoadLogistics(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	quotes := c.Quotes(logistics.QuoteInput{PostalCode: body.PostalCode, Subtotal: body.Subtotal, WeightGrams: body.WeightGrams,
		Profiles: []string{body.Profile}, Currency: "MXN", Now: now})
	chosen := ""
	if i := logistics.Choose(quotes, ""); i >= 0 && body.PostalCode != "" {
		chosen = quotes[i].ID
	}
	state := logistics.StateOf(body.PostalCode)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"quotes": nonNil(quotes), "default": chosen, "state": logistics.StateName(state),
		"at": now.In(c.Location()).Format(time.RFC3339)})
}

// ---------- taxes ----------

func (s *Server) listTaxRegions(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	c, err := s.DB.LoadLogistics(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := map[string]any{"items": nonNil(c.TaxRegions), "states": logistics.States}
	if t, ok := s.Proj.Tenant(tenant); ok {
		if ch, ok := t.Channel("mx"); ok {
			out["legacyBps"] = ch.TaxBps
		}
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) putTaxRegion(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	var reg logistics.TaxRegion
	if !decode(w, r, &reg) {
		return
	}
	if errs := reg.Validate(); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	c, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	exists := map[string]bool{}
	for _, x := range c.TaxRegions {
		exists[x.ID] = true
	}
	status := http.StatusOK
	if id == "" {
		id, status = newSlugID(reg.Name, func(x string) bool { return exists[x] }), http.StatusCreated
	} else if !exists[id] {
		apierr.NotFound(w, "No existe esa región")
		return
	}
	reg.ID = id
	err = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		// Only one region is the default of the store.
		if reg.Default {
			for _, x := range c.TaxRegions {
				if x.Default && x.ID != id {
					x.Default = false
					if err := store.PutDocTx(r.Context(), tx, pr.Tenant, pr.Actor(), "tax", x.ID, withoutID(x)); err != nil {
						return err
					}
				}
			}
		}
		return store.PutDocTx(r.Context(), tx, pr.Tenant, pr.Actor(), "tax", id, withoutID(reg))
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, status, reg)
}

func withoutID(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	delete(m, "id")
	return m
}

func (s *Server) deleteTaxRegion(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	chs, err := s.channelList(r)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for _, ch := range chs {
		if ch.TaxRegion == id {
			inUse(w, "El canal "+ch.ID+" usa esta región fiscal. Cámbiala en Canales antes de eliminarla.")
			return
		}
	}
	if s.storeError(w, s.DB.DeleteDoc(r.Context(), pr.Tenant, pr.Actor(), "tax", id, nil), "No existe esa región") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) taxDefaults(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	n, err := InstallTaxDefaults(r.Context(), s.DB, pr.Tenant, pr.Actor())
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"added": n})
}

// InstallTaxDefaults adds IVA 16% and the 8% northern-border region when missing.
func InstallTaxDefaults(ctx context.Context, db *store.DB, tenant, actor string) (int, error) {
	c, err := db.LoadLogistics(ctx, tenant)
	if err != nil {
		return 0, err
	}
	added := 0
	_, hasDefault := find(c.TaxRegions, func(x logistics.TaxRegion) bool { return x.Default })
	err = db.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, reg := range logistics.MexicoTaxDefaults() {
			if _, has := find(c.TaxRegions, func(x logistics.TaxRegion) bool { return x.ID == reg.ID }); has {
				continue
			}
			if reg.Default && hasDefault {
				reg.Default = false
			}
			added++
			if err := store.PutDocTx(ctx, tx, tenant, actor, "tax", reg.ID, withoutID(reg)); err != nil {
				return err
			}
		}
		return nil
	})
	return added, err
}

// taxPreview explains the tax inside a tax-inclusive amount for a postal code.
func (s *Server) taxPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PostalCode string `json:"postalCode"`
		Amount     int64  `json:"amount"`
		Class      string `json:"class"`
		Channel    string `json:"channel"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Class == "" {
		body.Class = logistics.TaxGeneral
	}
	var errs []apierr.FieldError
	if body.PostalCode != "" && !logistics.ValidPostalCode(body.PostalCode) {
		errs = append(errs, apierr.FieldError{Field: "postalCode", Code: "invalid", Message: "El código postal debe tener 5 dígitos"})
	}
	if body.Amount < 0 {
		errs = append(errs, apierr.FieldError{Field: "amount", Code: "out_of_range", Message: "El monto no puede ser negativo"})
	}
	if !logistics.ValidTaxClass(body.Class) {
		errs = append(errs, apierr.FieldError{Field: "class", Code: "invalid", Message: "Clase: general, exento o tasa0"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	tenant := principal(r).Tenant
	c, err := s.DB.LoadLogistics(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if body.Channel == "" {
		body.Channel = "mx"
	}
	var region string
	var legacy int64
	if t, ok := s.Proj.Tenant(tenant); ok {
		if ch, ok := t.Channel(body.Channel); ok {
			region, legacy = ch.TaxRegion, ch.TaxBps
		}
	}
	httpx.JSON(w, r, http.StatusOK, c.Tax("MX", body.PostalCode, region, legacy, []logistics.TaxPart{{Class: body.Class, Gross: body.Amount}}))
}

// ---------- per-product tax class and shipping profile ----------

type productClassView struct {
	ProductID       string `json:"productId"`
	Title           string `json:"title"`
	Handle          string `json:"handle"`
	TaxClass        string `json:"taxClass"`
	ShippingProfile string `json:"shippingProfile"`
	Missing         bool   `json:"missing,omitempty"` // the product no longer exists
}

func (s *Server) listProductClasses(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	c, err := s.DB.LoadLogistics(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	t, _ := s.Proj.Tenant(tenant)
	out := make([]productClassView, 0, len(c.Products))
	for id, pc := range c.Products {
		v := productClassView{ProductID: id, TaxClass: pc.TaxClass, ShippingProfile: pc.ShippingProfile, Title: id}
		if t != nil {
			if p, err := t.Catalog.Get(id); err == nil {
				v.Title, v.Handle = p.Title, p.Handle
			} else {
				v.Missing = true
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) putProductClass(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	var pc logistics.ProductClass
	if !decode(w, r, &pc) {
		return
	}
	if pc.TaxClass == "" {
		pc.TaxClass = logistics.TaxGeneral
	}
	var errs []apierr.FieldError
	if !logistics.ValidTaxClass(pc.TaxClass) {
		errs = append(errs, apierr.FieldError{Field: "taxClass", Code: "invalid", Message: "Clase: general, exento o tasa0"})
	}
	c, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if pc.ShippingProfile != "" {
		p, ok := find(c.Profiles, func(x logistics.Profile) bool { return x.ID == pc.ShippingProfile })
		switch {
		case !ok:
			errs = append(errs, apierr.FieldError{Field: "shippingProfile", Code: "unknown", Message: "El perfil de envío no existe"})
		case p.Kind == "default":
			pc.ShippingProfile = ""
		}
	}
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	if _, err := t.Catalog.Get(id); err != nil {
		if _, had := c.Products[id]; !had {
			apierr.NotFound(w, "Producto no encontrado")
			return
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	if err := s.DB.SetProductClass(r.Context(), pr.Tenant, pr.Actor(), id, pc); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, pc)
}

// productSearch finds products by name, brand or SKU for pickers.
func (s *Server) productSearch(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 100 {
		q = q[:100]
	}
	ps, _ := t.Catalog.Search(q, "", 12)
	type item struct {
		ID     string   `json:"id"`
		Title  string   `json:"title"`
		Handle string   `json:"handle"`
		SKUs   []string `json:"skus"`
	}
	out := make([]item, 0, len(ps))
	for _, p := range ps {
		it := item{ID: p.ID, Title: p.Title, Handle: p.Handle, SKUs: []string{}}
		for _, v := range p.Variants {
			it.SKUs = append(it.SKUs, v.SKU)
		}
		out = append(out, it)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}
