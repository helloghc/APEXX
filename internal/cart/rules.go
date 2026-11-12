package cart

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Checkout rules: hide payment methods or delivery options when a cart meets
// conditions ("no cash over 10,000", "no express to these postal codes"). A
// store app can add its own answer; if it is slow or fails, it is ignored.

// Payment kinds a rule can hide.
var PaymentKinds = []string{"card", "oxxo", "spei", "credit", "gift_card"}

// CheckoutRule is one declarative rule.
type CheckoutRule struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Active         bool     `json:"active"`
	MinTotal       int64    `json:"minTotal,omitempty"` // items after discounts, store currency
	MaxTotal       int64    `json:"maxTotal,omitempty"`
	PostalPrefixes []string `json:"postalPrefixes,omitempty"`
	Groups         []string `json:"groups,omitempty"` // price groups (B2B)
	SKUs           []string `json:"skus,omitempty"`
	Channels       []string `json:"channels,omitempty"`
	HidePayments   []string `json:"hidePayments,omitempty"`
	HideShipping   []string `json:"hideShipping,omitempty"` // delivery option ids
	Message        string   `json:"message,omitempty"`
}

// RulesInput is what a rule (or the store's app) sees of the cart. No
// personal data: just the postal code.
type RulesInput struct {
	Channel    string      `json:"channel"`
	Currency   string      `json:"currency"`
	Subtotal   int64       `json:"subtotal"`
	PostalCode string      `json:"postalCode"`
	Group      string      `json:"group,omitempty"`
	Lines      []RulesLine `json:"lines"`
}

// RulesLine is one cart line for the rules.
type RulesLine struct {
	SKU  string `json:"sku"`
	Qty  int64  `json:"qty"`
	Unit int64  `json:"unit"`
}

// RulesResult is what the rules decided.
type RulesResult struct {
	HidePayments []string `json:"hidePayments,omitempty"`
	HideShipping []string `json:"hideShipping,omitempty"`
	Messages     []string `json:"messages,omitempty"`
}

// CleanCheckoutRules validates a store's rules.
func CleanCheckoutRules(in []CheckoutRule) ([]CheckoutRule, []FieldError) {
	if len(in) > 50 {
		return nil, []FieldError{{"rules", "too_many", "Hasta 50 reglas"}}
	}
	var errs []FieldError
	ids := map[string]bool{}
	for i := range in {
		r := &in[i]
		p := fmt.Sprintf("rules[%d]", i)
		r.Name, r.Message = strings.TrimSpace(r.Name), strings.TrimSpace(r.Message)
		if r.ID == "" {
			r.ID = slugID(r.Name)
		}
		if r.ID == "" || ids[r.ID] || !customID.MatchString(r.ID) {
			r.ID = fmt.Sprintf("regla-%d", i+1)
		}
		ids[r.ID] = true
		if r.Name == "" || utf8.RuneCountInString(r.Name) > 80 {
			errs = append(errs, FieldError{p + ".name", "invalid", "Escribe un nombre de hasta 80 caracteres"})
		}
		if utf8.RuneCountInString(r.Message) > 160 {
			errs = append(errs, FieldError{p + ".message", "invalid", "Mensaje de hasta 160 caracteres"})
		}
		if r.MinTotal < 0 || r.MaxTotal < 0 || (r.MaxTotal > 0 && r.MaxTotal < r.MinTotal) {
			errs = append(errs, FieldError{p + ".maxTotal", "invalid", "El máximo no puede ser menor que el mínimo"})
		}
		for _, k := range r.HidePayments {
			if !slices.Contains(PaymentKinds, k) {
				errs = append(errs, FieldError{p + ".hidePayments", "invalid", "Método de pago desconocido: " + k})
			}
		}
		if len(r.HidePayments) == 0 && len(r.HideShipping) == 0 {
			errs = append(errs, FieldError{p, "empty", "La regla debe ocultar algún pago o entrega"})
		}
		for _, list := range [][]string{r.PostalPrefixes, r.Groups, r.SKUs, r.Channels, r.HideShipping} {
			if len(list) > 200 {
				errs = append(errs, FieldError{p, "too_many", "Listas de hasta 200 valores"})
			}
		}
	}
	return in, errs
}

// matches tells whether rule r applies to the cart.
func (r CheckoutRule) matches(in RulesInput) bool {
	if !r.Active {
		return false
	}
	if r.MinTotal > 0 && in.Subtotal < r.MinTotal || r.MaxTotal > 0 && in.Subtotal > r.MaxTotal {
		return false
	}
	if len(r.Channels) > 0 && !slices.Contains(r.Channels, in.Channel) {
		return false
	}
	if len(r.Groups) > 0 && !slices.Contains(r.Groups, in.Group) {
		return false
	}
	if len(r.PostalPrefixes) > 0 && !slices.ContainsFunc(r.PostalPrefixes, func(p string) bool { return p != "" && strings.HasPrefix(in.PostalCode, p) }) {
		return false
	}
	if len(r.SKUs) > 0 && !slices.ContainsFunc(in.Lines, func(l RulesLine) bool { return slices.Contains(r.SKUs, l.SKU) }) {
		return false
	}
	return true
}

// evaluateRules applies the store's rules and its app's answer.
func (s *Service) evaluateRules(in RulesInput) RulesResult {
	var out RulesResult
	add := func(dst *[]string, vals []string) {
		for _, v := range vals {
			if !slices.Contains(*dst, v) {
				*dst = append(*dst, v)
			}
		}
	}
	for _, r := range s.CheckoutRules {
		if r.matches(in) {
			add(&out.HidePayments, r.HidePayments)
			add(&out.HideShipping, r.HideShipping)
			if r.Message != "" {
				add(&out.Messages, []string{r.Message})
			}
		}
	}
	if s.ExternalRules != nil {
		if ext, ok := s.ExternalRules(in); ok {
			add(&out.HidePayments, slices.DeleteFunc(ext.HidePayments, func(k string) bool { return !slices.Contains(PaymentKinds, k) }))
			add(&out.HideShipping, ext.HideShipping)
			for _, m := range ext.Messages {
				if m = strings.TrimSpace(m); m != "" && utf8.RuneCountInString(m) <= 160 {
					add(&out.Messages, []string{m})
				}
			}
		}
	}
	return out
}

// PaymentKind maps a checkout payment method to the kind rules talk about.
func PaymentKind(pm string) string {
	switch pm {
	case "oxxo", "spei", "credit":
		return pm
	case "pm_gift_card":
		return "gift_card"
	}
	return "card"
}
