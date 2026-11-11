// Package fields lets a store add its own fields to customers, orders and
// companies. Definitions live in one document per resource; values in
// custom_values, with personal values encrypted. The checks here run before
// anything is stored, so a value always matches its definition.
package fields

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Resources that accept custom fields.
var Resources = map[string]string{"customer": "clientes", "order": "pedidos", "company": "empresas"}

// Types of field.
var Types = map[string]bool{"text": true, "textarea": true, "number": true, "boolean": true, "date": true, "select": true, "url": true, "email": true}

// Def is one field of a resource.
type Def struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"` // select
	Required bool     `json:"required"`
	// Personal values are encrypted and cannot be used in segments.
	Personal bool   `json:"personal"`
	Help     string `json:"help,omitempty"`
}

// Defs is the list of fields of one resource, in display order.
type Defs struct {
	Fields []Def `json:"fields"`
}

// FieldError is a problem with one field.
type FieldError struct{ Field, Message string }

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Clean validates the definitions.
func (d *Defs) Clean() error {
	if len(d.Fields) > 50 {
		return FieldError{"fields", "Hasta 50 campos por tipo"}
	}
	seen := map[string]bool{}
	for i := range d.Fields {
		f := &d.Fields[i]
		f.Key, f.Label, f.Help = strings.TrimSpace(f.Key), strings.TrimSpace(f.Label), strings.TrimSpace(f.Help)
		p := fmt.Sprintf("fields.%d", i)
		switch {
		case !keyRe.MatchString(f.Key):
			return FieldError{p + ".key", "La clave usa minúsculas, números y guion bajo, y empieza con letra (por ejemplo talla_preferida)"}
		case seen[f.Key]:
			return FieldError{p + ".key", "Clave repetida: " + f.Key}
		case f.Label == "" || len(f.Label) > 80:
			return FieldError{p + ".label", "Escribe el nombre del campo (hasta 80 caracteres)"}
		case !Types[f.Type]:
			return FieldError{p + ".type", "Tipo de campo desconocido"}
		case f.Type == "select" && len(f.Options) == 0:
			return FieldError{p + ".options", "Agrega al menos una opción"}
		case len(f.Help) > 200:
			return FieldError{p + ".help", "Ayuda de hasta 200 caracteres"}
		}
		if f.Type != "select" {
			f.Options = nil
		}
		opts := f.Options[:0]
		for _, o := range f.Options {
			if o = strings.TrimSpace(o); o != "" && len(o) <= 80 {
				opts = append(opts, o)
			}
		}
		f.Options = opts
		seen[f.Key] = true
	}
	return nil
}

// Find returns the definition of a key.
func (d Defs) Find(key string) (Def, bool) {
	for _, f := range d.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Def{}, false
}

// Split checks the values against the definitions and separates the plain
// values from the personal ones. Unknown keys are refused; empty values
// remove the field.
func (d Defs) Split(values map[string]string) (plain, personal map[string]string, err error) {
	plain, personal = map[string]string{}, map[string]string{}
	for k := range values {
		if _, ok := d.Find(k); !ok {
			return nil, nil, FieldError{k, "Campo desconocido"}
		}
	}
	for _, f := range d.Fields {
		v := strings.TrimSpace(values[f.Key])
		if v == "" {
			if f.Required {
				return nil, nil, FieldError{f.Key, "Completa " + f.Label}
			}
			continue
		}
		if len(v) > 5000 {
			return nil, nil, FieldError{f.Key, "Hasta 5,000 caracteres"}
		}
		switch f.Type {
		case "number":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return nil, nil, FieldError{f.Key, f.Label + " debe ser un número"}
			}
		case "boolean":
			if v != "true" && v != "false" {
				return nil, nil, FieldError{f.Key, f.Label + ": sí o no"}
			}
		case "date":
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return nil, nil, FieldError{f.Key, f.Label + ": fecha AAAA-MM-DD"}
			}
		case "select":
			ok := false
			for _, o := range f.Options {
				ok = ok || o == v
			}
			if !ok {
				return nil, nil, FieldError{f.Key, f.Label + ": elige una de las opciones"}
			}
		case "url":
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return nil, nil, FieldError{f.Key, f.Label + ": dirección web completa (https://…)"}
			}
		case "email":
			if !strings.Contains(v, "@") || strings.ContainsAny(v, " \t") {
				return nil, nil, FieldError{f.Key, f.Label + ": correo válido"}
			}
		case "text":
			if strings.ContainsAny(v, "\n\r") {
				v = strings.Join(strings.Fields(v), " ")
			}
		}
		if f.Personal {
			personal[f.Key] = v
		} else {
			plain[f.Key] = v
		}
	}
	return plain, personal, nil
}
