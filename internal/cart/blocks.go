package cart

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Checkout blocks: declarative pieces a store or its app adds to the payment
// page (a notice, a required checkbox, an extra field). Apexx renders and
// checks them itself, so no third-party code ever runs where the buyer pays.

// Block kinds.
const (
	BlockNotice   = "notice"
	BlockCheckbox = "checkbox"
	BlockText     = "text"
)

// CheckoutBlock is one block of the payment page.
type CheckoutBlock struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Text      string `json:"text,omitempty"`
	Required  bool   `json:"required,omitempty"`
	MaxLength int    `json:"maxLength,omitempty"`
	Source    string `json:"source,omitempty"` // the app that added it, if any
}

// BlockAnswer is what the buyer answered, kept with the order.
type BlockAnswer struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// CleanCheckoutBlocks validates the blocks of a store.
func CleanCheckoutBlocks(in []CheckoutBlock) ([]CheckoutBlock, []FieldError) {
	if len(in) > 10 {
		return nil, []FieldError{{"blocks", "too_many", "Hasta 10 bloques"}}
	}
	var errs []FieldError
	ids := map[string]bool{}
	for i := range in {
		b := &in[i]
		p := fmt.Sprintf("blocks[%d]", i)
		b.Label, b.Text = strings.TrimSpace(b.Label), strings.TrimSpace(b.Text)
		if b.ID == "" {
			b.ID = slugID(b.Label)
		}
		if !customID.MatchString(b.ID) || ids[b.ID] {
			errs = append(errs, FieldError{p + ".id", "invalid", "Identificador repetido o inválido"})
		}
		ids[b.ID] = true
		if b.Label == "" || utf8.RuneCountInString(b.Label) > 120 || utf8.RuneCountInString(b.Text) > 500 {
			errs = append(errs, FieldError{p + ".label", "invalid", "Título de hasta 120 caracteres y texto de hasta 500"})
		}
		switch b.Kind {
		case BlockNotice:
			b.Required, b.MaxLength = false, 0
		case BlockCheckbox:
			b.MaxLength = 0
		case BlockText:
			if b.MaxLength <= 0 {
				b.MaxLength = 80
			}
			if b.MaxLength > 300 {
				errs = append(errs, FieldError{p + ".maxLength", "invalid", "Hasta 300 caracteres"})
			}
		default:
			errs = append(errs, FieldError{p + ".kind", "invalid", "Tipo: aviso, casilla o campo"})
		}
	}
	return in, errs
}

// CheckBlocks validates the buyer's answers against the store's blocks.
func CheckBlocks(blocks []CheckoutBlock, in map[string]string) ([]BlockAnswer, string) {
	var out []BlockAnswer
	for k := range in {
		known := false
		for _, b := range blocks {
			known = known || (b.ID == k && b.Kind != BlockNotice)
		}
		if !known {
			return nil, "Dato de pago desconocido: " + k
		}
	}
	for _, b := range blocks {
		v := strings.TrimSpace(in[b.ID])
		switch b.Kind {
		case BlockCheckbox:
			on := v == "true" || v == "1"
			if b.Required && !on {
				return nil, "Falta aceptar: " + b.Label
			}
			if on {
				out = append(out, BlockAnswer{b.ID, b.Label, "Sí"})
			}
		case BlockText:
			if v == "" {
				if b.Required {
					return nil, "Falta: " + b.Label
				}
				continue
			}
			if utf8.RuneCountInString(v) > b.MaxLength || strings.ContainsFunc(v, func(r rune) bool { return r < 32 || r == 127 }) {
				return nil, fmt.Sprintf("%s: hasta %d caracteres en una línea", b.Label, b.MaxLength)
			}
			out = append(out, BlockAnswer{b.ID, b.Label, v})
		}
	}
	return out, ""
}
