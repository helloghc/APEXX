package catalog

import (
	"strconv"
	"strings"
)

// Intent is what a shopper's search says besides the product: a brand, a
// value of a filterable attribute (color, size), a price limit or "cheap".
// "tenis rojos para correr talla 27 baratos" becomes the text "tenis para
// correr", color Rojo, size 27, cheapest first. Rules only, no model: it
// runs where the storefront runs, in microseconds.
type Intent struct {
	Q        string // the words that are still text
	Brands   []string
	Attrs    map[string][]string
	PriceMin *int64
	PriceMax *int64
	Sort     string
	Labels   []string // what was understood, in the shopper's words
}

// Any reports whether the search said something besides text.
func (it Intent) Any() bool {
	return len(it.Brands) > 0 || len(it.Attrs) > 0 || it.PriceMin != nil || it.PriceMax != nil || it.Sort != ""
}

var cheapWords = map[string]bool{"barato": true, "baratos": true, "barata": true, "baratas": true, "economico": true, "economicos": true, "economica": true, "economicas": true}
var maxWords = map[string]bool{"menos": true, "hasta": true, "maximo": true, "max": true, "debajo": true, "bajo": true}
var minWords = map[string]bool{"mas": true, "desde": true, "minimo": true, "arriba": true, "encima": true}
var glue = map[string]bool{"de": true, "del": true, "por": true, "a": true, "que": true, "en": true, "color": true, "el": true, "los": true, "las": true}

// ParseIntent reads q against the store's brands and filterable attributes.
func ParseIntent(q string, t *Taxonomy) Intent {
	ws := strings.Fields(fold(q))
	used := make([]bool, len(ws))
	it := Intent{Attrs: map[string][]string{}}
	num := func(s string) (int64, bool) {
		s = strings.TrimPrefix(strings.ReplaceAll(s, ",", ""), "$")
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil && n > 0
	}
	// Prices: "menos de 500", "hasta 800", "más de 300", "baratos".
	for i, w := range ws {
		switch {
		case cheapWords[w]:
			used[i] = true
			if it.Sort == "" {
				it.Sort = SortPriceAsc
				it.Labels = append(it.Labels, "menor precio")
			}
		case maxWords[w] || minWords[w]:
			j := i + 1
			for j < len(ws) && glue[ws[j]] {
				j++
			}
			if j < len(ws) {
				if n, ok := num(ws[j]); ok {
					v := n * 100
					for k := i; k <= j; k++ {
						used[k] = true
					}
					if maxWords[w] {
						it.PriceMax = &v
						it.Labels = append(it.Labels, "hasta $"+strconv.FormatInt(n, 10))
					} else {
						it.PriceMin = &v
						it.Labels = append(it.Labels, "desde $"+strconv.FormatInt(n, 10))
					}
				}
			}
		}
	}
	// Brands: the whole name, one or more words.
	for _, b := range t.Brands() {
		if !b.Active && b.Name == "" {
			continue
		}
		bw := strings.Fields(fold(b.Name))
		if i := findSeq(ws, used, bw); i >= 0 {
			for k := i; k < i+len(bw); k++ {
				used[k] = true
			}
			it.Brands = append(it.Brands, b.Handle)
			it.Labels = append(it.Labels, b.Name)
		}
	}
	// Attributes: "talla 27" (name and value) or a value of a list ("rojo").
	for _, a := range t.Attributes() {
		if !a.Filterable {
			continue
		}
		name := strings.Fields(fold(a.Name))
		if i := findSeq(ws, used, name); i >= 0 && i+len(name) < len(ws) && !used[i+len(name)] {
			v := ws[i+len(name)]
			raw := v
			for _, o := range a.Options {
				if fold(o) == v {
					raw = o
				}
			}
			if a.Kind == AttrNumber || len(a.Options) == 0 || raw != v || containsFold(a.Options, v) {
				for k := i; k <= i+len(name); k++ {
					used[k] = true
				}
				it.Attrs[a.ID] = append(it.Attrs[a.ID], raw)
				it.Labels = append(it.Labels, a.Name+" "+raw)
				continue
			}
		}
		for _, o := range a.Options {
			ow := strings.Fields(fold(o))
			i := findSeq(ws, used, ow)
			for _, f := range forms(ow) { // "rojos", "roja", "rojas" for "rojo"
				if i >= 0 {
					break
				}
				i = findSeq(ws, used, []string{f})
			}
			if i >= 0 {
				for k := i; k < i+len(ow); k++ {
					used[k] = true
				}
				it.Attrs[a.ID] = append(it.Attrs[a.ID], o)
				it.Labels = append(it.Labels, o)
			}
		}
	}
	if len(it.Attrs) == 0 {
		it.Attrs = nil
	}
	var rest []string
	for i, w := range ws {
		if !used[i] {
			rest = append(rest, w)
		}
	}
	// Trailing glue words left by what was taken out ("tenis de").
	for len(rest) > 0 && glue[rest[len(rest)-1]] {
		rest = rest[:len(rest)-1]
	}
	it.Q = strings.Join(rest, " ")
	return it
}

func findSeq(ws []string, used []bool, seq []string) int {
	if len(seq) == 0 {
		return -1
	}
outer:
	for i := 0; i+len(seq) <= len(ws); i++ {
		for k, s := range seq {
			if used[i+k] || ws[i+k] != s {
				continue outer
			}
		}
		return i
	}
	return -1
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if fold(s) == v {
			return true
		}
	}
	return false
}

// forms are the gender and number forms of a one-word value.
func forms(ow []string) []string {
	if len(ow) != 1 {
		return nil
	}
	w := ow[0]
	out := []string{plural(w)}
	if n := len(w); n > 2 && (w[n-1] == 'o' || w[n-1] == 'a') {
		other := w[:n-1] + map[byte]string{'o': "a", 'a': "o"}[w[n-1]]
		out = append(out, other, plural(other))
	}
	return out
}

// plural is the usual Spanish plural of a word.
func plural(w string) string {
	if w == "" {
		return w
	}
	switch w[len(w)-1] {
	case 'a', 'e', 'i', 'o', 'u':
		return w + "s"
	case 'z':
		return w[:len(w)-1] + "ces"
	}
	return w + "es"
}
