package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Quantity rules of a product: minimum, maximum and multiples per order, in
// general or for the companies of one price group.

func (s *Server) getQtyRules(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	if _, err := t.Catalog.Get(r.PathValue("id")); err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	var rules []cart.QtyRule
	if _, err := s.DB.GetDoc(r.Context(), t.ID, "qty_rules", r.PathValue("id"), &rules); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if rules == nil {
		rules = []cart.QtyRule{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) putQtyRules(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	t, ok := s.Proj.Tenant(pr.Tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	id := r.PathValue("id")
	if _, err := t.Catalog.Get(id); err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	var body struct {
		Rules []cart.QtyRule `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Rules) > 20 {
		apierr.BadRequest(w, "", fe("rules", "too_many", "Hasta 20 reglas por producto"))
		return
	}
	seen := map[string]bool{}
	clean := []cart.QtyRule{}
	for i, x := range body.Rules {
		x.Group = strings.TrimSpace(x.Group)
		p := "rules." + strconv.Itoa(i)
		switch {
		case seen[x.Group]:
			apierr.BadRequest(w, "", fe(p+".group", "duplicate", "Ya hay una regla para ese grupo"))
			return
		case len(x.Group) > 60 || strings.ContainsAny(x.Group, " \t"):
			apierr.BadRequest(w, "", fe(p+".group", "invalid", "El grupo es una sola palabra"))
			return
		case x.Min < 0 || x.Max < 0 || x.Step < 0 || x.Min > 1_000_000 || x.Max > 1_000_000 || x.Step > 10_000:
			apierr.BadRequest(w, "", fe(p, "out_of_range", "Usa números positivos razonables"))
			return
		case x.Max > 0 && x.Max < x.Min:
			apierr.BadRequest(w, "", fe(p+".max", "invalid", "El máximo no puede ser menor que el mínimo"))
			return
		case x.Max > 0 && x.Step > 1 && x.Max < x.Step:
			apierr.BadRequest(w, "", fe(p+".max", "invalid", "El máximo debe permitir al menos un múltiplo"))
			return
		case x.Min <= 1 && x.Max == 0 && x.Step <= 1:
			continue // a rule that allows anything is not stored
		}
		seen[x.Group] = true
		clean = append(clean, x)
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "qty_rules", id, store.KeyQtyRules, clean); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"rules": clean})
}
