package i18n

import (
	"embed"
	"regexp"
	"sort"
	"strings"
)

//go:embed phrases*.txt
var files embed.FS

// Language is one language the storefront can be shown in.
type Language struct {
	Code string `json:"code"` // "en"
	Name string `json:"name"` // as its speakers write it
	Tag  string `json:"tag"`  // for hreflang and <html lang>
}

// Languages lists every language Apexx ships, the default first.
var Languages = []Language{
	{"es", "Español", "es"},
	{"en", "English", "en"},
	{"fr", "Français", "fr"},
	{"pt", "Português", "pt"},
	{"id", "Bahasa Indonesia", "id"},
	{"ja", "日本語", "ja"},
}

// Known reports whether Apexx ships that language.
func Known(code string) bool {
	for _, l := range Languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

// Find returns the language with that code.
func Find(code string) (Language, bool) {
	for _, l := range Languages {
		if l.Code == code {
			return l, true
		}
	}
	return Language{}, false
}

// builtin holds Apexx's own translations: language -> Spanish phrase -> text.
var builtin = load()

func load() map[string]map[string]string {
	out := map[string]map[string]string{}
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		raw, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		var cols []string
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "## ") {
				continue
			}
			parts := strings.Split(line, " || ")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			if strings.HasPrefix(line, "@ ") {
				parts[0] = strings.TrimPrefix(parts[0], "@ ")
				cols = parts
				continue
			}
			if len(parts) != len(cols) {
				panic("i18n: " + e.Name() + ": línea con columnas de más o de menos: " + line)
			}
			for i := 1; i < len(parts); i++ {
				if out[cols[i]] == nil {
					out[cols[i]] = map[string]string{}
				}
				out[cols[i]][parts[0]] = parts[i]
			}
		}
	}
	return out
}

// langKey is asked of a Lookup to learn its language (for the patterns).
const langKey = "\x00lang"

// Builtin looks a phrase up in Apexx's own dictionary for that language.
func Builtin(lang string) Lookup {
	d := builtin[lang]
	return func(p string) (string, bool) {
		if p == langKey {
			return lang, true
		}
		v, ok := d[p]
		return v, ok
	}
}

// With looks a phrase up first in the store's own translations, then in
// Apexx's. The store's win, so a merchant can reword anything.
func With(lang string, own map[string]string) Lookup {
	d := builtin[lang]
	return func(p string) (string, bool) {
		if p == langKey {
			return lang, true
		}
		if v, ok := own[p]; ok && v != "" {
			return v, true
		}
		v, ok := d[p]
		return v, ok
	}
}

// Config is what a store chose: which languages its storefront offers besides
// Spanish, and its own translations of its own texts (product names, menus,
// headings) or rewordings of Apexx's.
type Config struct {
	Enabled []string                     `json:"enabled"`
	Own     map[string]map[string]string `json:"own"` // language -> Spanish text -> translation
}

// Normalize drops unknown languages and empty entries.
func (c *Config) Normalize() {
	seen := map[string]bool{Default: true}
	out := []string{}
	for _, l := range c.Enabled {
		if Known(l) && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	c.Enabled = out
	own := map[string]map[string]string{}
	for lang, m := range c.Own {
		if !Known(lang) || lang == Default {
			continue
		}
		clean := map[string]string{}
		for k, v := range m {
			k, v = strings.Join(strings.Fields(k), " "), strings.TrimSpace(v)
			if k != "" && v != "" && len(k) <= 2000 && len(v) <= 2000 {
				clean[k] = v
			}
		}
		if len(clean) > 0 {
			own[lang] = clean
		}
	}
	c.Own = own
}

// Has reports whether the store offers that language.
func (c *Config) Has(lang string) bool {
	if c == nil {
		return false
	}
	for _, l := range c.Enabled {
		if l == lang {
			return true
		}
	}
	return false
}

// Text is one translated piece of a product, with the Spanish text it was
// written for, so a later change of the original can be pointed out.
type Text struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

// ProductTexts are the translations of one product: language -> field -> text.
type ProductTexts map[string]map[string]Text

// Merge adds product translations to the store's own phrases. Phrases the
// merchant wrote by hand win over the ones written on a product.
func (c *Config) Merge(products []ProductTexts) {
	if c.Own == nil {
		c.Own = map[string]map[string]string{}
	}
	for _, p := range products {
		for lang, fields := range p {
			if !Known(lang) || lang == Default {
				continue
			}
			if c.Own[lang] == nil {
				c.Own[lang] = map[string]string{}
			}
			for _, t := range fields {
				src := strings.Join(strings.Fields(t.Source), " ")
				if src == "" || strings.TrimSpace(t.Text) == "" {
					continue
				}
				if _, set := c.Own[lang][src]; !set {
					c.Own[lang][src] = strings.TrimSpace(t.Text)
				}
			}
		}
	}
}

// pattern is a phrase with variable parts: "Llega el {}" or "Zona {}: el
// código postal # es de {}". Text parts ({}) are translated themselves;
// numbers (#) go back as they were.
type pattern struct {
	re    *regexp.Regexp
	out   string
	kinds []byte // 't' text, 'n' number, in order
}

// patterns[lang][first word] lists the patterns that start with that word
// (most specific first), so a message only tries the few that could match.
var patterns = map[string]map[string][]pattern{}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

func init() {
	for lang, d := range builtin {
		var list []pattern
		for k, v := range d {
			if !strings.Contains(k, "{}") || len(strings.TrimSpace(strings.NewReplacer("{}", "", "#", "").Replace(k))) < 3 {
				continue
			}
			var kinds []byte
			var src strings.Builder
			src.WriteString("^")
			for _, part := range splitKeep(k) {
				switch part {
				case "{}":
					kinds = append(kinds, 't')
					src.WriteString("(.+?)")
				case "#":
					kinds = append(kinds, 'n')
					src.WriteString(`([0-9][0-9.,]*[0-9]|[0-9])`)
				default:
					src.WriteString(regexp.QuoteMeta(part))
				}
			}
			src.WriteString("$")
			list = append(list, pattern{re: regexp.MustCompile(src.String()), out: v, kinds: kinds})
		}
		// Longer keys first: the most specific pattern wins.
		sort.Slice(list, func(i, j int) bool { return len(list[i].re.String()) > len(list[j].re.String()) })
		byWord := map[string][]pattern{}
		for _, p := range list {
			w := firstWord(strings.TrimPrefix(p.re.String(), "^"))
			if strings.ContainsAny(w, "(\\") {
				w = "" // starts with a variable part
			}
			byWord[w] = append(byWord[w], p)
		}
		patterns[lang] = byWord
	}
}

func splitKeep(k string) []string {
	var out []string
	for k != "" {
		i := strings.IndexAny(k, "{#")
		if i < 0 {
			out = append(out, k)
			break
		}
		if k[i] == '{' && !strings.HasPrefix(k[i:], "{}") {
			out = append(out, k[:i+1])
			k = k[i+1:]
			continue
		}
		if i > 0 {
			out = append(out, k[:i])
		}
		if k[i] == '#' {
			out = append(out, "#")
			k = k[i+1:]
		} else {
			out = append(out, "{}")
			k = k[i+2:]
		}
	}
	return out
}

// byPattern translates a phrase with variable parts, or reports false.
func byPattern(key string, look Lookup, depth int) (string, bool) {
	lang, ok := look(langKey)
	if !ok || depth > 2 {
		return "", false
	}
	cands := append(patterns[lang][firstWord(key)], patterns[lang][""]...)
	for _, p := range cands {
		m := p.re.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		var texts, nums []string
		for i, k := range p.kinds {
			if k == 't' {
				texts = append(texts, translatePart(m[i+1], look, depth+1))
			} else {
				nums = append(nums, m[i+1])
			}
		}
		var b strings.Builder
		ti, ni := 0, 0
		for _, part := range splitKeep(p.out) {
			switch part {
			case "{}":
				if ti < len(texts) {
					b.WriteString(texts[ti])
					ti++
				}
			case "#":
				if ni < len(nums) {
					b.WriteString(nums[ni])
					ni++
				}
			default:
				b.WriteString(part)
			}
		}
		return b.String(), true
	}
	return "", false
}

// translatePart translates the variable part of a pattern: whole, by its
// numbers, or as another pattern; else it stays as it is (a name, a place).
func translatePart(v string, look Lookup, depth int) string {
	if out, ok := look(v); ok {
		return out
	}
	if nums := numbers.FindAllString(v, -1); len(nums) > 0 {
		if out, ok := look(numbers.ReplaceAllString(v, "#")); ok {
			for _, n := range nums {
				out = strings.Replace(out, "#", n, 1)
			}
			return out
		}
	}
	if out, ok := byPattern(v, look, depth); ok {
		return out
	}
	return v
}
