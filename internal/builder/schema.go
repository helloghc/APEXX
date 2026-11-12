package builder

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// Schema is the subset of JSON Schema 2020-12 the editor needs for section
// settings. x-apexx-* keys tell the editor which control to render.
type Schema struct {
	Type                 string            `json:"type,omitempty"`
	Title                string            `json:"title,omitempty"`
	Required             []string          `json:"required,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	AdditionalProperties *bool             `json:"additionalProperties,omitempty"`
	Enum                 []any             `json:"enum,omitempty"`
	MinLength            *int              `json:"minLength,omitempty"`
	MaxLength            *int              `json:"maxLength,omitempty"`
	Minimum              *float64          `json:"minimum,omitempty"`
	Maximum              *float64          `json:"maximum,omitempty"`
	Default              any               `json:"default,omitempty"`
	Widget               string            `json:"x-apexx-widget,omitempty"`
	// Order lists Properties in the order an editor shows them (a JSON
	// object has no order of its own).
	Order []string `json:"x-apexx-order,omitempty"`
}

// Validate checks v against s and appends issues under path.
func (s Schema) Validate(path string, v any, out *[]Issue) {
	bad := func(code, msg string) {
		*out = append(*out, Issue{Path: path, Code: code, Message: msg, Blocking: true})
	}
	if v == nil {
		return
	}
	switch s.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			bad("type", "debe ser un objeto")
			return
		}
		for _, r := range s.Required {
			if _, ok := m[r]; !ok {
				*out = append(*out, Issue{Path: path + "/" + r, Code: "required", Message: "campo obligatorio", Blocking: true})
			}
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, ok := s.Properties[k]
			if !ok {
				if s.AdditionalProperties != nil && !*s.AdditionalProperties {
					*out = append(*out, Issue{Path: path + "/" + k, Code: "unknown_setting", Message: "ajuste no definido en la sección", Blocking: true})
				}
				continue
			}
			sub.Validate(path+"/"+k, m[k], out)
		}
	case "string":
		str, ok := v.(string)
		if !ok {
			bad("type", "debe ser texto")
			return
		}
		n := utf8.RuneCountInString(str)
		if s.MinLength != nil && n < *s.MinLength {
			bad("min_length", fmt.Sprintf("mínimo %d caracteres", *s.MinLength))
		}
		if s.MaxLength != nil && n > *s.MaxLength {
			bad("max_length", fmt.Sprintf("máximo %d caracteres", *s.MaxLength))
		}
	case "number", "integer":
		f, ok := v.(float64)
		if !ok {
			bad("type", "debe ser un número")
			return
		}
		if s.Type == "integer" && f != float64(int64(f)) {
			bad("type", "debe ser un entero")
		}
		if s.Minimum != nil && f < *s.Minimum {
			bad("minimum", fmt.Sprintf("mínimo %v", *s.Minimum))
		}
		if s.Maximum != nil && f > *s.Maximum {
			bad("maximum", fmt.Sprintf("máximo %v", *s.Maximum))
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			bad("type", "debe ser verdadero o falso")
		}
	}
	if len(s.Enum) > 0 {
		for _, e := range s.Enum {
			if e == v {
				return
			}
		}
		bad("enum", fmt.Sprintf("valor no permitido; opciones: %v", s.Enum))
	}
}

// fieldRank orders a section's settings the way a merchant reads them: what
// it says first, then its buttons, its picture, how much it shows, and last
// how it looks. Keys not listed go after the content, by name.
var fieldRank = map[string]int{
	"eyebrow": 1, "heading": 2, "title": 3, "subheading": 4, "storeName": 5, "logoText": 6, "brand": 7, "initial": 8, "about": 9,
	"menu": 20, "links": 21, "legal": 22, "showSearch": 23,
	"ctaLabel": 30, "ctaUrl": 31, "cta2Label": 32, "cta2Url": 33, "url": 34,
	"image": 40, "alt": 41, "monogram": 42,
	"limit": 50, "columns": 51,
	"layout": 60, "style": 61, "align": 62, "height": 63, "reverse": 64, "tone": 65, "overlay": 66,
}

// orderFields returns the keys of props in editor order.
func orderFields(props map[string]Schema) []string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if r, ok := fieldRank[k]; ok {
			return r
		}
		return 15
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}
