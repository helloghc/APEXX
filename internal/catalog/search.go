package catalog

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// fold lowercases and strips accents ("Pacífico" → "pacifico").
func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, _ := transform.String(t, strings.ToLower(s))
	return out
}

func words(s string) []string {
	return strings.FieldsFunc(fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// within1 reports whether a and b differ by at most one edit (typo tolerance).
func within1(a, b string) bool {
	if a == b {
		return true
	}
	la, lb := len(a), len(b)
	if la-lb > 1 || lb-la > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < la && j < lb {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		switch {
		case la > lb:
			i++
		case lb > la:
			j++
		default:
			i++
			j++
		}
	}
	return edits+(la-i)+(lb-j) <= 1
}

// tokenMatches: prefix match, or one typo for tokens of 4+ letters.
func tokenMatches(q string, ws []string) bool {
	for _, w := range ws {
		if strings.HasPrefix(w, q) || (len(q) >= 4 && (within1(q, w) || (len(w) > len(q) && within1(q, w[:len(q)])))) {
			return true
		}
	}
	return false
}

// Search returns active products matching every query token in title, brand,
// handle, categories or SKU, after the cursor id. An empty query lists all.
func (s *Store) Search(q, after string, limit int) ([]Product, string) {
	qs := words(q)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Product, 0, limit)
	next := ""
	for _, id := range s.ids {
		if id <= after && after != "" {
			continue
		}
		p := s.byID[id]
		if p.Status != "active" {
			continue
		}
		if len(qs) > 0 {
			if !matchesAll(qs, p.hay) {
				continue
			}
		}
		if len(out) == limit {
			next = out[len(out)-1].ID
			break
		}
		out = append(out, *p)
	}
	return out, next
}

// haystack returns the folded words a query is matched against.
func haystack(p *Product) []string {
	hay := words(p.Title + " " + p.Brand + " " + p.Handle + " " + p.Type + " " + strings.Join(p.Categories, " ") + " " + strings.Join(p.Tags, " "))
	for _, v := range p.Variants {
		hay = append(hay, words(v.SKU)...)
		if v.Barcode != "" {
			hay = append(hay, fold(v.Barcode))
		}
	}
	return hay
}

func matchesAll(qs, hay []string) bool {
	for _, t := range qs {
		if !tokenMatches(t, hay) {
			return false
		}
	}
	return true
}

// ByHandle returns an active product by handle.
func (s *Store) ByHandle(h string) (Product, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.byID[s.byHdl[h]]; ok && p.Status == "active" {
		return *p, true
	}
	return Product{}, false
}

// Matcher returns a function that reports whether a product matches every
// token of q (prefix match, one typo allowed). An empty q matches everything.
func Matcher(q string) func(p *Product) bool {
	qs := words(q)
	if len(qs) == 0 {
		return func(*Product) bool { return true }
	}
	return func(p *Product) bool { return matchesAll(qs, p.hay) }
}
