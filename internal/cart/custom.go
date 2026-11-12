package cart

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/money"
)

// Customizations: fields the buyer fills on a product (engraving, gift
// wrap, a finish), each one able to add to the unit price.

// Custom field kinds.
const (
	CustomText     = "text"
	CustomSelect   = "select"
	CustomCheckbox = "checkbox"
)

// CustomChoice is one option of a select field.
type CustomChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Price int64  `json:"price"` // added per unit, store currency
}

// CustomField is one thing the buyer can personalize.
type CustomField struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Kind      string         `json:"kind"`
	Required  bool           `json:"required,omitempty"`
	MaxLength int            `json:"maxLength,omitempty"` // text
	Price     int64          `json:"price,omitempty"`     // text (when filled) and checkbox (when checked)
	Choices   []CustomChoice `json:"choices,omitempty"`   // select
	Help      string         `json:"help,omitempty"`
}

// CustomValue is what the buyer chose for a field, as kept on the line.
type CustomValue struct {
	Field string      `json:"field"`
	Label string      `json:"label"`
	Value string      `json:"value"`
	Price money.Money `json:"price"`
}

var customID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// CleanCustomFields validates the fields of a product.
func CleanCustomFields(fs []CustomField) ([]CustomField, []FieldError) {
	var errs []FieldError
	if len(fs) > 10 {
		return nil, []FieldError{{"fields", "too_many", "Hasta 10 personalizaciones por producto"}}
	}
	seen := map[string]bool{}
	out := make([]CustomField, 0, len(fs))
	for i, f := range fs {
		p := fmt.Sprintf("fields[%d]", i)
		f.ID, f.Label, f.Help = strings.TrimSpace(f.ID), strings.TrimSpace(f.Label), strings.TrimSpace(f.Help)
		if f.ID == "" {
			f.ID = slugID(f.Label)
		}
		switch {
		case !customID.MatchString(f.ID) || seen[f.ID]:
			errs = append(errs, FieldError{p + ".id", "invalid", "Identificador repetido o inválido"})
		case f.Label == "" || utf8.RuneCountInString(f.Label) > 60:
			errs = append(errs, FieldError{p + ".label", "invalid", "Escribe un nombre de hasta 60 caracteres"})
		case utf8.RuneCountInString(f.Help) > 160:
			errs = append(errs, FieldError{p + ".help", "invalid", "Ayuda de hasta 160 caracteres"})
		case f.Price < 0 || f.Price > 100_000_00:
			errs = append(errs, FieldError{p + ".price", "invalid", "Precio de 0 a 100,000"})
		}
		seen[f.ID] = true
		switch f.Kind {
		case CustomText:
			if f.MaxLength <= 0 {
				f.MaxLength = 40
			}
			if f.MaxLength > 500 {
				errs = append(errs, FieldError{p + ".maxLength", "invalid", "Hasta 500 caracteres"})
			}
			f.Choices = nil
		case CustomCheckbox:
			f.Choices, f.MaxLength, f.Required = nil, 0, false
		case CustomSelect:
			f.MaxLength, f.Price = 0, 0
			if len(f.Choices) < 2 || len(f.Choices) > 30 {
				errs = append(errs, FieldError{p + ".choices", "invalid", "Una lista tiene de 2 a 30 opciones"})
			}
			cs := map[string]bool{}
			for j, c := range f.Choices {
				c.Label = strings.TrimSpace(c.Label)
				if c.ID = strings.TrimSpace(c.ID); c.ID == "" {
					c.ID = slugID(c.Label)
				}
				if c.Label == "" || utf8.RuneCountInString(c.Label) > 60 || cs[c.ID] || !customID.MatchString(c.ID) {
					errs = append(errs, FieldError{fmt.Sprintf("%s.choices[%d]", p, j), "invalid", "Opción vacía, repetida o muy larga"})
				}
				if c.Price < 0 || c.Price > 100_000_00 {
					errs = append(errs, FieldError{fmt.Sprintf("%s.choices[%d].price", p, j), "invalid", "Precio de 0 a 100,000"})
				}
				cs[c.ID] = true
				f.Choices[j] = c
			}
		default:
			errs = append(errs, FieldError{p + ".kind", "invalid", "Tipo: texto, lista o casilla"})
		}
		out = append(out, f)
	}
	return out, errs
}

func slugID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune("áàä", r):
			b.WriteRune('a')
		case strings.ContainsRune("éèë", r):
			b.WriteRune('e')
		case strings.ContainsRune("íìï", r):
			b.WriteRune('i')
		case strings.ContainsRune("óòö", r):
			b.WriteRune('o')
		case strings.ContainsRune("úùü", r):
			b.WriteRune('u')
		case r == 'ñ':
			b.WriteRune('n')
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteRune('-')
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// applyCustom checks what the buyer chose against the fields of the product.
// It returns the values to keep on the line, the surcharge per unit (store
// currency) and, when the choice is not valid, a reason.
func applyCustom(fields []CustomField, in map[string]string) ([]CustomValue, int64, string) {
	var out []CustomValue
	var extra int64
	for k := range in {
		found := false
		for _, f := range fields {
			found = found || f.ID == k
		}
		if !found {
			return nil, 0, "Personalización desconocida: " + k
		}
	}
	for _, f := range fields {
		v := strings.TrimSpace(in[f.ID])
		switch f.Kind {
		case CustomText:
			if v == "" {
				if f.Required {
					return nil, 0, "Falta: " + f.Label
				}
				continue
			}
			if utf8.RuneCountInString(v) > f.MaxLength {
				return nil, 0, fmt.Sprintf("%s: hasta %d caracteres", f.Label, f.MaxLength)
			}
			if strings.ContainsFunc(v, func(r rune) bool { return r < 32 || r == 127 }) {
				return nil, 0, f.Label + ": solo texto en una línea"
			}
			out = append(out, CustomValue{Field: f.ID, Label: f.Label, Value: v, Price: money.Money{Amount: f.Price}})
			extra += f.Price
		case CustomCheckbox:
			if v == "" || v == "false" || v == "0" {
				continue
			}
			out = append(out, CustomValue{Field: f.ID, Label: f.Label, Value: "Sí", Price: money.Money{Amount: f.Price}})
			extra += f.Price
		case CustomSelect:
			if v == "" {
				if f.Required {
					return nil, 0, "Elige: " + f.Label
				}
				continue
			}
			ok := false
			for _, c := range f.Choices {
				if c.ID == v {
					out = append(out, CustomValue{Field: f.ID, Label: f.Label, Value: c.Label, Price: money.Money{Amount: c.Price}})
					extra += c.Price
					ok = true
				}
			}
			if !ok {
				return nil, 0, f.Label + ": opción no válida"
			}
		}
	}
	return out, extra, ""
}

// FieldError points at the part of a definition that is wrong.
type FieldError struct{ Field, Code, Message string }

func (e FieldError) Error() string { return e.Message }
