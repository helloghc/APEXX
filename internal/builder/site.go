package builder

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors returned by Site.
var (
	ErrNotFound   = errors.New("builder: not found")
	ErrBlocked    = errors.New("builder: validation issues block publishing")
	ErrNoPrevious = errors.New("builder: no previous release to roll back to")
	ErrStaleDraft = errors.New("builder: draft changed since it was read")
)

// Card is a product as a section sees it.
type Card struct {
	Title     string
	URL       string
	Price     string
	CompareAt string
	Brand     string
	Initial   string
	Tone      int    // 1..6, picks a placeholder art variation
	SKU       string // first sellable variant, for quick add
	SoldOut   bool
	Image     template.HTML // cover image markup (srcset, width, height, alt); empty shows the placeholder
}

// Data gives sections access to commerce data at render time.
type Data interface {
	Products(limit int) []Card
}

// ProductQuery filters products for grids and carousels.
type ProductQuery struct {
	Limit    int
	Brand    string
	Category string
	Sort     string // newest | price-asc | price-desc
}

// Querier is optionally implemented by Data for filtered product lists.
type Querier interface {
	Query(q ProductQuery) []Card
}

// ToneOf returns a stable 1..6 placeholder variation for a title.
func ToneOf(s string) int {
	h := 0
	for _, r := range s {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return h%6 + 1
}

// Library holds the section definitions available to a site.
type Library struct {
	defs  map[string]SectionDefinition
	tmpls map[string]*template.Template
}

// NewLibrary compiles the templates of defs.
func NewLibrary(defs ...SectionDefinition) (*Library, error) {
	l := &Library{defs: map[string]SectionDefinition{}, tmpls: map[string]*template.Template{}}
	for _, d := range defs {
		t, err := template.New(d.Type).Funcs(sectionFuncs).Parse(partials + d.Template)
		if err != nil {
			return nil, fmt.Errorf("section %s: %w", d.Type, err)
		}
		l.defs[d.Type] = d
		l.tmpls[d.Type] = t
	}
	return l, nil
}

// Definitions lists section types sorted by category and name.
func (l *Library) Definitions() []SectionDefinition {
	out := make([]SectionDefinition, 0, len(l.defs))
	for _, d := range l.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// RendererVersion changes whenever the generated HTML changes.
const RendererVersion = "3"

// Persister stores builder state durably. A failed save aborts the change.
type Persister interface {
	SaveDraft(handle string, t Template, version int64) error
	SaveRevision(r *Revision) error
	SaveRelease(rel Release) error
}

// Site is one tenant's storefront: an editable draft plus published revisions.
type Site struct {
	ID   string
	lib  *Library
	data Data
	now  func() time.Time

	mu        sync.Mutex
	draft     map[string]Template // handle -> template
	draftVer  int64
	revisions map[string]*Revision
	releases  []Release
	live      *Revision // swapped atomically under mu; readers copy the pointer
	persist   Persister
}

// SetPersister enables durable storage.
func (s *Site) SetPersister(p Persister) { s.persist = p }

// Restore loads persisted state (drafts, revisions and release history).
func (s *Site) Restore(drafts map[string]Template, version int64, revs []*Revision, rels []Release) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, t := range drafts {
		s.draft[h] = t
	}
	s.draftVer = version
	for _, r := range revs {
		s.revisions[r.ID] = r
	}
	s.releases = rels
	if n := len(rels); n > 0 {
		s.live = s.revisions[rels[n-1].RevisionID]
	}
}

// Templates returns the current draft templates.
func (s *Site) Templates() map[string]Template {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Template, len(s.draft))
	for h, t := range s.draft {
		out[h] = t
	}
	return out
}

// NewSite creates a site with an initial draft.
func NewSite(id string, lib *Library, data Data, templates ...Template) *Site {
	s := &Site{ID: id, lib: lib, data: data, now: time.Now, draft: map[string]Template{}, revisions: map[string]*Revision{}}
	for _, t := range templates {
		s.draft[t.Handle] = t
	}
	return s
}

// Draft returns the current draft of a template and its version for
// optimistic concurrency.
func (s *Site) Draft(handle string) (Template, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.draft[handle]
	if !ok {
		return Template{}, 0, ErrNotFound
	}
	return t, s.draftVer, nil
}

// Patch applies JSON Patch ops to a draft template and returns its report.
// ifVersion (if non-zero) must match the current draft version.
func (s *Site) Patch(handle string, ops []Op, ifVersion int64) (Template, Report, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.draft[handle]
	if !ok {
		return Template{}, Report{}, 0, ErrNotFound
	}
	if ifVersion != 0 && ifVersion != s.draftVer {
		return Template{}, Report{}, 0, ErrStaleDraft
	}
	raw, _ := json.Marshal(t)
	var doc any
	_ = json.Unmarshal(raw, &doc)
	doc, err := ApplyPatch(doc, ops)
	if err != nil {
		return Template{}, Report{}, 0, err
	}
	b, _ := json.Marshal(doc)
	var nt Template
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&nt); err != nil {
		return Template{}, Report{}, 0, fmt.Errorf("patched template is invalid: %w", err)
	}
	if nt.Handle != handle {
		return Template{}, Report{}, 0, errors.New("the handle cannot be changed with a patch")
	}
	if s.persist != nil {
		if err := s.persist.SaveDraft(handle, nt, s.draftVer+1); err != nil {
			if errors.Is(err, ErrStaleDraft) {
				return Template{}, Report{}, 0, ErrStaleDraft
			}
			return Template{}, Report{}, 0, fmt.Errorf("builder: save draft: %w", err)
		}
	}
	s.draft[handle] = nt
	s.draftVer++
	_, rep := s.render(nt)
	return nt, rep, s.draftVer, nil
}

// Preview renders the draft of a template without publishing.
func (s *Site) Preview(handle string) ([]byte, Report, error) {
	s.mu.Lock()
	t, ok := s.draft[handle]
	s.mu.Unlock()
	if !ok {
		return nil, Report{}, ErrNotFound
	}
	html, rep := s.render(t)
	return html, rep, nil
}

// Publish freezes the draft as an immutable revision, renders every page
// and switches the live pointer. Nothing changes if any page is blocked.
func (s *Site) Publish(by, message string) (Release, map[string]Report, error) {
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	rev := &Revision{
		CreatedAt: s.now().UTC(), CreatedBy: by, Message: message,
		Templates: make(map[string]Template, len(s.draft)),
		Reports:   map[string]Report{}, Pages: map[string][]byte{},
	}
	blocked := false
	for h, t := range s.draft {
		r := s.renderFull(t)
		rev.Templates[h] = t
		rev.Reports[h] = r.Report
		rev.Pages[h] = r.Doc
		blocked = blocked || r.Report.Blocking()
		if h == "index" {
			// Shared chrome for the shopper pages (catalog, product, cart...).
			rev.Pages[chromeHead] = []byte(r.Theme.Head())
			rev.Pages[chromeBody] = []byte(r.Theme.BodyClass())
			rev.Pages[chromeHeader] = r.Header
			rev.Pages[chromeFooter] = r.Footer
		}
	}
	if blocked {
		return Release{}, rev.Reports, ErrBlocked
	}
	snap, _ := json.Marshal(rev.Templates)
	// The renderer version is part of the identity of a revision: after a
	// platform upgrade that changes the HTML, publishing again re-renders.
	sum := sha256.Sum256(append([]byte(RendererVersion+"|"), snap...))
	rev.Checksum = hex.EncodeToString(sum[:])
	rev.ID = "rev_" + rev.Checksum[:12]
	if s.live != nil {
		if s.live.Checksum == rev.Checksum {
			return s.releases[len(s.releases)-1], rev.Reports, nil // nothing changed
		}
		rev.ParentID = s.live.ID
	}
	rel := Release{RevisionID: rev.ID, PublishedAt: s.now().UTC(), PublishedBy: by}
	if s.persist != nil {
		if _, exists := s.revisions[rev.ID]; !exists {
			if err := s.persist.SaveRevision(rev); err != nil {
				return Release{}, rev.Reports, fmt.Errorf("builder: save revision: %w", err)
			}
		}
	}
	rel.DurationMicros = time.Since(start).Microseconds()
	if s.persist != nil {
		if err := s.persist.SaveRelease(rel); err != nil {
			return Release{}, rev.Reports, fmt.Errorf("builder: save release: %w", err)
		}
	}
	s.revisions[rev.ID] = rev
	s.live = rev
	s.releases = append(s.releases, rel)
	return rel, rev.Reports, nil
}

// Rollback makes the previous release live again. HTML is reused, so it only
// moves the pointer.
func (s *Site) Rollback(by string) (Release, error) {
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.releases) < 2 {
		return Release{}, ErrNoPrevious
	}
	cur := s.releases[len(s.releases)-1]
	prev := s.releases[len(s.releases)-2]
	rel := Release{RevisionID: prev.RevisionID, PublishedAt: s.now().UTC(), PublishedBy: by,
		RolledBackFrom: cur.RevisionID, DurationMicros: time.Since(start).Microseconds()}
	if s.persist != nil {
		if err := s.persist.SaveRelease(rel); err != nil {
			return Release{}, fmt.Errorf("builder: save release: %w", err)
		}
	}
	s.live = s.revisions[prev.RevisionID]
	s.releases = append(s.releases, rel)
	return rel, nil
}

// Live returns the published HTML of a page.
func (s *Site) Live(handle string) ([]byte, string, error) {
	s.mu.Lock()
	rev := s.live
	s.mu.Unlock()
	if rev == nil {
		return nil, "", ErrNotFound
	}
	html, ok := rev.Pages[handle]
	if !ok || strings.HasPrefix(handle, "__") {
		return nil, "", ErrNotFound
	}
	return html, rev.ID, nil
}

// Keys of the shared chrome stored with each revision.
const (
	chromeHead   = "__head"
	chromeBody   = "__bodyclass"
	chromeHeader = "__header"
	chromeFooter = "__footer"
)

// Chrome is the theme head, body classes, header and footer of the live site,
// so pages outside the builder look like the rest of the store.
type Chrome struct {
	Head      template.HTML
	BodyClass string
	Header    template.HTML
	Footer    template.HTML
}

// LiveChrome returns the chrome of the published site, or of the draft home
// when nothing is published yet.
func (s *Site) LiveChrome() Chrome {
	s.mu.Lock()
	rev := s.live
	draft, hasDraft := s.draft["index"]
	s.mu.Unlock()
	if rev != nil && len(rev.Pages[chromeHead]) > 0 {
		return Chrome{Head: template.HTML(rev.Pages[chromeHead]), BodyClass: string(rev.Pages[chromeBody]),
			Header: template.HTML(rev.Pages[chromeHeader]), Footer: template.HTML(rev.Pages[chromeFooter])}
	}
	if !hasDraft {
		draft = Template{Handle: "index", Kind: "home"}
	}
	r := s.renderFull(draft)
	return Chrome{Head: r.Theme.Head(), BodyClass: r.Theme.BodyClass(), Header: template.HTML(r.Header), Footer: template.HTML(r.Footer)}
}

// ApplyStarter replaces the draft of handle with a starter template. The
// previous draft is first saved as a revision (not published) so it can be
// restored with RestoreRevision.
func (s *Site) ApplyStarter(handle string, t Template, by string) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prevID := ""
	if len(s.draft) > 0 {
		snap := &Revision{CreatedAt: s.now().UTC(), CreatedBy: by, Message: "Borrador antes de cambiar de tema",
			Templates: map[string]Template{}, Reports: map[string]Report{}, Pages: map[string][]byte{}}
		for h, d := range s.draft {
			snap.Templates[h] = d
		}
		b, _ := json.Marshal(snap.Templates)
		sum := sha256.Sum256(append([]byte("draft|"+RendererVersion+"|"), b...))
		snap.Checksum = hex.EncodeToString(sum[:])
		snap.ID = "rev_" + snap.Checksum[:12]
		if _, exists := s.revisions[snap.ID]; !exists && s.persist != nil {
			if err := s.persist.SaveRevision(snap); err != nil {
				return "", 0, fmt.Errorf("builder: save revision: %w", err)
			}
		}
		s.revisions[snap.ID] = snap
		prevID = snap.ID
	}
	t.Handle = handle
	if s.persist != nil {
		if err := s.persist.SaveDraft(handle, t, s.draftVer+1); err != nil {
			if errors.Is(err, ErrStaleDraft) {
				return "", 0, ErrStaleDraft
			}
			return "", 0, fmt.Errorf("builder: save draft: %w", err)
		}
	}
	s.draft[handle] = t
	s.draftVer++
	return prevID, s.draftVer, nil
}

// RestoreRevision copies the templates of a revision back into the draft.
func (s *Site) RestoreRevision(id string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rev, ok := s.revisions[id]
	if !ok {
		return 0, ErrNotFound
	}
	for h, t := range rev.Templates {
		if s.persist != nil {
			if err := s.persist.SaveDraft(h, t, s.draftVer+1); err != nil {
				if errors.Is(err, ErrStaleDraft) {
					return 0, ErrStaleDraft
				}
				return 0, fmt.Errorf("builder: save draft: %w", err)
			}
		}
		s.draft[h] = t
		s.draftVer++
	}
	return s.draftVer, nil
}

// Releases returns the publish history, newest first.
func (s *Site) Releases() []Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Release, len(s.releases))
	for i, r := range s.releases {
		out[len(out)-1-i] = r
	}
	return out
}

// Rendered is the output of rendering one template.
type Rendered struct {
	Doc    []byte
	Report Report
	Header []byte // header region HTML, reused by the shopper pages
	Footer []byte
	Theme  ResolvedTheme
}

// render validates and renders a template to a full HTML document.
func (s *Site) render(t Template) ([]byte, Report) {
	r := s.renderFull(t)
	return r.Doc, r.Report
}

func (s *Site) renderFull(t Template) Rendered {
	rep := Report{Handle: t.Handle, Limit: PageBudgets[t.Kind], Issues: []Issue{}}
	if rep.Limit == (Budget{}) {
		rep.Limit = PageBudgets["page"]
	}
	issue := func(path, code, msg string, blocking bool) {
		rep.Issues = append(rep.Issues, Issue{Path: path, Code: code, Message: msg, Blocking: blocking})
	}
	if strings.TrimSpace(t.SEO.Title) == "" {
		issue("/seo/title", "seo_title_missing", "La página no tiene título para buscadores", true)
	}
	if strings.TrimSpace(t.SEO.Description) == "" {
		issue("/seo/description", "seo_description_missing", "Falta la descripción para buscadores", false)
	}
	ValidateTheme(t.Theme, &rep.Issues)
	rt := Resolve(t.Theme)

	h1Used := false
	ids := map[string]bool{}
	renderSection := func(region string, i int, sec SectionInstance, out *bytes.Buffer, account bool) {
		path := fmt.Sprintf("/regions/%s/%d", region, i)
		if account {
			if ids[sec.ID] {
				issue(path+"/id", "duplicate_id", "Identificador de sección repetido", true)
			}
			ids[sec.ID] = true
		}
		if sec.GlobalRef != "" {
			return // global sections render from their own document
		}
		def, ok := s.lib.defs[sec.Type]
		if !ok {
			issue(path+"/type", "unknown_section", "Tipo de sección desconocido: "+sec.Type, true)
			return
		}
		if region == "main" && !enabled(def, t.Kind) {
			issue(path+"/type", "section_not_allowed", def.Name+" no se puede usar en este tipo de página", true)
		}
		settings := map[string]any{}
		for k, v := range sec.Settings {
			settings[k] = v
		}
		for k, p := range def.SettingsSchema.Properties {
			if _, ok := settings[k]; !ok && p.Default != nil {
				settings[k] = p.Default
			}
		}
		before := len(rep.Issues)
		if account {
			def.SettingsSchema.Validate(path+"/settings", sec.Settings, &rep.Issues)
			// An uploaded image always needs a description for screen readers.
			if img, _ := sec.Settings["image"].(string); img != "" {
				if alt, _ := sec.Settings["alt"].(string); len(strings.TrimSpace(alt)) < 5 {
					issue(path+"/settings/alt", "required", "Describe la imagen (mínimo 5 caracteres) para quien usa lector de pantalla", true)
				}
			}
			if n := len(sec.Blocks); n < def.Blocks.Min || n > def.Blocks.Max {
				issue(path+"/blocks", "block_count", fmt.Sprintf("%s admite entre %d y %d bloques", def.Name, def.Blocks.Min, def.Blocks.Max), true)
			}
			for j, b := range sec.Blocks {
				if !contains(def.Blocks.Allowed, b.Type) {
					issue(fmt.Sprintf("%s/blocks/%d/type", path, j), "block_not_allowed", "Bloque no permitido: "+b.Type, true)
				}
			}
			rep.Budget = rep.Budget.add(def.Budget)
		}
		if len(rep.Issues) > before {
			return
		}
		if !visibleNow(sec.Visibility, s.now()) {
			return
		}
		isH1 := false
		if !h1Used && region == "main" && headed(def) {
			isH1, h1Used = true, true
		}
		ctx := map[string]any{"ID": sec.ID, "S": settings, "Blocks": sec.Blocks, "H1": isH1}
		if s.data != nil && (def.Type == "product-grid" || def.Type == "product-carousel") {
			limit := 8
			if f, ok := settings["limit"].(float64); ok {
				limit = int(f)
			}
			brand, _ := settings["brand"].(string)
			cat, _ := settings["category"].(string)
			if q, ok := s.data.(Querier); ok {
				ctx["Products"] = q.Query(ProductQuery{Limit: limit, Brand: brand, Category: cat})
			} else {
				ctx["Products"] = s.data.Products(limit)
			}
		}
		fmt.Fprintf(out, `<section id="%s" data-type="%s">`, template.HTMLEscapeString(sec.ID), def.Type)
		if err := s.lib.tmpls[def.Type].Execute(out, ctx); err != nil {
			issue(path, "render_failed", err.Error(), true)
		}
		out.WriteString("</section>\n")
	}

	var header, main, footer bytes.Buffer
	for i, sec := range t.Regions["header"] {
		renderSection("header", i, sec, &header, true)
	}
	for i, sec := range t.Regions["main"] {
		renderSection("main", i, sec, &main, true)
	}
	for i, sec := range t.Regions["footer"] {
		renderSection("footer", i, sec, &footer, true)
	}
	// Every page gets a header and footer, even when the template has none.
	if header.Len() == 0 {
		renderSection("header", 0, SectionInstance{ID: "apx-header", Type: "header", Settings: map[string]any{"logoText": "Tienda", "menu": "Productos | c"}}, &header, false)
	}
	if footer.Len() == 0 {
		renderSection("footer", 0, SectionInstance{ID: "apx-footer", Type: "footer", Settings: map[string]any{"storeName": "Tienda", "legal": "© Tienda"}}, &footer, false)
	}
	if !h1Used {
		issue("/regions/main", "h1_missing", "La página no tiene un título principal (h1)", false)
	}

	var doc bytes.Buffer
	doc.WriteString("<!doctype html><html lang=\"es\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	fmt.Fprintf(&doc, "<title>%s</title><meta name=\"description\" content=\"%s\">",
		template.HTMLEscapeString(t.SEO.Title), template.HTMLEscapeString(t.SEO.Description))
	doc.WriteString(string(rt.Head()))
	fmt.Fprintf(&doc, `<script src="/s/assets/shop.js?v=%s" defer></script></head><body class="%s"><a class="skip" href="#main">Saltar al contenido</a>`, AssetVersion, rt.BodyClass())
	doc.Write(header.Bytes())
	doc.WriteString(`<main id="main">`)
	doc.Write(main.Bytes())
	doc.WriteString(`</main>`)
	doc.Write(footer.Bytes())
	doc.WriteString("</body></html>")

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(doc.Bytes())
	_ = zw.Close()
	rep.HTMLSize = gz.Len()
	if rep.HTMLSize > rep.Limit.HTMLBytes {
		issue("/", "budget_html", fmt.Sprintf("El HTML comprimido pesa %d bytes; el máximo es %d", rep.HTMLSize, rep.Limit.HTMLBytes), true)
	}
	if rep.Budget.JSBytes > rep.Limit.JSBytes {
		issue("/", "budget_js", fmt.Sprintf("Las secciones suman %d bytes de JS; el máximo es %d", rep.Budget.JSBytes, rep.Limit.JSBytes), true)
	}
	if rep.Budget.AboveFoldImages > rep.Limit.AboveFoldImages {
		issue("/", "budget_images", "Demasiadas imágenes grandes en la parte superior de la página", false)
	}
	return Rendered{Doc: doc.Bytes(), Report: rep, Header: header.Bytes(), Footer: footer.Bytes(), Theme: rt}
}

// headed reports whether a section renders its heading as the page title.
func headed(d SectionDefinition) bool { return d.Type == "hero" || d.Type == "rich-text" }

func enabled(d SectionDefinition, kind string) bool {
	return len(d.EnabledOn) == 0 || contains(d.EnabledOn, kind)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func visibleNow(v *Visibility, now time.Time) bool {
	if v == nil || v.Schedule == nil {
		return true
	}
	return !now.Before(v.Schedule.From) && !now.After(v.Schedule.To)
}
