// Package builder is the site builder core: templates made of sections and
// blocks, edited with JSON Patch, validated against each section's schema
// and performance budget, and published as immutable revisions that render
// to static HTML. Publishing and rollback only move a pointer.
// The model follows docs/audit/builder.md section 4.2 and 5.
package builder

import "time"

// Budget limits what a section or a page may cost.
type Budget struct {
	HTMLBytes       int `json:"htmlBytes"`
	JSBytes         int `json:"jsBytes"`
	CSSBytes        int `json:"cssBytes"`
	Requests        int `json:"requests"`
	AboveFoldImages int `json:"aboveFoldImages"`
}

func (b Budget) add(o Budget) Budget {
	return Budget{b.HTMLBytes + o.HTMLBytes, b.JSBytes + o.JSBytes, b.CSSBytes + o.CSSBytes, b.Requests + o.Requests, b.AboveFoldImages + o.AboveFoldImages}
}

// BlockRules limits child blocks of a section.
type BlockRules struct {
	Allowed []string `json:"allowed"`
	Min     int      `json:"min"`
	Max     int      `json:"max"`
}

// SectionDefinition is a reusable section type ("hero", "product-carousel").
type SectionDefinition struct {
	Type           string     `json:"type"`
	Version        string     `json:"version"`
	Name           string     `json:"name"`
	Category       string     `json:"category"`
	Description    string     `json:"description"`
	EnabledOn      []string   `json:"enabledOn"`
	Hydration      string     `json:"hydration"` // none | visible | idle | interaction
	Blocks         BlockRules `json:"blocks"`
	Budget         Budget     `json:"budget"`
	SettingsSchema Schema     `json:"settingsSchema"`
	Template       string     `json:"-"` // Go html/template source
}

// Block is a child element of a section.
type Block struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings,omitempty"`
}

// Visibility controls when a section shows.
type Visibility struct {
	Devices  []string  `json:"devices,omitempty"`
	Schedule *Schedule `json:"schedule,omitempty"`
	Audience *Audience `json:"audience,omitempty"`
}

// Audience shows a section only to (or hides it from) customers of some
// groups. Pages are cached for everyone, so the store's page shows or hides
// it once it knows who is signed in; "only" sections start hidden.
type Audience struct {
	Mode   string   `json:"mode"` // only | hide
	Groups []string `json:"groups"`
}

// Schedule shows a section only inside a time window.
type Schedule struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// SectionInstance is one placed section on a template.
type SectionInstance struct {
	ID          string         `json:"id"`
	Type        string         `json:"type,omitempty"`
	TypeVersion string         `json:"typeVersion,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
	Blocks      []Block        `json:"blocks,omitempty"`
	GlobalRef   string         `json:"globalRef,omitempty"`
	Visibility  *Visibility    `json:"visibility,omitempty"`
	Locked      bool           `json:"locked,omitempty"`
}

// SEO is generated into the page head.
type SEO struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Template is one page layout (home, product, collection, custom page).
type Template struct {
	ID      string                       `json:"id"`
	Kind    string                       `json:"kind"`
	Handle  string                       `json:"handle"`
	Name    string                       `json:"name"`
	SEO     SEO                          `json:"seo"`
	Theme   *ThemeSettings               `json:"theme,omitempty"`
	Regions map[string][]SectionInstance `json:"regions"`
}

// Revision is an immutable snapshot of every template of a site.
type Revision struct {
	ID        string              `json:"id"`
	ParentID  string              `json:"parentId,omitempty"`
	CreatedAt time.Time           `json:"createdAt"`
	CreatedBy string              `json:"createdBy"`
	Message   string              `json:"message,omitempty"`
	Checksum  string              `json:"checksum"`
	Templates map[string]Template `json:"templates"`
	Reports   map[string]Report   `json:"reports"`
	Pages     map[string][]byte   `json:"-"` // rendered pages, keyed by handle
}

// Release records one publish or rollback.
type Release struct {
	RevisionID     string    `json:"revisionId"`
	PublishedAt    time.Time `json:"publishedAt"`
	PublishedBy    string    `json:"publishedBy"`
	DurationMicros int64     `json:"durationMicros"`
	RolledBackFrom string    `json:"rolledBackFrom,omitempty"`
}

// Issue is a validation finding. Blocking issues stop a publish.
type Issue struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

// Report is the validation and budget result for one template.
type Report struct {
	Handle   string  `json:"handle"`
	Budget   Budget  `json:"budget"`
	Limit    Budget  `json:"limit"`
	Issues   []Issue `json:"issues"`
	HTMLSize int     `json:"htmlBytes"`
}

// Blocking reports whether any issue blocks publishing.
func (r Report) Blocking() bool {
	for _, i := range r.Issues {
		if i.Blocking {
			return true
		}
	}
	return false
}

// PageBudgets are the per-kind limits enforced by the editor
// (docs/audit/builder.md section 4.7).
var PageBudgets = map[string]Budget{
	"home":       {HTMLBytes: 40_000, JSBytes: 80_000, CSSBytes: 30_000, Requests: 35, AboveFoldImages: 2},
	"product":    {HTMLBytes: 30_000, JSBytes: 100_000, CSSBytes: 30_000, Requests: 30, AboveFoldImages: 2},
	"collection": {HTMLBytes: 40_000, JSBytes: 120_000, CSSBytes: 30_000, Requests: 40, AboveFoldImages: 4},
	"page":       {HTMLBytes: 40_000, JSBytes: 80_000, CSSBytes: 30_000, Requests: 35, AboveFoldImages: 2},
}
