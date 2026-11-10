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
