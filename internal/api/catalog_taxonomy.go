package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// taxonomyError maps store errors of the organization endpoints to problems.
// It reports whether it wrote a response.
func (s *Server) taxonomyError(w http.ResponseWriter, err error, what string) bool {
	var ref *store.RefError
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, what+" no existe")
	case errors.Is(err, store.ErrConflict):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Ya existe " + strings.ToLower(what) + " con esa dirección o identificador"})
	case errors.Is(err, store.ErrCycle):
		apierr.BadRequest(w, "", fe("parentId", "cycle", "Una categoría no puede quedar dentro de sí misma"))
	case errors.Is(err, store.ErrHasChildren):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "has_children", Detail: "Mueve o elimina primero sus subcategorías"})
	case errors.As(err, &ref):
		apierr.BadRequest(w, "", fe(ref.Field, "not_found", "No existe: "+ref.ID))
	default:
		internalError(w, s.Log, err)
	}
	return true
}

// handleOr returns the given handle or a slug of the name.
func handleOr(handle, name string) string {
	if h := strings.TrimSpace(handle); h != "" {
		return h
	}
	return catalog.Slug(name)
}

func seoErrors(title, desc string) []apierr.FieldError {
	var errs []apierr.FieldError
	if len(title) > 120 {
		errs = append(errs, fe("seoTitle", "too_long", "El título SEO admite hasta 120 caracteres"))
	}
	if len(desc) > 320 {
		errs = append(errs, fe("seoDescription", "too_long", "La descripción SEO admite hasta 320 caracteres"))
	}
	return errs
}

// ---------- categories ----------

type categoryView struct {
	catalog.Category
	Products      int `json:"products"`      // directly in this category
	ProductsTotal int `json:"productsTotal"` // including subcategories
	Depth         int `json:"depth"`
}

func categoryCounts(t *projection.Tenant) (direct, total map[string]int) {
	direct, total = map[string]int{}, map[string]int{}
	for _, p := range t.Catalog.Products() {
		for _, raw := range p.Categories {
			if id, ok := t.Taxo.ResolveCategory(raw); ok {
				direct[id]++
			}
		}
		for id := range t.Taxo.ProductCategories(p) {
			total[id]++
		}
	}
	return direct, total
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	direct, total := categoryCounts(t)
	cats := t.Taxo.Categories()
	depth := map[string]int{}
	out := make([]categoryView, 0, len(cats))
	for _, c := range cats {
		d := 0
		if c.ParentID != "" {
			d = depth[c.ParentID] + 1
		}
		depth[c.ID] = d
		out = append(out, categoryView{Category: c, Products: direct[c.ID], ProductsTotal: total[c.ID], Depth: d})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func validateCategory(c *catalog.Category) []apierr.FieldError {
	c.Name = strings.TrimSpace(c.Name)
	c.Handle = handleOr(c.Handle, c.Name)
	var errs []apierr.FieldError
	if c.Name == "" || len(c.Name) > 120 {
		errs = append(errs, fe("name", "required", "Escribe un nombre de hasta 120 caracteres"))
	}
	if !catalog.ValidHandle(c.Handle) {
		errs = append(errs, fe("handle", "invalid", "La dirección solo admite minúsculas, números y guiones"))
	}
	if len(c.Description) > 5000 {
		errs = append(errs, fe("description", "too_long", "La descripción admite hasta 5,000 caracteres"))
	}
	return append(errs, seoErrors(c.SEOTitle, c.SEODescription)...)
}

func (s *Server) saveCategory(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var c catalog.Category
	if !decode(w, r, &c) {
		return
	}
	c.ID = id
	if errs := validateCategory(&c); len(errs) > 0 {
		apierr.BadRequest(w, "La categoría tiene errores", errs...)
		return
	}
	pr := principal(r)
	if s.taxonomyError(w, s.DB.SaveCategory(r.Context(), pr.Tenant, pr.Actor(), c, create), "La categoría") {
		return
	}
	s.settle(r.Context())
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	if t, ok := s.Proj.Tenant(pr.Tenant); ok {
		if saved, found := t.Taxo.Category(id); found {
			c = saved
		}
	}
	httpx.JSON(w, r, status, c)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	s.saveCategory(w, r, store.NewID("cat_"), true)
}

func (s *Server) updateCategory(w http.ResponseWriter, r *http.Request) {
	s.saveCategory(w, r, r.PathValue("id"), false)
}

func (s *Server) reorderCategories(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Moves []store.CategoryMove `json:"moves"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Moves) == 0 || len(body.Moves) > 2000 {
		apierr.BadRequest(w, "", fe("moves", "out_of_range", "Envía entre 1 y 2000 movimientos"))
		return
	}
	for _, m := range body.Moves {
		if m.Position < 0 || m.Position > 100000 || m.ID == "" {
			apierr.BadRequest(w, "", fe("moves", "invalid", "Cada movimiento necesita id y una posición válida"))
			return
		}
	}
	pr := principal(r)
	if s.taxonomyError(w, s.DB.ReorderCategories(r.Context(), pr.Tenant, pr.Actor(), body.Moves), "La categoría") {
		return
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.taxonomyError(w, s.DB.DeleteCategory(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")), "La categoría") {
		return
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ---------- brands ----------

type brandView struct {
	catalog.Brand
	Products int        `json:"products"`
	Logo     *mediaView `json:"logo"`
}

func (s *Server) listBrands(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	counts := map[string]int{}
	for _, p := range t.Catalog.Products() {
		id := p.BrandID
		if id == "" && p.Brand != "" {
			if b, found := t.Taxo.BrandByName(p.Brand); found {
				id = b.ID
			}
		}
		if id != "" {
			counts[id]++
		}
	}
	bs := t.Taxo.Brands()
	out := make([]brandView, 0, len(bs))
	for _, b := range bs {
		v := brandView{Brand: b, Products: counts[b.ID]}
		if m, found := t.Taxo.Media(b.LogoMediaID); found {
			mv := viewMedia(t.ID, m)
			v.Logo = &mv
		}
		out = append(out, v)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) saveBrand(w http.ResponseWriter, r *http.Request, id string, create bool) {
	var b catalog.Brand
	if !decode(w, r, &b) {
		return
	}
	b.ID, b.Name = id, strings.TrimSpace(b.Name)
	b.Handle = handleOr(b.Handle, b.Name)
	var errs []apierr.FieldError
	if b.Name == "" || len(b.Name) > 120 {
		errs = append(errs, fe("name", "required", "Escribe un nombre de hasta 120 caracteres"))
	}
	if !catalog.ValidHandle(b.Handle) {
		errs = append(errs, fe("handle", "invalid", "La dirección solo admite minúsculas, números y guiones"))
	}
	if len(b.Description) > 5000 {
		errs = append(errs, fe("description", "too_long", "La descripción admite hasta 5,000 caracteres"))
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "La marca tiene errores", errs...)
		return
	}
	pr := principal(r)
	if s.taxonomyError(w, s.DB.SaveBrand(r.Context(), pr.Tenant, pr.Actor(), b, create), "La marca") {
		return
	}
	s.settle(r.Context())
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, b)
}

func (s *Server) createBrand(w http.ResponseWriter, r *http.Request) {
	s.saveBrand(w, r, store.NewID("br_"), true)
}

func (s *Server) updateBrand(w http.ResponseWriter, r *http.Request) {
	s.saveBrand(w, r, r.PathValue("id"), false)
}

func (s *Server) deleteBrand(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.taxonomyError(w, s.DB.DeleteBrand(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")), "La marca") {
		return
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ---------- collections ----------

type collectionView struct {
	catalog.Collection
	Products int `json:"products"`
}

// collectionMembers returns the products of c in any status, in its order.
func collectionMembers(t *projection.Tenant, c *catalog.Collection) []*catalog.Product {
	of := offerOf(t, defaultChannel)
	if c.Kind == catalog.CollectionManual {
		out := make([]*catalog.Product, 0, len(c.ProductIDs))
		for _, id := range c.ProductIDs {
			if p, err := t.Catalog.Get(id); err == nil {
				out = append(out, &p)
			}
		}
		return out
	}
	var out []*catalog.Product
	for _, p := range t.Catalog.Products() {
		if t.Taxo.MatchesCollection(c, p, t.Taxo.ProductCategories(p), of(p)) {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	cs := t.Taxo.Collections()
	out := make([]collectionView, 0, len(cs))
	for i := range cs {
		out = append(out, collectionView{Collection: cs[i], Products: len(collectionMembers(t, &cs[i]))})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) collectionDetail(t *projection.Tenant, c *catalog.Collection) map[string]any {
	members := collectionMembers(t, c)
	rows := make([]any, 0, min(len(members), 100))
	for i, p := range members {
		if i == 100 {
			break
		}
		rows = append(rows, httpx.Project(s.rowOf(t, p), []string{"id", "title", "handle", "status", "brand", "priceMin", "currency", "stock", "thumb"}))
	}
	return map[string]any{"collection": c, "products": rows, "total": len(members)}
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	c, found := t.Taxo.Collection(r.PathValue("id"))
	if !found {
		apierr.NotFound(w, "La colección no existe")
		return
	}
	httpx.JSON(w, r, http.StatusOK, s.collectionDetail(t, &c))
}

func validateCollection(t *projection.Tenant, c *catalog.Collection) []apierr.FieldError {
	c.Title = strings.TrimSpace(c.Title)
	c.Handle = handleOr(c.Handle, c.Title)
	var errs []apierr.FieldError
	if c.Title == "" || len(c.Title) > 120 {
		errs = append(errs, fe("title", "required", "Escribe un nombre de hasta 120 caracteres"))
	}
	if !catalog.ValidHandle(c.Handle) {
		errs = append(errs, fe("handle", "invalid", "La dirección solo admite minúsculas, números y guiones"))
	}
	if len(c.Description) > 5000 {
		errs = append(errs, fe("description", "too_long", "La descripción admite hasta 5,000 caracteres"))
	}
	errs = append(errs, seoErrors(c.SEOTitle, c.SEODescription)...)
	switch c.Kind {
	case catalog.CollectionManual:
		c.Rules = catalog.CollectionRuleSet{}
		if len(c.ProductIDs) > 1000 {
			errs = append(errs, fe("productIds", "too_many", "Una colección manual admite hasta 1,000 productos"))
		}
		seen := map[string]bool{}
		ids := make([]string, 0, len(c.ProductIDs))
		for _, id := range c.ProductIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, err := t.Catalog.Get(id); err != nil {
				errs = append(errs, fe("productIds", "not_found", "El producto "+id+" no existe"))
				continue
			}
			ids = append(ids, id)
		}
		c.ProductIDs = ids
	case catalog.CollectionRules:
		c.ProductIDs = nil
		r := &c.Rules
		if r.Match != "any" {
			r.Match = "all"
		}
		if r.Empty() {
			errs = append(errs, fe("rules", "required", "Agrega al menos una regla: marca, categoría, etiqueta o rango de precio"))
		}
		for _, id := range r.BrandIDs {
			if _, found := t.Taxo.Brand(id); !found {
				errs = append(errs, fe("rules.brandIds", "not_found", "Una marca de las reglas ya no existe"))
			}
		}
		for _, id := range r.CategoryIDs {
			if _, found := t.Taxo.Category(id); !found {
				errs = append(errs, fe("rules.categoryIds", "not_found", "Una categoría de las reglas ya no existe"))
			}
		}
		var ok bool
		if r.Tags, ok = cleanList(r.Tags, 20, 60); !ok {
			errs = append(errs, fe("rules.tags", "invalid", "Hasta 20 etiquetas de 60 caracteres"))
		}
		if len(r.BrandIDs) > 50 || len(r.CategoryIDs) > 50 {
			errs = append(errs, fe("rules", "too_many", "Hasta 50 marcas y 50 categorías por colección"))
		}
		if (r.PriceMin != nil && *r.PriceMin < 0) || (r.PriceMax != nil && *r.PriceMax < 0) ||
			(r.PriceMin != nil && r.PriceMax != nil && *r.PriceMin > *r.PriceMax) {
			errs = append(errs, fe("rules.priceMin", "invalid", "El precio mínimo no puede ser mayor que el máximo"))
		}
	default:
		errs = append(errs, fe("kind", "invalid", "kind: manual o rules"))
	}
	return errs
}

func (s *Server) saveCollection(w http.ResponseWriter, r *http.Request, id string, create bool) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var c catalog.Collection
	if !decode(w, r, &c) {
		return
	}
	c.ID = id
	if errs := validateCollection(t, &c); len(errs) > 0 {
		apierr.BadRequest(w, "La colección tiene errores", errs...)
		return
	}
	pr := principal(r)
	if s.taxonomyError(w, s.DB.SaveCollection(r.Context(), pr.Tenant, pr.Actor(), c, create), "La colección") {
		return
	}
	s.settle(r.Context())
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, s.collectionDetail(t, &c))
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	s.saveCollection(w, r, store.NewID("col_"), true)
}

func (s *Server) updateCollection(w http.ResponseWriter, r *http.Request) {
	s.saveCollection(w, r, r.PathValue("id"), false)
}

// previewCollection resolves a collection that is not saved yet, so the
// editor shows which products the rules select before saving.
func (s *Server) previewCollection(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var c catalog.Collection
	if !decode(w, r, &c) {
		return
	}
	if c.Title == "" {
		c.Title = "vista previa"
	}
	if c.Kind == catalog.CollectionRules && c.Rules.Empty() {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"collection": c, "products": []any{}, "total": 0})
		return
	}
	if errs := validateCollection(t, &c); len(errs) > 0 {
		apierr.BadRequest(w, "La colección tiene errores", errs...)
		return
	}
	httpx.JSON(w, r, http.StatusOK, s.collectionDetail(t, &c))
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.taxonomyError(w, s.DB.DeleteCollection(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")), "La colección") {
		return
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ---------- attributes ----------

type attributeView struct {
	catalog.AttributeDef
	Products int `json:"products"`
}

func (s *Server) listAttributes(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	counts := map[string]int{}
	for _, p := range t.Catalog.Products() {
		for k, v := range p.Attributes {
			if v != "" {
				counts[k]++
			}
		}
	}
	as := t.Taxo.Attributes()
	out := make([]attributeView, 0, len(as))
	for _, a := range as {
		out = append(out, attributeView{AttributeDef: a, Products: counts[a.ID]})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

var attrIDRe = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

func (s *Server) saveAttribute(w http.ResponseWriter, r *http.Request, id string, create bool) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var a catalog.AttributeDef
	if !decode(w, r, &a) {
		return
	}
	a.Name, a.Unit = strings.TrimSpace(a.Name), strings.TrimSpace(a.Unit)
	if create {
		id = strings.TrimSpace(a.ID)
		if id == "" {
			id = strings.ReplaceAll(catalog.Slug(a.Name), "-", "_")
		}
	}
	a.ID = id
	var errs []apierr.FieldError
	if a.Name == "" || len(a.Name) > 80 {
		errs = append(errs, fe("name", "required", "Escribe un nombre de hasta 80 caracteres"))
	}
	if !attrIDRe.MatchString(a.ID) || len(a.ID) > 40 {
		errs = append(errs, fe("id", "invalid", "El identificador admite minúsculas, números y guion bajo (por ejemplo material o largo_cm)"))
	}
	if old, found := t.Taxo.Attribute(a.ID); found && !create {
		if a.Kind == "" {
			a.Kind = old.Kind
		}
		if a.Kind != old.Kind {
			errs = append(errs, fe("kind", "immutable", "El tipo de un atributo no se puede cambiar; crea uno nuevo"))
		}
	}
	switch a.Kind {
	case catalog.AttrText, catalog.AttrBoolean:
		a.Options, a.Unit = nil, ""
	case catalog.AttrNumber:
		a.Options = nil
		if len(a.Unit) > 12 {
			errs = append(errs, fe("unit", "too_long", "La unidad admite hasta 12 caracteres"))
		}
	case catalog.AttrSelect:
		a.Unit = ""
		var okList bool
		if a.Options, okList = cleanList(a.Options, 100, 60); !okList || len(a.Options) == 0 {
			errs = append(errs, fe("options", "required", "Una lista necesita entre 1 y 100 opciones de hasta 60 caracteres"))
		}
	default:
		errs = append(errs, fe("kind", "invalid", "kind: text, number, boolean o select"))
	}
	if a.Position < 0 || a.Position > 100000 {
		errs = append(errs, fe("position", "out_of_range", "Posición inválida"))
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "El atributo tiene errores", errs...)
		return
	}
	pr := principal(r)
	if s.taxonomyError(w, s.DB.SaveAttribute(r.Context(), pr.Tenant, pr.Actor(), a, create), "El atributo") {
		return
	}
	s.settle(r.Context())
	if saved, found := t.Taxo.Attribute(a.ID); found {
		a = saved
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, a)
}

func (s *Server) createAttribute(w http.ResponseWriter, r *http.Request) {
	s.saveAttribute(w, r, "", true)
}

func (s *Server) updateAttribute(w http.ResponseWriter, r *http.Request) {
	s.saveAttribute(w, r, r.PathValue("id"), false)
}

func (s *Server) deleteAttribute(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.taxonomyError(w, s.DB.DeleteAttribute(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")), "El atributo") {
		return
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ---------- types and tags ----------

func termKind(w http.ResponseWriter, r *http.Request) (string, bool) {
	switch k := r.PathValue("kind"); k {
	case "types":
		return "type", true
	case "tags":
		return "tag", true
	}
	apierr.NotFound(w, "Vocabulario desconocido; usa types o tags")
	return "", false
}

func (s *Server) listTerms(w http.ResponseWriter, r *http.Request) {
	kind, ok := termKind(w, r)
	if !ok {
		return
	}
	ts, err := s.DB.Terms(r.Context(), principal(r).Tenant, kind)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if ts == nil {
		ts = []store.Term{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ts})
}

func (s *Server) addTerm(w http.ResponseWriter, r *http.Request) {
	kind, ok := termKind(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 60 || strings.ContainsAny(name, ",|") {
		apierr.BadRequest(w, "", fe("name", "invalid", "Escribe un nombre de hasta 60 caracteres, sin comas"))
		return
	}
	pr := principal(r)
	if err := s.DB.AddTerm(r.Context(), pr.Tenant, pr.Actor(), kind, name); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"name": name})
}

func (s *Server) deleteTerm(w http.ResponseWriter, r *http.Request) {
	kind, ok := termKind(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		apierr.BadRequest(w, "", fe("name", "required", "Indica el nombre en ?name="))
		return
	}
	pr := principal(r)
	n, err := s.DB.DeleteTerm(r.Context(), pr.Tenant, pr.Actor(), kind, name)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.settle(r.Context())
	httpx.JSON(w, r, http.StatusOK, map[string]any{"products": n})
}
