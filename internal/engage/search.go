// Package engage holds what brings shoppers back: search merchandising,
// reviews and wish lists.
package engage

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// SearchConfig is how a store tunes its search. It is read on every search,
// so it lives in memory and changes apply at once (no reindex).
type SearchConfig struct {
	// Synonyms: every group lists words or phrases that mean the same.
	Synonyms [][]string `json:"synonyms"`
	// Redirects send a search term straight to a page (a campaign, a policy).
	Redirects []TermRedirect `json:"redirects"`
	// Pins put products first for a term.
	Pins []TermPins `json:"pins"`
	// Hidden product ids never show in search results.
	Hidden []string `json:"hidden"`
}

// TermRedirect sends Term to URL.
type TermRedirect struct {
	Term string `json:"term"`
	URL  string `json:"url"`
}

// TermPins lists the products shown first for Term.
type TermPins struct {
	Term       string   `json:"term"`
	ProductIDs []string `json:"productIds"`
}

// Fold lowercases, strips accents and collapses spaces, so "Ténis  ROJOS"
// and "tenis rojos" are the same term.
func Fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(s))) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// Normalize cleans the configuration and returns one message per problem.
func (c *SearchConfig) Normalize() []string {
	var errs []string
	groups := c.Synonyms[:0]
	for _, g := range c.Synonyms {
		seen := map[string]bool{}
		var out []string
		for _, w := range g {
			f := Fold(w)
			if f != "" && !seen[f] && len(f) <= 60 {
				seen[f] = true
				out = append(out, f)
			}
		}
		if len(out) == 1 {
			errs = append(errs, "Un grupo de sinónimos necesita al menos dos palabras: "+out[0])
		}
		if len(out) > 1 {
			groups = append(groups, out)
		}
	}
	c.Synonyms = groups
	if len(c.Synonyms) > 500 {
		errs = append(errs, "Máximo 500 grupos de sinónimos")
	}
	reds := c.Redirects[:0]
	seen := map[string]bool{}
	for _, r := range c.Redirects {
		r.Term, r.URL = Fold(r.Term), strings.TrimSpace(r.URL)
		if r.Term == "" && r.URL == "" {
			continue
		}
		switch {
		case r.Term == "":
			errs = append(errs, "Falta el término de una redirección")
		case seen[r.Term]:
			errs = append(errs, "El término “"+r.Term+"” tiene dos redirecciones")
		case !strings.HasPrefix(r.URL, "/") && !strings.HasPrefix(r.URL, "https://"):
			errs = append(errs, "La redirección de “"+r.Term+"” debe empezar con / o https://")
		case strings.ContainsAny(r.URL, " \t\r\n") || len(r.URL) > 500 || strings.HasPrefix(r.URL, "//"):
			errs = append(errs, "La dirección de “"+r.Term+"” no es válida")
		}
		seen[r.Term] = true
		reds = append(reds, r)
	}
	c.Redirects = reds
	pins := c.Pins[:0]
	seen = map[string]bool{}
	for _, p := range c.Pins {
		p.Term = Fold(p.Term)
		if p.Term == "" || len(p.ProductIDs) == 0 {
			continue
		}
		if seen[p.Term] {
			errs = append(errs, "El término “"+p.Term+"” tiene dos listas de productos fijados")
		}
		if len(p.ProductIDs) > 12 {
			errs = append(errs, "Máximo 12 productos fijados para “"+p.Term+"”")
		}
		seen[p.Term] = true
		pins = append(pins, p)
	}
	c.Pins = pins
	if c.Synonyms == nil {
		c.Synonyms = [][]string{}
	}
	if c.Redirects == nil {
		c.Redirects = []TermRedirect{}
	}
	if c.Pins == nil {
		c.Pins = []TermPins{}
	}
	if c.Hidden == nil {
		c.Hidden = []string{}
	}
	return errs
}

// RedirectFor returns where the term must go, if anywhere.
func (c *SearchConfig) RedirectFor(q string) (string, bool) {
	if c == nil {
		return "", false
	}
	f := Fold(q)
	for _, r := range c.Redirects {
		if r.Term == f {
			return r.URL, true
		}
	}
	return "", false
}

// PinsFor returns the products pinned for the term.
func (c *SearchConfig) PinsFor(q string) []string {
	if c == nil {
		return nil
	}
	f := Fold(q)
	for _, p := range c.Pins {
		if p.Term == f {
			return p.ProductIDs
		}
	}
	return nil
}

// HiddenSet returns the hidden product ids as a set.
func (c *SearchConfig) HiddenSet() map[string]bool {
	if c == nil || len(c.Hidden) == 0 {
		return nil
	}
	m := make(map[string]bool, len(c.Hidden))
	for _, id := range c.Hidden {
		m[id] = true
	}
	return m
}

// Expand returns other ways to phrase q using the synonym groups: each
// synonym found in q (as whole words) is swapped by the others of its group.
func (c *SearchConfig) Expand(q string) []string {
	if c == nil || len(c.Synonyms) == 0 {
		return nil
	}
	f := " " + Fold(q) + " "
	seen := map[string]bool{strings.TrimSpace(f): true}
	var out []string
	for _, g := range c.Synonyms {
		for _, w := range g {
			if !strings.Contains(f, " "+w+" ") {
				continue
			}
			for _, alt := range g {
				if alt == w {
					continue
				}
				v := strings.TrimSpace(strings.Replace(f, " "+w+" ", " "+alt+" ", 1))
				if !seen[v] && len(out) < 12 {
					seen[v] = true
					out = append(out, v)
				}
			}
		}
	}
	return out
}
