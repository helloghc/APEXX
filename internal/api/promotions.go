package api

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/promostore"
)

func (s *Server) promos() *promostore.Store { return &promostore.Store{DB: s.DB} }

func fieldErrors(w http.ResponseWriter, detail string, errs []promo.FieldError) {
	out := make([]apierr.FieldError, len(errs))
	for i, e := range errs {
		out[i] = apierr.FieldError{Field: e.Field, Code: "invalid", Message: e.Message}
	}
	apierr.BadRequest(w, detail, out...)
}

// namer resolves product and SKU titles from the read model for summaries.
func namer(t *projection.Tenant) promo.Namer {
	return func(kind, id string) string {
		if kind == "product" {
			if p, err := t.Catalog.Get(id); err == nil {
				return p.Title
			}
			return ""
		}
		if v, p, err := t.Catalog.VariantBySKU(id); err == nil {
			return strings.TrimSpace(p.Title + " " + v.Title)
		}
		return ""
	}
}

func decorate(t *projection.Tenant, v *promostore.PromotionView) {
	v.DisplayStatus = v.Promotion.DisplayStatus(time.Now())
	v.Summary = promo.Summary(v.Promotion, namer(t))
}

// customerHash is the keyed digest that identifies a shopper for limits.
func (s *Server) customerHash(tenant, email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return ""
	}
	h, err := s.Keys.BlindIndex(tenant, email)
	if err != nil {
		return ""
	}
	return h
}

// validateCartExtras checks the optional coupon and email fields of a cart.
func validateCartExtras(req *cart.Request) []apierr.FieldError {
	var errs []apierr.FieldError
	if len(req.Coupons) > 5 {
		errs = append(errs, apierr.FieldError{Field: "coupons", Code: "out_of_range", Message: "Se pueden usar hasta 5 cupones por compra"})
	}
	for i, c := range req.Coupons {
		if len(c) > 60 {
			errs = append(errs, apierr.FieldError{Field: "coupons[" + strconv.Itoa(i) + "]", Code: "invalid", Message: "Cupón demasiado largo"})
		}
	}
	if len(req.Email) > 200 {
		errs = append(errs, apierr.FieldError{Field: "email", Code: "invalid", Message: "Correo demasiado largo"})
	}
	if len(req.PaymentMethod) > 20 {
		errs = append(errs, apierr.FieldError{Field: "paymentMethod", Code: "invalid", Message: "Método de pago no válido"})
	}
	return errs
}

// resolveCart loads from the database what a cart with coupons or a known
// customer needs. Carts without either stay entirely in memory.
func (s *Server) resolveCart(r *http.Request, tenant string, req *cart.Request) error {
	hash := s.customerHash(tenant, req.Email)
	if len(req.Coupons) == 0 && hash == "" {
		return nil
	}
	lk, err := s.promos().Lookup(r.Context(), tenant, req.Coupons, hash)
	if err != nil {
		return err
	}
	req.Lookup = lk
	return nil
}

// ---------- promotions ----------

func (s *Server) listPromotions(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := promostore.PromotionFilter{Status: q.Get("status"), Type: q.Get("type"), Campaign: q.Get("campaign"),
		Query: strings.TrimSpace(q.Get("q")), Sort: q.Get("sort"), Limit: 25}
	switch f.Status {
	case "", "activa", "programada", "vencida", "pausada", "borrador", "archivada":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "status: activa, programada, vencida, pausada, borrador o archivada"})
		return
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "limit", Code: "out_of_range", Message: "limit debe estar entre 1 y 100"})
			return
		}
		f.Limit = n
	}
	cur, err := httpx.DecodeCursor(q.Get("cursor"))
	if cur != "" && err == nil {
		f.Offset, err = strconv.Atoi(cur)
	}
	if err != nil || f.Offset < 0 || len(f.Query) > 100 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "cursor", Code: "invalid", Message: "cursor o búsqueda no válidos"})
		return
	}
	items, total, counts, err := s.promos().ListPromotions(r.Context(), principal(r).Tenant, f)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for i := range items {
		decorate(t, &items[i])
	}
	next := ""
	if f.Offset+len(items) < total {
		next = httpx.EncodeCursor(strconv.Itoa(f.Offset + len(items)))
	}
	if items == nil {
		items = []promostore.PromotionView{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "total": total, "counts": counts, "nextCursor": next, "types": promo.Types})
}

func (s *Server) getPromotion(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	tenant := principal(r).Tenant
	v, err := s.promos().GetPromotion(r.Context(), tenant, r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Promoción no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	decorate(t, &v)
	reds, err := s.promos().Redemptions(r.Context(), tenant, v.ID, "", 20)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if reds == nil {
		reds = []promostore.Redemption{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"promotion": v, "redemptions": reds})
}

// readPromotion decodes a definition. The phase-0 body {kind, rule, active}
// is still accepted so existing integrations keep working.
func readPromotion(w http.ResponseWriter, r *http.Request, id string) (promo.Promotion, bool) {
	var raw json.RawMessage
	if !decode(w, r, &raw) {
		return promo.Promotion{}, false
	}
	var probe struct {
		Kind   string          `json:"kind"`
		Rule   json.RawMessage `json:"rule"`
		Active *bool           `json:"active"`
	}
	_ = json.Unmarshal(raw, &probe)
	var p promo.Promotion
	if probe.Kind != "" {
		if !promo.IsLegacyKind(probe.Kind) {
			apierr.BadRequest(w, "kind debe ser percent_over_threshold o buy_x_pay_y; para los demás tipos envía la definición con type")
			return p, false
		}
		var err error
		if p, err = promo.FromLegacy(id, probe.Kind, probe.Rule, probe.Active == nil || *probe.Active); err != nil {
			apierr.BadRequest(w, "Regla no válida: "+err.Error())
			return p, false
		}
	} else {
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			apierr.BadRequest(w, "JSON inválido: "+err.Error())
			return p, false
		}
	}
	if id != "" {
		p.ID = id
	}
	p.Normalize()
	return p, true
}

func slug(s string) string {
	s = promo.Fold(s)
	var b strings.Builder
	dash := false
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}

func shortID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) savePromotion(w http.ResponseWriter, r *http.Request, create bool) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	p, ok := readPromotion(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	auto := false
	if create && p.ID == "" {
		if p.ID, auto = slug(p.Name), true; p.ID == "" {
			p.ID = "promo-" + shortID()
		}
	}
	if errs := p.Validate(); len(errs) > 0 {
		fieldErrors(w, "La promoción tiene errores", errs)
		return
	}
	if p.Type == promo.TypeGift {
		if _, _, err := t.Catalog.VariantBySKU(p.GiftSKU); err != nil {
			apierr.BadRequest(w, "La promoción tiene errores", apierr.FieldError{Field: "giftSku", Code: "invalid", Message: "El SKU de regalo " + p.GiftSKU + " no existe en el catálogo."})
			return
		}
	}
	pr := principal(r)
	err := s.promos().SavePromotion(r.Context(), pr.Tenant, pr.Actor(), p, create)
	if errors.Is(err, promostore.ErrExists) && auto {
		p.ID += "-" + shortID()
		err = s.promos().SavePromotion(r.Context(), pr.Tenant, pr.Actor(), p, true)
	}
	switch {
	case errors.Is(err, promostore.ErrExists):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Ya existe una promoción con el identificador " + p.ID})
	case errors.Is(err, promostore.ErrNotFound):
		apierr.BadRequest(w, "La promoción tiene errores", apierr.FieldError{Field: "campaignId", Code: "invalid", Message: "La campaña elegida no existe."})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		v, err := s.promos().GetPromotion(r.Context(), pr.Tenant, p.ID)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		decorate(t, &v)
		status := http.StatusOK
		if create {
			status = http.StatusCreated
		}
		httpx.JSON(w, r, status, v)
	}
}

func (s *Server) createPromotion(w http.ResponseWriter, r *http.Request) { s.savePromotion(w, r, true) }
func (s *Server) putPromotion(w http.ResponseWriter, r *http.Request)    { s.savePromotion(w, r, false) }

func (s *Server) promotionsBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if !decode(w, r, &body) {
		return
	}
	status := map[string]string{"activate": promo.StatusActive, "pause": promo.StatusPaused, "archive": promo.StatusArchived, "draft": promo.StatusDraft}[body.Action]
	if status == "" || len(body.IDs) == 0 || len(body.IDs) > 200 {
		apierr.BadRequest(w, "Envía de 1 a 200 ids y una acción: activate, pause, archive o draft")
		return
	}
	pr := principal(r)
	n, err := s.promos().SetPromotionStatus(r.Context(), pr.Tenant, pr.Actor(), body.IDs, status)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"changed": n, "status": status})
}

func (s *Server) deletePromotion(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	archived, err := s.promos().DeletePromotion(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Promoción no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"deleted": !archived, "archived": archived})
}

func (s *Server) duplicatePromotion(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	pr := principal(r)
	v, err := s.promos().GetPromotion(r.Context(), pr.Tenant, r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Promoción no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	p := v.Promotion
	base := p.ID
	if len(base) > 50 {
		base = base[:50]
	}
	p.ID, p.Name, p.Status = base+"-copia-"+shortID(), p.Name+" (copia)", promo.StatusDraft
	if len(p.Name) > 120 {
		p.Name = p.Name[:120]
	}
	if err := s.promos().SavePromotion(r.Context(), pr.Tenant, pr.Actor(), p, true); err != nil {
		internalError(w, s.Log, err)
		return
	}
	nv, err := s.promos().GetPromotion(r.Context(), pr.Tenant, p.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	decorate(t, &nv)
	httpx.JSON(w, r, http.StatusCreated, nv)
}

// simBody is the test cart of the simulator and the builder preview.
type simBody struct {
	Channel       string      `json:"channel"`
	PostalCode    string      `json:"postalCode"`
	Items         []cart.Item `json:"items"`
	Coupons       []string    `json:"coupons"`
	Email         string      `json:"email"`
	PaymentMethod string      `json:"paymentMethod"`
	At            string      `json:"at"` // optional instant to simulate ("2026-11-15T10:00" in store time)
}

func (b simBody) request(w http.ResponseWriter) (cart.Request, bool) {
	req := cart.Request{Channel: b.Channel, PostalCode: b.PostalCode, Items: b.Items, Coupons: b.Coupons, Email: b.Email,
		PaymentMethod: b.PaymentMethod, Explain: true}
	errs := append(validateItems(req.Channel, req.Items), validateCartExtras(&req)...)
	if b.At != "" {
		at, err := promo.ParseTime(b.At)
		if err != nil {
			errs = append(errs, apierr.FieldError{Field: "at", Code: "invalid", Message: "Fecha no válida; usa AAAA-MM-DDTHH:MM"})
		}
		req.At = at
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "El carrito de prueba tiene errores", errs...)
		return req, false
	}
	return req, true
}

// simulatePromotions is the admin simulator: a test cart with every
// promotion explained, including the ones a shopper would never see.
func (s *Server) simulatePromotions(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var body simBody
	if !decode(w, r, &body) {
		return
	}
	req, ok := body.request(w)
	if !ok {
		return
	}
	if err := s.resolveCart(r, principal(r).Tenant, &req); err != nil {
		internalError(w, s.Log, err)
		return
	}
	res, err := t.Simulate(req)
	var uc cart.ErrUnknownChannel
	if errors.As(err, &uc) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "channel", Code: "unknown", Message: "El canal " + uc.ID + " no existe"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"result": res, "at": at, "atText": promo.When(at)})
}

// previewPromotion explains an unsaved definition: its plain-language
// summary, its validation problems and how it behaves on a test cart.
func (s *Server) previewPromotion(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var body struct {
		Promotion promo.Promotion `json:"promotion"`
		Cart      *simBody        `json:"cart"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := body.Promotion
	if p.ID == "" {
		p.ID = "vista-previa"
	}
	if p.Name == "" {
		p.Name = "Promoción sin nombre"
	}
	p.Normalize()
	out := map[string]any{"summary": "", "errors": []apierr.FieldError{}, "displayStatus": p.DisplayStatus(time.Now())}
	errs := p.Validate()
	fe := make([]apierr.FieldError, len(errs))
	for i, e := range errs {
		fe[i] = apierr.FieldError{Field: e.Field, Code: "invalid", Message: e.Message}
	}
	out["errors"] = fe
	if len(errs) == 0 {
		out["summary"] = promo.Summary(p, namer(t))
	}
	if len(errs) == 0 && body.Cart != nil && len(body.Cart.Items) > 0 {
		req, ok := body.Cart.request(w)
		if !ok {
			return
		}
		if err := s.resolveCart(r, principal(r).Tenant, &req); err != nil {
			internalError(w, s.Log, err)
			return
		}
		// The preview answers "would it apply once active?": drafts and paused
		// definitions are evaluated as active, and a coupon is assumed typed.
		p.Status = promo.StatusActive
		if p.Conditions.RequiresCoupon {
			if req.Lookup == nil {
				req.Lookup = &promo.Lookup{Coupons: map[string]promo.Coupon{}}
			}
			req.Lookup.Coupons["VISTA-PREVIA"] = promo.Coupon{Code: "VISTA-PREVIA", PromotionID: p.ID, Status: promo.StatusActive}
			req.Coupons = append(req.Coupons, "VISTA-PREVIA")
		}
		req.Extra = []promo.Promotion{p}
		res, err := t.Simulate(req)
		if err != nil {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "cart.channel", Code: "unknown", Message: "El canal del carrito de prueba no existe"})
			return
		}
		out["simulation"] = res
		for _, pr := range res.Promotions {
			if pr.ID == p.ID {
				out["result"] = pr
			}
		}
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// ---------- targets (pickers) ----------

type targetVariant struct {
	SKU       string `json:"sku"`
	Title     string `json:"title"`
	Price     *int64 `json:"price,omitempty"`
	Available int64  `json:"available"`
}

type targetProduct struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Brand      string          `json:"brand,omitempty"`
	Categories []string        `json:"categories,omitempty"`
	Variants   []targetVariant `json:"variants"`
}

func targetOf(t *projection.Tenant, channel string, p catalog.Product) targetProduct {
	out := targetProduct{ID: p.ID, Title: p.Title, Brand: p.Brand, Categories: p.Categories, Variants: []targetVariant{}}
	for _, v := range p.Variants {
		tv := targetVariant{SKU: v.SKU, Title: v.Title, Available: t.Inventory.Available(v.SKU)}
		if q, ok := t.Pricing.Base(channel, v.SKU, 1); ok {
			a := q.Unit.Amount
			tv.Price = &a
		}
		out.Variants = append(out.Variants, tv)
	}
	return out
}

// searchTargets feeds the product pickers: search by text, or resolve the
// titles of ids/SKUs already selected.
func (s *Server) searchTargets(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.Permissions.Allows("promotions", "read") && !p.Permissions.Allows("pricing", "read") {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Necesitas acceso a promociones o precios"})
		return
	}
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	channel := q.Get("channel")
	if channel == "" {
		channel = "mx"
	}
	out := []targetProduct{}
	seen := map[string]bool{}
	add := func(pr catalog.Product) {
		if !seen[pr.ID] {
			seen[pr.ID] = true
			out = append(out, targetOf(t, channel, pr))
		}
	}
	if ids, skus := q.Get("ids"), q.Get("skus"); ids != "" || skus != "" {
		for _, id := range strings.Split(ids, ",") {
			if pr, err := t.Catalog.Get(strings.TrimSpace(id)); err == nil && len(out) < 500 {
				add(pr)
			}
		}
		for _, sku := range strings.Split(skus, ",") {
			if _, pr, err := t.Catalog.VariantBySKU(strings.TrimSpace(sku)); err == nil && len(out) < 500 {
				add(pr)
			}
		}
	} else {
		text := strings.TrimSpace(q.Get("q"))
		if len(text) > 100 {
			text = text[:100]
		}
		// An exact SKU or product id wins over text search.
		if _, pr, err := t.Catalog.VariantBySKU(text); err == nil {
			add(pr)
		} else if pr, err := t.Catalog.Get(text); err == nil {
			add(pr)
		}
		ps, _ := t.Catalog.Search(text, "", 20)
		for _, pr := range ps {
			add(pr)
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// facets lists what a promotion can target: brands, categories and channels.
func (s *Server) promotionFacets(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.Permissions.Allows("promotions", "read") && !p.Permissions.Allows("pricing", "read") {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Necesitas acceso a promociones o precios"})
		return
	}
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	brands, cats, cols := map[string]int{}, map[string]int{}, map[string]int{}
	after := ""
	for {
		ps, next := t.Catalog.List(after, 500)
		for _, pr := range ps {
			if pr.Brand != "" {
				brands[pr.Brand]++
			}
			for _, c := range pr.Categories {
				cats[c]++
			}
			for _, c := range strings.Split(pr.Attributes["collections"], ",") {
				if c = strings.TrimSpace(c); c != "" {
					cols[c]++
				}
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	type facet struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	}
	list := func(m map[string]int) []facet {
		out := make([]facet, 0, len(m))
		for k, v := range m {
			out = append(out, facet{k, v})
		}
		sort.Slice(out, func(i, j int) bool { return promo.Fold(out[i].Value) < promo.Fold(out[j].Value) })
		return out
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"brands": list(brands), "categories": list(cats), "collections": list(cols),
		"channels": t.ChannelIDs(), "paymentMethods": []string{"card"}, "timezone": promo.Location.String()})
}

// ---------- campaigns ----------

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	cs, err := s.promos().ListCampaigns(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if cs == nil {
		cs = []promostore.CampaignView{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": cs})
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	tenant := principal(r).Tenant
	c, err := s.promos().GetCampaign(r.Context(), tenant, r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Campaña no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	ps, _, _, err := s.promos().ListPromotions(r.Context(), tenant, promostore.PromotionFilter{Campaign: c.ID, Limit: 100})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for i := range ps {
		decorate(t, &ps[i])
	}
	if ps == nil {
		ps = []promostore.PromotionView{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"campaign": c, "promotions": ps})
}

func (s *Server) saveCampaign(w http.ResponseWriter, r *http.Request, create bool) {
	var c promo.Campaign
	if !decode(w, r, &c) {
		return
	}
	if id := r.PathValue("id"); id != "" {
		c.ID = id
	}
	c.Normalize()
	auto := false
	if create && c.ID == "" {
		if c.ID, auto = slug(c.Name), true; c.ID == "" {
			c.ID = "campana-" + shortID()
		}
	}
	if errs := c.Validate(); len(errs) > 0 {
		fieldErrors(w, "La campaña tiene errores", errs)
		return
	}
	pr := principal(r)
	err := s.promos().SaveCampaign(r.Context(), pr.Tenant, pr.Actor(), c, create)
	if errors.Is(err, promostore.ErrExists) && auto {
		c.ID += "-" + shortID()
		err = s.promos().SaveCampaign(r.Context(), pr.Tenant, pr.Actor(), c, true)
	}
	if errors.Is(err, promostore.ErrExists) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Ya existe una campaña con el identificador " + c.ID})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	v, err := s.promos().GetCampaign(r.Context(), pr.Tenant, c.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, v)
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) { s.saveCampaign(w, r, true) }
func (s *Server) putCampaign(w http.ResponseWriter, r *http.Request)    { s.saveCampaign(w, r, false) }

func (s *Server) deleteCampaign(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	err := s.promos().DeleteCampaign(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Campaña no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- coupons ----------

func couponFilter(r *http.Request) promostore.CouponFilter {
	q := r.URL.Query()
	return promostore.CouponFilter{Promotion: q.Get("promotion"), Batch: q.Get("batch"), Query: promo.NormalizeCode(q.Get("q")), State: q.Get("state")}
}

func (s *Server) listCoupons(w http.ResponseWriter, r *http.Request) {
	f := couponFilter(r)
	f.Limit = 50
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "limit", Code: "out_of_range", Message: "limit debe estar entre 1 y 200"})
			return
		}
		f.Limit = n
	}
	after, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil || !in(f.State, "", "active", "disabled", "used", "unused") {
		apierr.BadRequest(w, "cursor o state no válidos (state: active, disabled, used, unused)")
		return
	}
	f.After = after
	f.Limit++ // one extra row tells whether there is a next page
	items, total, err := s.promos().ListCoupons(r.Context(), principal(r).Tenant, f)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(items) == f.Limit {
		items = items[:f.Limit-1]
		next = httpx.EncodeCursor(items[len(items)-1].Code)
	}
	if items == nil {
		items = []promostore.CouponView{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}

func in(v string, opts ...string) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

func validateSpec(spec promostore.CouponSpec) []apierr.FieldError {
	var errs []apierr.FieldError
	if spec.PromotionID == "" {
		errs = append(errs, apierr.FieldError{Field: "promotionId", Code: "required", Message: "Elige la promoción que activa el cupón."})
	}
	if spec.MaxUses < 0 || spec.MaxUsesPerCustomer < 0 || spec.MaxUses > 1_000_000_000 || spec.MaxUsesPerCustomer > 1_000_000 {
		errs = append(errs, apierr.FieldError{Field: "maxUses", Code: "out_of_range", Message: "Los límites de uso no pueden ser negativos."})
	}
	if spec.StartsAt != nil && spec.EndsAt != nil && !spec.EndsAt.After(*spec.StartsAt) {
		errs = append(errs, apierr.FieldError{Field: "endsAt", Code: "invalid", Message: "La fecha de fin debe ser posterior a la de inicio."})
	}
	return errs
}

func (s *Server) couponError(w http.ResponseWriter, err error, code string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, promostore.ErrExists) && code != "":
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "El cupón " + code + " ya existe"})
	case errors.Is(err, promostore.ErrExists):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "No hay suficientes códigos libres con ese prefijo y largo; usa un largo mayor"})
	case errors.Is(err, promostore.ErrNotFound):
		apierr.BadRequest(w, "El cupón tiene errores", apierr.FieldError{Field: "promotionId", Code: "invalid", Message: "La promoción elegida no existe o está archivada."})
	default:
		internalError(w, s.Log, err)
	}
	return true
}

func (s *Server) createCoupon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
		promostore.CouponSpec
	}
	if !decode(w, r, &body) {
		return
	}
	code := promo.NormalizeCode(body.Code)
	errs := validateSpec(body.CouponSpec)
	if !promo.ValidCode(code) {
		errs = append(errs, apierr.FieldError{Field: "code", Code: "invalid", Message: "El código lleva de 3 a 40 letras, números o guiones (por ejemplo BUENFIN10)."})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "El cupón tiene errores", errs...)
		return
	}
	pr := principal(r)
	if s.couponError(w, s.promos().CreateCoupon(r.Context(), pr.Tenant, pr.Actor(), code, body.CouponSpec), code) {
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"code": code, "promotionId": body.PromotionID})
}

func (s *Server) generateCoupons(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prefix   string `json:"prefix"`
		Length   int    `json:"length"`
		Quantity int    `json:"quantity"`
		promostore.CouponSpec
	}
	if !decode(w, r, &body) {
		return
	}
	prefix := promo.NormalizeCode(body.Prefix)
	if body.Length == 0 {
		body.Length = 8
	}
	errs := validateSpec(body.CouponSpec)
	if prefix != "" && !promo.ValidCode(prefix+"AAA") || len(prefix) > 20 {
		errs = append(errs, apierr.FieldError{Field: "prefix", Code: "invalid", Message: "El prefijo lleva hasta 20 letras, números o guiones (por ejemplo BF-)."})
	}
	if body.Length < 6 || body.Length > 16 {
		errs = append(errs, apierr.FieldError{Field: "length", Code: "out_of_range", Message: "El largo de la parte aleatoria va de 6 a 16 caracteres."})
	}
	if body.Quantity < 1 || body.Quantity > 50000 {
		errs = append(errs, apierr.FieldError{Field: "quantity", Code: "out_of_range", Message: "Genera de 1 a 50,000 cupones por lote."})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "El lote tiene errores", errs...)
		return
	}
	pr := principal(r)
	b, err := s.promos().GenerateCoupons(r.Context(), pr.Tenant, pr.Actor(), prefix, body.Length, body.Quantity, body.CouponSpec)
	if s.couponError(w, err, "") {
		return
	}
	httpx.JSON(w, r, http.StatusCreated, b)
}

func (s *Server) listCouponBatches(w http.ResponseWriter, r *http.Request) {
	bs, err := s.promos().ListBatches(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if bs == nil {
		bs = []promostore.Batch{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": bs})
}

func (s *Server) couponsBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Codes  []string `json:"codes"`
		Action string   `json:"action"` // enable | disable | delete
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Codes) == 0 || len(body.Codes) > 500 || !in(body.Action, "enable", "disable", "delete") {
		apierr.BadRequest(w, "Envía de 1 a 500 códigos y una acción: enable, disable o delete")
		return
	}
	for i := range body.Codes {
		body.Codes[i] = promo.NormalizeCode(body.Codes[i])
	}
	pr := principal(r)
	var n int64
	var err error
	switch body.Action {
	case "delete":
		n, err = s.promos().DeleteCoupons(r.Context(), pr.Tenant, pr.Actor(), body.Codes)
		if errors.Is(err, promostore.ErrInUse) {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "coupon_in_use", Detail: "Los cupones que ya se usaron no se pueden eliminar; desactívalos"})
			return
		}
		if errors.Is(err, promostore.ErrNotFound) {
			apierr.NotFound(w, "Cupón no encontrado")
			return
		}
	case "enable":
		n, err = s.promos().SetCouponStatus(r.Context(), pr.Tenant, pr.Actor(), body.Codes, "active")
	default:
		n, err = s.promos().SetCouponStatus(r.Context(), pr.Tenant, pr.Actor(), body.Codes, "disabled")
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"changed": n})
}

// csvSafe stops spreadsheet programs from running a cell as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (s *Server) exportCoupons(w http.ResponseWriter, r *http.Request) {
	f := couponFilter(r)
	if !in(f.State, "", "active", "disabled", "used", "unused") {
		apierr.BadRequest(w, "state no válido")
		return
	}
	name := "cupones"
	if f.Batch != "" {
		name += "-" + slug(f.Batch)
	} else if f.Promotion != "" {
		name += "-" + slug(f.Promotion)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\xEF\xBB\xBF")) // BOM so Excel reads accents
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"codigo", "promocion", "estado", "usos", "limite_total", "limite_por_cliente", "inicio", "fin", "lote", "creado"})
	ts := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.In(promo.Location).Format("2006-01-02 15:04")
	}
	lim := func(n int64) string {
		if n == 0 {
			return "sin límite"
		}
		return strconv.FormatInt(n, 10)
	}
	err := s.promos().ExportCoupons(r.Context(), principal(r).Tenant, f, func(c promostore.CouponView) error {
		return cw.Write([]string{c.Code, csvSafe(c.PromotionName), c.State, strconv.FormatInt(c.Uses, 10), lim(c.MaxUses), lim(c.MaxUsesPerCustomer),
			ts(c.StartsAt), ts(c.EndsAt), c.BatchID, ts(&c.CreatedAt)})
	})
	cw.Flush()
	if err != nil {
		s.Log.Error("coupon export", "err", err)
	}
}
