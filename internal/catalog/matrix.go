package catalog

import (
	"fmt"
	"strings"
)

// Limits of the variant matrix.
const (
	MaxOptions       = 3
	MaxOptionValues  = 50
	MaxVariants      = 250
	DefaultVariantTi = "Única"
)

// MatrixError explains why a set of options cannot produce a matrix.
type MatrixError struct {
	Field   string // e.g. "options[1].values[2]"
	Code    string
	Message string
}

func (e *MatrixError) Error() string { return e.Message }

// NormalizeOptions trims names and values and validates them: at most
// MaxOptions options, unique names, unique non-empty values.
func NormalizeOptions(opts []Option) ([]Option, error) {
	if len(opts) > MaxOptions {
		return nil, &MatrixError{"options", "too_many", fmt.Sprintf("Un producto admite hasta %d opciones", MaxOptions)}
	}
	out := make([]Option, 0, len(opts))
	names := map[string]bool{}
	for i, o := range opts {
		name := strings.TrimSpace(o.Name)
		f := fmt.Sprintf("options[%d]", i)
		if name == "" || len(name) > 40 {
			return nil, &MatrixError{f + ".name", "invalid", "Cada opción necesita un nombre de hasta 40 caracteres"}
		}
		if names[fold(name)] {
			return nil, &MatrixError{f + ".name", "duplicate", fmt.Sprintf("La opción %q está repetida", name)}
		}
		names[fold(name)] = true
		if len(o.Values) == 0 {
			return nil, &MatrixError{f + ".values", "required", fmt.Sprintf("Agrega al menos un valor a %q", name)}
		}
		if len(o.Values) > MaxOptionValues {
			return nil, &MatrixError{f + ".values", "too_many", fmt.Sprintf("%q admite hasta %d valores", name, MaxOptionValues)}
		}
		vals := make([]string, 0, len(o.Values))
		seen := map[string]bool{}
		for j, v := range o.Values {
			v = strings.TrimSpace(v)
			vf := fmt.Sprintf("%s.values[%d]", f, j)
			if v == "" || len(v) > 60 {
				return nil, &MatrixError{vf, "invalid", fmt.Sprintf("Los valores de %q no pueden estar vacíos ni pasar de 60 caracteres", name)}
			}
			if seen[fold(v)] {
				return nil, &MatrixError{vf, "duplicate", fmt.Sprintf("El valor %q está repetido en %q", v, name)}
			}
			seen[fold(v)] = true
			vals = append(vals, v)
		}
		out = append(out, Option{Name: name, Values: vals})
	}
	return out, nil
}

// Combinations returns the cartesian product of the option values in order:
// the last option varies fastest ("Rojo/S", "Rojo/M", "Azul/S", …).
func Combinations(opts []Option) []map[string]string {
	combos := []map[string]string{{}}
	for _, o := range opts {
		next := make([]map[string]string, 0, len(combos)*len(o.Values))
		for _, c := range combos {
			for _, v := range o.Values {
				m := make(map[string]string, len(c)+1)
				for k, x := range c {
					m[k] = x
				}
				m[o.Name] = v
				next = append(next, m)
			}
		}
		combos = next
	}
	return combos
}

// comboKey identifies a combination regardless of the case of names and values.
func comboKey(opts []Option, m map[string]string) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		v, ok := m[o.Name]
		if !ok { // legacy variants may use another case for the option name
			for k, x := range m {
				if fold(k) == fold(o.Name) {
					v = x
					break
				}
			}
		}
		parts[i] = fold(v)
	}
	return strings.Join(parts, "\x1f")
}

// VariantTitle renders the values of a combination: "Rojo / M".
func VariantTitle(opts []Option, m map[string]string) string {
	if len(opts) == 0 {
		return DefaultVariantTi
	}
	parts := make([]string, len(opts))
	for i, o := range opts {
		parts[i] = m[o.Name]
	}
	return strings.Join(parts, " / ")
}

// GenerateVariants builds the full variant matrix for opts. A variant in
// existing whose options equal a combination is kept as it is (SKU, barcode,
// weight, image), so regenerating after adding a value never loses work.
// New combinations get a SKU derived from skuBase and their values. Existing
// variants that match no combination are dropped. With no options the result
// is a single default variant.
func GenerateVariants(skuBase string, opts []Option, existing []Variant) ([]Variant, error) {
	opts, err := NormalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	skuBase = strings.ToUpper(strings.Trim(Slug(skuBase), "-"))
	if skuBase == "" {
		skuBase = "SKU"
	}
	used := map[string]bool{}
	for _, v := range existing {
		used[v.SKU] = true
	}
	if len(opts) == 0 {
		if len(existing) > 0 {
			v := existing[0]
			v.Options = nil
			if strings.TrimSpace(v.Title) == "" {
				v.Title = DefaultVariantTi
			}
			return []Variant{v}, nil
		}
		return []Variant{{SKU: skuBase, Title: DefaultVariantTi}}, nil
	}
	total := 1
	for _, o := range opts {
		total *= len(o.Values)
		if total > MaxVariants {
			return nil, &MatrixError{"options", "too_many_variants", fmt.Sprintf("Las opciones generan más de %d variantes; reduce valores", MaxVariants)}
		}
	}
	byKey := map[string]Variant{}
	for _, v := range existing {
		if len(v.Options) == len(opts) {
			k := comboKey(opts, v.Options)
			if _, dup := byKey[k]; !dup {
				byKey[k] = v
			}
		}
	}
	// A product that had a single default variant keeps its weight as the
	// starting point for the new variants.
	var seedWeight int64
	if len(existing) == 1 && len(existing[0].Options) == 0 {
		seedWeight = existing[0].WeightGrams
	}
	out := make([]Variant, 0, total)
	for _, c := range Combinations(opts) {
		title := VariantTitle(opts, c)
		if v, ok := byKey[comboKey(opts, c)]; ok {
			v.Options, v.Title = c, title
			out = append(out, v)
			continue
		}
		parts := []string{skuBase}
		for _, o := range opts {
			if s := strings.ToUpper(Slug(c[o.Name])); s != "" {
				parts = append(parts, s)
			}
		}
		sku := strings.Join(parts, "-")
		for n := 2; used[sku]; n++ {
			sku = fmt.Sprintf("%s-%d", strings.Join(parts, "-"), n)
		}
		used[sku] = true
		out = append(out, Variant{SKU: sku, Title: title, Options: c, WeightGrams: seedWeight})
	}
	return out, nil
}

// ValidateVariantOptions checks that every variant has exactly one value per
// option, that the value is listed in the option, and that no two variants
// share a combination.
func ValidateVariantOptions(opts []Option, variants []Variant) error {
	if len(opts) == 0 {
		return nil
	}
	seen := map[string]int{}
	for i, v := range variants {
		f := fmt.Sprintf("variants[%d].options", i)
		if len(v.Options) != len(opts) {
			return &MatrixError{f, "invalid", fmt.Sprintf("La variante %s debe tener un valor para cada opción", v.SKU)}
		}
		for _, o := range opts {
			val, ok := v.Options[o.Name]
			if !ok {
				return &MatrixError{f, "invalid", fmt.Sprintf("A la variante %s le falta la opción %q", v.SKU, o.Name)}
			}
			found := false
			for _, x := range o.Values {
				found = found || x == val
			}
			if !found {
				return &MatrixError{f, "invalid", fmt.Sprintf("%q no es un valor de %q (variante %s)", val, o.Name, v.SKU)}
			}
		}
		k := comboKey(opts, v.Options)
		if j, dup := seen[k]; dup {
			return &MatrixError{f, "duplicate", fmt.Sprintf("Las variantes %s y %s tienen la misma combinación", variants[j].SKU, v.SKU)}
		}
		seen[k] = i
	}
	return nil
}
