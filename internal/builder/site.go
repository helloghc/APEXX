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
	Title string
	URL   string
	Price string
}

// Data gives sections access to commerce data at render time.
type Data interface {
	Products(limit int) []Card
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
		t, err := template.New(d.Type).Parse(d.Template)
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
const RendererVersion = "2"

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
		html, rep := s.render(t)
		rev.Templates[h] = t
		rev.Reports[h] = rep
		rev.Pages[h] = html
		blocked = blocked || rep.Blocking()
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
	if !ok {
		return nil, "", ErrNotFound
	}
	return html, rev.ID, nil
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

// render validates and renders a template to a full HTML document.
func (s *Site) render(t Template) ([]byte, Report) {
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

	var body bytes.Buffer
	h1Used := false
	ids := map[string]bool{}
	for _, region := range []string{"header", "main", "footer"} {
		for i, sec := range t.Regions[region] {
			path := fmt.Sprintf("/regions/%s/%d", region, i)
			if ids[sec.ID] {
				issue(path+"/id", "duplicate_id", "Identificador de sección repetido", true)
			}
			ids[sec.ID] = true
			if sec.GlobalRef != "" {
				continue // global sections render from their own document
			}
			def, ok := s.lib.defs[sec.Type]
			if !ok {
				issue(path+"/type", "unknown_section", "Tipo de sección desconocido: "+sec.Type, true)
				continue
			}
			if !enabled(def, t.Kind) {
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
			def.SettingsSchema.Validate(path+"/settings", sec.Settings, &rep.Issues)
			if n := len(sec.Blocks); n < def.Blocks.Min || n > def.Blocks.Max {
				issue(path+"/blocks", "block_count", fmt.Sprintf("%s admite entre %d y %d bloques", def.Name, def.Blocks.Min, def.Blocks.Max), true)
			}
			for j, b := range sec.Blocks {
				if !contains(def.Blocks.Allowed, b.Type) {
					issue(fmt.Sprintf("%s/blocks/%d/type", path, j), "block_not_allowed", "Bloque no permitido: "+b.Type, true)
				}
			}
			rep.Budget = rep.Budget.add(def.Budget)
			if len(rep.Issues) > before {
				continue
			}
			if !visibleNow(sec.Visibility, s.now()) {
				continue
			}
			isH1 := false
			if !h1Used && region == "main" && headed(def) {
				isH1, h1Used = true, true
			}
			ctx := map[string]any{"ID": sec.ID, "S": settings, "Blocks": sec.Blocks, "H1": isH1}
			if s.data != nil {
				limit := 8
				if f, ok := settings["limit"].(float64); ok {
					limit = int(f)
				}
				ctx["Products"] = s.data.Products(limit)
			}
			fmt.Fprintf(&body, `<section id="%s" data-type="%s">`, template.HTMLEscapeString(sec.ID), def.Type)
			if err := s.lib.tmpls[def.Type].Execute(&body, ctx); err != nil {
				issue(path, "render_failed", err.Error(), true)
			}
			body.WriteString("</section>\n")
		}
	}
	if !h1Used {
		issue("/regions/main", "h1_missing", "La página no tiene un título principal (h1)", false)
	}

	var doc bytes.Buffer
	doc.WriteString("<!doctype html><html lang=\"es\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	fmt.Fprintf(&doc, "<title>%s</title><meta name=\"description\" content=\"%s\">",
		template.HTMLEscapeString(t.SEO.Title), template.HTMLEscapeString(t.SEO.Description))
	doc.WriteString("<style>body{margin:0;font:16px/1.5 system-ui,sans-serif}section{padding:24px 16px}.apx-nav{display:flex;gap:16px;padding:12px 16px;border-bottom:1px solid #e3e4ea;font-weight:600}.apx-nav a{color:#1d3fae;text-decoration:none}.apx-nav a:first-child{margin-right:auto}.hero img{max-width:100%;height:auto}</style></head><body>")
	doc.WriteString(`<nav class="apx-nav" aria-label="Principal"><a href="index">Inicio</a><a href="c">Productos</a><a href="cart">Carrito</a></nav>`)
	doc.Write(body.Bytes())
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
	return doc.Bytes(), rep
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
