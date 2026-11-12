package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// Kits (bundles): a SKU sold as a set of other SKUs. The kit has no stock of
// its own; it is available while its components are, and selling or
// returning one moves the components' stock.

type bundlePart struct {
	SKU       string `json:"sku"`
	Qty       int64  `json:"qty"`
	Title     string `json:"title,omitempty"`
	Available int64  `json:"available"`
}

type bundleView struct {
	SKU        string       `json:"sku"`
	Title      string       `json:"title,omitempty"`
	Components []bundlePart `json:"components"`
	Available  int64        `json:"available"`
}

func viewBundle(t *projection.Tenant, sku string, comps []inventory.Component) bundleView {
	v := bundleView{SKU: sku, Components: []bundlePart{}}
	if vr, p, err := t.Catalog.VariantBySKU(sku); err == nil {
		v.Title = variantTitle(p.Title, vr.Title)
	}
	first := true
	for _, c := range comps {
		part := bundlePart{SKU: c.SKU, Qty: c.Qty, Available: t.Inventory.Available(c.SKU)}
		if vr, p, err := t.Catalog.VariantBySKU(c.SKU); err == nil {
			part.Title = variantTitle(p.Title, vr.Title)
		}
		if n := part.Available / c.Qty; first || n < v.Available {
			v.Available = n
		}
		first = false
		v.Components = append(v.Components, part)
	}
	return v
}

func variantTitle(product, variant string) string {
	if variant == "" || strings.EqualFold(variant, "default") || variant == product {
		return product
	}
	return product + " · " + variant
}

func (s *Server) listBundles(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	kits, err := s.DB.LoadBundles(r.Context(), principal(r).Tenant, "")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]bundleView, 0, len(kits))
	for sku, comps := range kits {
		out = append(out, viewBundle(t, sku, comps))
	}
	slices.SortFunc(out, func(a, b bundleView) int { return strings.Compare(a.SKU, b.SKU) })
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) getBundle(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	sku := r.PathValue("sku")
	comps, err := s.DB.Bundle(r.Context(), principal(r).Tenant, sku)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, viewBundle(t, sku, comps))
}

func (s *Server) putBundle(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	sku := r.PathValue("sku")
	var in struct {
		Components []inventory.Component `json:"components"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, _, err := t.Catalog.VariantBySKU(sku); err != nil {
		apierr.NotFound(w, "No existe un producto con ese SKU")
		return
	}
	var errs []apierr.FieldError
	if len(in.Components) > 30 {
		errs = append(errs, apierr.FieldError{Field: "components", Code: "too_many", Message: "Un kit tiene hasta 30 componentes"})
	}
	seen := map[string]bool{}
	for i, c := range in.Components {
		c.SKU = strings.TrimSpace(c.SKU)
		in.Components[i].SKU = c.SKU
		f := fmt.Sprintf("components[%d]", i)
		switch {
		case c.SKU == sku:
			errs = append(errs, apierr.FieldError{Field: f + ".sku", Code: "self", Message: "Un kit no puede contenerse a sí mismo"})
		case seen[c.SKU]:
			errs = append(errs, apierr.FieldError{Field: f + ".sku", Code: "duplicate", Message: "Componente repetido: suma la cantidad"})
		default:
			if _, _, err := t.Catalog.VariantBySKU(c.SKU); err != nil {
				errs = append(errs, apierr.FieldError{Field: f + ".sku", Code: "unknown", Message: "No existe el SKU " + c.SKU})
			}
		}
		if c.Qty < 1 || c.Qty > 10000 {
			errs = append(errs, apierr.FieldError{Field: f + ".qty", Code: "invalid", Message: "Cantidad de 1 a 10,000"})
		}
		seen[c.SKU] = true
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los componentes del kit", errs...)
		return
	}
	if err := s.DB.SetBundle(r.Context(), pr.Tenant, pr.Actor(), sku, in.Components); err != nil {
		if errors.Is(err, store.ErrBundle) {
			apierr.BadRequest(w, err.Error(), apierr.FieldError{Field: "components", Code: "nested", Message: err.Error()})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, viewBundle(t, sku, in.Components))
}

func (s *Server) deleteBundle(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.DB.SetBundle(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("sku"), nil); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
