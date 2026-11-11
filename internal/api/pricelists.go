package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/promostore"
)

// priceListItemView adds what a person needs to read a price list row.
type priceListItemView struct {
	SKU    string `json:"sku"`
	Amount *int64 `json:"amount,omitempty"`
	Title  string `json:"title"`
	Base   *int64 `json:"basePrice,omitempty"` // regular price in the list's channel
	Final  *int64 `json:"finalPrice,omitempty"`
}

func (s *Server) listPriceLists(w http.ResponseWriter, r *http.Request) {
	ls, err := s.promos().ListPriceLists(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if ls == nil {
		ls = []promostore.PriceList{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ls})
}

func priceListView(t *projection.Tenant, l promostore.PriceList) map[string]any {
	ch := l.Channel
	if ch == "" {
		ch = "mx"
	}
	items := make([]priceListItemView, 0, len(l.Items))
	for _, it := range l.Items {
		v := priceListItemView{SKU: it.SKU, Amount: it.Amount}
		if va, p, err := t.Catalog.VariantBySKU(it.SKU); err == nil {
			v.Title = strings.TrimSpace(p.Title + " " + va.Title)
		}
		if q, ok := t.Pricing.Base(ch, it.SKU, 1); ok {
			b := q.Unit.Amount
			v.Base = &b
			f := b
			if l.Kind == "override" && it.Amount != nil {
				f = *it.Amount
			} else if l.Kind == "adjust" {
				c := promostore.BulkChange{Mode: "percent", Value: l.AdjustBps}
				f = c.Apply(b)
			}
			v.Final = &f
		}
		items = append(items, v)
	}
	return map[string]any{"list": l, "items": items}
}

func (s *Server) getPriceList(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	l, err := s.promos().GetPriceList(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Lista de precios no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, priceListView(t, l))
}

func (s *Server) savePriceList(w http.ResponseWriter, r *http.Request, create bool) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var body struct {
		ID            string                     `json:"id"`
		Name          string                     `json:"name"`
		Description   string                     `json:"description"`
		Status        string                     `json:"status"`
		Channel       string                     `json:"channel"`
		CustomerGroup string                     `json:"customerGroup"`
		Kind          string                     `json:"kind"`
		AdjustBps     int64                      `json:"adjustBps"`
		AppliesTo     string                     `json:"appliesTo"`
		Priority      int                        `json:"priority"`
		StartsAt      *time.Time                 `json:"startsAt"`
		EndsAt        *time.Time                 `json:"endsAt"`
		Items         []promostore.PriceListItem `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	l := promostore.PriceList{ID: body.ID, Name: body.Name, Description: body.Description, Status: body.Status, Channel: body.Channel,
		CustomerGroup: body.CustomerGroup, Kind: body.Kind, AdjustBps: body.AdjustBps, AppliesTo: body.AppliesTo, Priority: body.Priority,
		StartsAt: body.StartsAt, EndsAt: body.EndsAt, Items: body.Items}
	if id := r.PathValue("id"); id != "" {
		l.ID = id
	}
	l.Normalize()
	auto := false
	if create && l.ID == "" {
		if l.ID, auto = slug(l.Name), true; l.ID == "" {
			l.ID = "lista-" + shortID()
		}
	}
	errs := l.Validate()
	if l.Channel != "" {
		if _, ok := t.Channel(l.Channel); !ok {
			errs = append(errs, promo.FieldError{Field: "channel", Message: "El canal " + l.Channel + " no existe."})
		}
	}
	for i, it := range l.Items {
		if _, _, err := t.Catalog.VariantBySKU(it.SKU); err != nil && len(errs) < 20 {
			errs = append(errs, promo.FieldError{Field: "items[" + strconv.Itoa(i) + "].sku", Message: "El SKU " + it.SKU + " no existe en el catálogo."})
		}
	}
	if len(errs) > 0 {
		fieldErrors(w, "La lista de precios tiene errores", errs)
		return
	}
	pr := principal(r)
	err := s.promos().SavePriceList(r.Context(), pr.Tenant, pr.Actor(), l, create)
	if errors.Is(err, promostore.ErrExists) && auto {
		l.ID += "-" + shortID()
		err = s.promos().SavePriceList(r.Context(), pr.Tenant, pr.Actor(), l, true)
	}
	if errors.Is(err, promostore.ErrExists) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Ya existe una lista con el identificador " + l.ID})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	saved, err := s.promos().GetPriceList(r.Context(), pr.Tenant, l.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, priceListView(t, saved))
}

func (s *Server) createPriceList(w http.ResponseWriter, r *http.Request) { s.savePriceList(w, r, true) }
func (s *Server) putPriceList(w http.ResponseWriter, r *http.Request)    { s.savePriceList(w, r, false) }

func (s *Server) deletePriceList(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	err := s.promos().DeletePriceList(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"))
	if errors.Is(err, promostore.ErrNotFound) {
		apierr.NotFound(w, "Lista de precios no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- bulk price change ----------

type bulkBody struct {
	promostore.BulkChange
	Scope struct {
		All        bool     `json:"all"`
		SKUs       []string `json:"skus"`
		ProductIDs []string `json:"productIds"`
		Brands     []string `json:"brands"`
		Categories []string `json:"categories"`
	} `json:"scope"`
	Hash string `json:"hash"` // apply only: the fingerprint returned by the preview
}

// bulkScope resolves the scope into SKUs using the catalog read model.
func bulkScope(t *projection.Tenant, b *bulkBody) (skus []string, titles map[string]string) {
	titles = map[string]string{}
	target := promo.Target{SKUs: b.Scope.SKUs, ProductIDs: b.Scope.ProductIDs, Brands: b.Scope.Brands, Categories: b.Scope.Categories}
	after := ""
	for {
		ps, next := t.Catalog.List(after, 500)
		for _, p := range ps {
			for _, v := range p.Variants {
				if b.Scope.All || target.Matches(promo.Line{SKU: v.SKU, ProductID: p.ID, Brand: p.Brand, Categories: p.Categories}) {
					skus = append(skus, v.SKU)
					titles[v.SKU] = strings.TrimSpace(p.Title + " " + v.Title)
				}
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	return skus, titles
}

func (s *Server) bulkPrices(w http.ResponseWriter, r *http.Request, apply bool) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var b bulkBody
	if !decode(w, r, &b) {
		return
	}
	errs := b.BulkChange.Validate()
	if _, ok := t.Channel(b.Channel); !ok && b.Channel != "" {
		errs = append(errs, promo.FieldError{Field: "channel", Message: "El canal " + b.Channel + " no existe."})
	}
	sc := b.Scope
	if !sc.All && len(sc.SKUs)+len(sc.ProductIDs)+len(sc.Brands)+len(sc.Categories) == 0 {
		errs = append(errs, promo.FieldError{Field: "scope", Message: "Elige a qué productos aplica el cambio."})
	}
	if apply && len(b.Hash) != 64 {
		errs = append(errs, promo.FieldError{Field: "hash", Message: "Genera la vista previa antes de aplicar."})
	}
	if len(errs) > 0 {
		fieldErrors(w, "El cambio de precios tiene errores", errs)
		return
	}
	skus, titles := bulkScope(t, &b)
	if len(skus) > promostore.MaxBulkSKUs {
		apierr.BadRequest(w, "El cambio alcanza "+strconv.Itoa(len(skus))+" SKUs; el máximo por operación es "+strconv.Itoa(promostore.MaxBulkSKUs)+". Divide por marca o categoría.")
		return
	}
	hash := ""
	if apply {
		hash = b.Hash
	}
	pr := principal(r)
	res, err := s.promos().BulkPrices(r.Context(), pr.Tenant, pr.Actor(), b.BulkChange, skus, titles, hash)
	if errors.Is(err, promostore.ErrChanged) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "prices_changed", Detail: "Los precios cambiaron desde la vista previa. Revísala de nuevo antes de aplicar."})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, res)
}

func (s *Server) bulkPricesPreview(w http.ResponseWriter, r *http.Request) { s.bulkPrices(w, r, false) }
func (s *Server) bulkPricesApply(w http.ResponseWriter, r *http.Request)   { s.bulkPrices(w, r, true) }

func (s *Server) priceHistory(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	sku := strings.TrimSpace(q.Get("sku"))
	list, err := s.promos().PriceHistory(r.Context(), principal(r).Tenant, sku, q.Get("channel"), q.Get("ref"), before, limit)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	type row struct {
		promostore.PriceChange
		Title string `json:"title"`
	}
	out := make([]row, len(list))
	for i, c := range list {
		out[i] = row{PriceChange: c}
		if v, p, err := t.Catalog.VariantBySKU(c.SKU); err == nil {
			out[i].Title = strings.TrimSpace(p.Title + " " + v.Title)
		}
	}
	next := int64(0)
	if len(list) == limit {
		next = list[len(list)-1].ID
	}
	resp := map[string]any{"items": out, "nextBefore": next}
	if sku != "" {
		// Current price with the price list in force, to explain what shoppers see.
		ch := q.Get("channel")
		if ch == "" {
			ch = "mx"
		}
		if qt, ok := t.Pricing.Resolve(ch, sku, 1); ok {
			resp["current"] = qt
		}
		if v, p, err := t.Catalog.VariantBySKU(sku); err == nil {
			resp["title"] = strings.TrimSpace(p.Title + " " + v.Title)
		}
	}
	httpx.JSON(w, r, http.StatusOK, resp)
}
