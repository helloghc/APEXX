package i18n

import (
	"embed"
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

// Builtin looks a phrase up in Apexx's own dictionary for that language.
func Builtin(lang string) Lookup {
	d := builtin[lang]
	return func(p string) (string, bool) {
		v, ok := d[p]
		return v, ok
	}
}

// With looks a phrase up first in the store's own translations, then in
// Apexx's. The store's win, so a merchant can reword anything.
func With(lang string, own map[string]string) Lookup {
	d := builtin[lang]
	return func(p string) (string, bool) {
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
