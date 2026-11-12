package api

// Catalog copilot (plan IA-5): suggestions for one product, each with its
// reason, that the merchant applies or rejects; applied ones can be undone.
//
//   attributes  a filterable attribute without value whose option the title
//               or description names ("Playera roja" -> Color: Rojo)
//   category    a product without category: the category most of its
//               closest products share
//   seo         an empty SEO title or description, written from the product
//   duplicate   another product almost identical (inform only)
//
// Applying writes only that field, guarded by the product's version, and
// keeps the value before; undo restores it only if nobody changed the field
// since. Every step is audited.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

type sugSource struct {
	Text string `json:"text"`
}

func sources(texts ...string) json.RawMessage {
	var out []sugSource
	for _, t := range texts {
		out = append(out, sugSource{t})
	}
	b, _ := json.Marshal(out)
	return b
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// catalogSuggestions computes the suggestions for product p.
func (s *Server) catalogSuggestions(ctx context.Context, t *projection.Tenant, p *catalog.Product) []store.Suggestion {
	var out []store.Suggestion
	add := func(feature string, proposal any, why ...string) {
		out = append(out, store.Suggestion{Feature: feature, TargetKind: "product", TargetID: p.ID, Proposal: raw(proposal), Model: "reglas", Sources: sources(why...)})
	}
	text := p.Title + ". " + stripTags(p.Description)

	// Attributes named in the title or description.
	if attrs := map[string]string{}; true {
		var why []string
		for _, a := range t.Taxo.Attributes() {
			if !a.Filterable || len(a.Options) == 0 || strings.TrimSpace(p.Attributes[a.ID]) != "" {
				continue
			}
			var hits, said []string
			for _, o := range a.Options {
				if w, ok := optionNamed(text, o); ok {
					hits, said = append(hits, o), append(said, w)
				}
			}
			if len(hits) == 1 {
				attrs[a.ID] = hits[0]
				why = append(why, fmt.Sprintf("%s: %s, porque el texto del producto dice «%s»", a.Name, hits[0], said[0]))
			}
		}
		if len(attrs) > 0 {
			add("attributes", store.ProductField{Attributes: attrs}, why...)
		}
	}

	// Category from the closest products.
	if len(p.Categories) == 0 {
		if x := s.loadedIndex(t.ID); x != nil {
			if v, ok := x.Vector(p.ID); ok {
				hits, _ := x.Search(v, 11, nil)
				votes, n := map[string]int{}, 0
				for _, h := range hits {
					if h.ID == p.ID || h.Score < semanticFloor {
						continue
					}
					q, err := t.Catalog.Get(h.ID)
					if err != nil || len(q.Categories) == 0 {
						continue
					}
					n++
					votes[q.Categories[0]]++
				}
				best, bv := "", 0
				for c, k := range votes {
					if k > bv || (k == bv && c < best) {
						best, bv = c, k
					}
				}
				if bv >= 3 && float64(bv) >= 0.6*float64(n) {
					name := best
					if c, ok := t.Taxo.Category(best); ok {
						name = c.Name
					}
					add("category", store.ProductField{Categories: []string{best}}, fmt.Sprintf("%d de los %d productos más parecidos están en «%s»", bv, n, name))
				}
			}
		}
	}

	// SEO from the product itself.
	if strings.TrimSpace(p.SEOTitle) == "" || strings.TrimSpace(p.SEODescription) == "" {
		f := store.ProductField{}
		var why []string
		if strings.TrimSpace(p.SEOTitle) == "" {
			title := p.Title
			if p.Brand != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(p.Brand)) {
				title += " · " + p.Brand
			}
			title = cut(title, 60)
			f.SEOTitle = &title
			why = append(why, "No tiene título para buscadores: se usa el nombre del producto")
		}
		if strings.TrimSpace(p.SEODescription) == "" {
			d := ""
			if ss := ai.Sentences(stripTags(p.Description)); len(ss) > 0 {
				d = ss[0]
				if len(ss) > 1 && len(d)+len(ss[1]) < 150 {
					d += " " + ss[1]
				}
			} else {
				d = p.Title
				if p.Brand != "" {
					d += " de " + p.Brand
				}
				d += "."
			}
			d = cut(d, 155)
			f.SEODescription = &d
			why = append(why, "No tiene descripción para buscadores: se usa el inicio de la descripción")
		}
		add("seo", f, why...)
	}

	// Near duplicates: almost the same meaning and the same words.
	if x := s.loadedIndex(t.ID); x != nil {
		if v, ok := x.Vector(p.ID); ok {
			hits, _ := x.Search(v, 4, nil)
			pw := contentStems(p.Title)
			for _, h := range hits {
				if h.ID == p.ID || h.Score < 0.97 {
					continue
				}
				q, err := t.Catalog.Get(h.ID)
				if err != nil || q.Status == "archived" {
					continue
				}
				qw := contentStems(q.Title)
				inter, union := 0, len(qw)
				for w := range pw {
					if qw[w] {
						inter++
					} else {
						union++
					}
				}
				if union > 0 && float64(inter)/float64(union) >= 0.8 {
					add("duplicate", map[string]string{"productId": q.ID, "title": q.Title},
						fmt.Sprintf("«%s» es casi igual (%.0f %% de parecido); revisa si es el mismo producto", q.Title, h.Score*100))
				}
			}
		}
	}
	return out
}

// optionNamed reports whether text names an option value as a whole word,
// in any gender or number ("rojo" ~ "roja", "rojos").
func optionNamed(text, option string) (string, bool) {
	words := ai.Words(text)
	tw := map[string]bool{}
	for _, w := range words {
		tw[w] = true
	}
	ow := ai.Words(option)
	if len(ow) == 0 {
		return "", false
	}
	if len(ow) > 1 {
		phrase := strings.Join(ow, " ")
		return option, strings.Contains(" "+strings.Join(words, " ")+" ", " "+phrase+" ")
	}
	w := ow[0]
	forms := []string{w, w + "s", w + "es"}
	if n := len(w); n > 2 && (w[n-1] == 'o' || w[n-1] == 'a') {
		other := w[:n-1] + map[byte]string{'o': "a", 'a': "o"}[w[n-1]]
		forms = append(forms, other, other+"s")
	}
	for _, f := range forms {
		if tw[f] {
			return f, true
		}
	}
	return "", false
}

func cut(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	c := string(r[:n-1])
	if i := strings.LastIndex(c, " "); i > n/2 {
		c = c[:i]
	}
	return strings.TrimRight(c, " ,.;:") + "…"
}

// ---------- HTTP ----------

func (s *Server) productSuggestions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	pr, err := t.Catalog.Get(r.PathValue("id"))
	if err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	// Fresh proposals are stored (once); then the list shows all states.
	if _, err := s.DB.ProposeSuggestions(r.Context(), p.Tenant, s.catalogSuggestions(r.Context(), t, &pr)); err != nil {
		internalError(w, s.Log, err)
		return
	}
	list, err := s.DB.Suggestions(r.Context(), p.Tenant, "product", pr.ID, "", "", 50)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

// currentFields reads the fields a suggestion touches, as they are now.
func currentFields(p catalog.Product, f store.ProductField) store.ProductField {
	var cur store.ProductField
	if f.Attributes != nil {
		cur.Attributes = map[string]string{}
		for k, v := range p.Attributes {
			cur.Attributes[k] = v
		}
	}
	if f.Categories != nil {
		cur.Categories = append([]string{}, p.Categories...)
		if cur.Categories == nil {
			cur.Categories = []string{}
		}
	}
	if f.SEOTitle != nil {
		v := p.SEOTitle
		cur.SEOTitle = &v
	}
	if f.SEODescription != nil {
		v := p.SEODescription
		cur.SEODescription = &v
	}
	return cur
}

func (s *Server) applySuggestion(w http.ResponseWriter, r *http.Request) {
	s.suggestionAction(w, r, "applied")
}

func (s *Server) undoSuggestion(w http.ResponseWriter, r *http.Request) {
	s.suggestionAction(w, r, "undone")
}

func (s *Server) rejectSuggestion(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.DB.DecideSuggestion(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), "rejected"); err != nil {
		apierr.NotFound(w, "La sugerencia no existe o ya se decidió")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) suggestionAction(w http.ResponseWriter, r *http.Request, to string) {
	p := principal(r)
	sg, err := s.DB.Suggestion(r.Context(), p.Tenant, r.PathValue("id"))
	if err != nil {
		apierr.NotFound(w, "Sugerencia no encontrada")
		return
	}
	if sg.TargetKind != "product" || sg.Feature == "duplicate" {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "id", Code: "not_applicable", Message: "Esta sugerencia solo informa; márcala como revisada o recházala"})
		return
	}
	want := map[string]string{"applied": "proposed", "undone": "applied"}[to]
	if sg.Status != want {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "state", Detail: "La sugerencia ya no está en un estado que lo permita"})
		return
	}
	t, _ := s.Proj.Tenant(p.Tenant)
	pr, err := t.Catalog.Get(sg.TargetID)
	if err != nil {
		apierr.NotFound(w, "El producto ya no existe")
		return
	}
	var prop store.ProductField
	_ = json.Unmarshal(sg.Proposal, &prop)
	var set store.ProductField
	var before json.RawMessage
	if to == "applied" {
		cur := currentFields(pr, prop)
		before = raw(cur)
		set = prop
		if prop.Attributes != nil { // merge: keep the other attributes
			merged := map[string]string{}
			for k, v := range cur.Attributes {
				merged[k] = v
			}
			for k, v := range prop.Attributes {
				merged[k] = v
			}
			set.Attributes = merged
		}
	} else {
		// Undo only if the fields still hold what was applied.
		var prev store.ProductField
		_ = json.Unmarshal(sg.Before, &prev)
		now := currentFields(pr, prop)
		applied := prop
		if prop.Attributes != nil {
			applied.Attributes = map[string]string{}
			for k, v := range prev.Attributes {
				applied.Attributes[k] = v
			}
			for k, v := range prop.Attributes {
				applied.Attributes[k] = v
			}
		}
		if !reflect.DeepEqual(raw(now), raw(applied)) {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "stale", Detail: "El producto cambió después de aplicar la sugerencia; deshazlo a mano para no perder ese cambio"})
			return
		}
		set = prev
		if set.Attributes == nil && prop.Attributes != nil {
			set.Attributes = map[string]string{}
		}
		if set.Categories == nil && prop.Categories != nil {
			set.Categories = []string{}
		}
	}
	v, err := s.DB.ApplyProductSuggestion(r.Context(), p.Tenant, p.Actor(), sg.ID, pr.Version, set, before, to)
	if errors.Is(err, store.ErrStale) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "stale", Detail: "El producto cambió mientras tanto; vuelve a intentarlo"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"status": to, "version": v})
}

// scanCatalog proposes suggestions for many products at once (the panel's
// "Revisar catálogo"): at most 2 000 per call, products without decisions first.
func (s *Server) scanCatalog(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	ps := t.Catalog.Products()
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	var all []store.Suggestion
	for i, pr := range ps {
		if i >= 2000 {
			break
		}
		all = append(all, s.catalogSuggestions(r.Context(), t, pr)...)
	}
	n, err := s.DB.ProposeSuggestions(r.Context(), p.Tenant, all)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	counts := map[string]int{}
	for _, sg := range all {
		counts[sg.Feature]++
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"new": n, "byFeature": counts})
}

func (s *Server) listSuggestions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "proposed"
	}
	list, err := s.DB.Suggestions(r.Context(), p.Tenant, "product", "", q.Get("feature"), status, 200)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	t, _ := s.Proj.Tenant(p.Tenant)
	type row struct {
		store.Suggestion
		Title string `json:"title"`
	}
	out := make([]row, 0, len(list))
	for _, sg := range list {
		title := "(producto eliminado)"
		if pr, err := t.Catalog.Get(sg.TargetID); err == nil {
			title = pr.Title
		}
		out = append(out, row{sg, title})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) routesCopilots(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/products/{id}/suggestions", s.staff("catalog", auth.Read, s.productSuggestions))
	handle("GET /v1/ai/suggestions", s.staff("catalog", auth.Read, s.listSuggestions))
	handle("POST /v1/ai/suggestions/scan", s.staff("catalog", auth.Edit, s.scanCatalog))
	handle("POST /v1/ai/suggestions/{id}/apply", s.staff("catalog", auth.Edit, s.applySuggestion))
	handle("POST /v1/ai/suggestions/{id}/undo", s.staff("catalog", auth.Edit, s.undoSuggestion))
	handle("POST /v1/ai/suggestions/{id}/reject", s.staff("catalog", auth.Edit, s.rejectSuggestion))
}
