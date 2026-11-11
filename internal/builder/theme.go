package builder

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Font is a self-hosted typeface (woff2, latin subset) served from
// /s/fonts/<File>.
type Font struct {
	ID       string `json:"id"`
	Family   string `json:"family"`   // CSS family name
	File     string `json:"file"`     // file name without extension
	Weights  string `json:"weights"`  // "300 800" for variable fonts, "400" for static
	Fallback string `json:"fallback"` // CSS fallback stack
	Kind     string `json:"kind"`     // serif | sans | mono | rounded
}

// Fonts is the bundled font library merchants can choose from.
var Fonts = map[string]Font{
	"cormorant":        {ID: "cormorant", Family: "Cormorant", File: "cormorant", Weights: "300 700", Fallback: "Georgia, 'Times New Roman', serif", Kind: "serif"},
	"jost":             {ID: "jost", Family: "Jost", File: "jost", Weights: "100 900", Fallback: "'Helvetica Neue', Arial, sans-serif", Kind: "sans"},
	"sora":             {ID: "sora", Family: "Sora", File: "sora", Weights: "100 800", Fallback: "'Segoe UI', Arial, sans-serif", Kind: "sans"},
	"jetbrains-mono":   {ID: "jetbrains-mono", Family: "JetBrains Mono", File: "jetbrains-mono", Weights: "100 800", Fallback: "ui-monospace, Menlo, Consolas, monospace", Kind: "mono"},
	"fredoka":          {ID: "fredoka", Family: "Fredoka", File: "fredoka", Weights: "300 700", Fallback: "'Arial Rounded MT Bold', Arial, sans-serif", Kind: "rounded"},
	"nunito":           {ID: "nunito", Family: "Nunito", File: "nunito", Weights: "200 1000", Fallback: "'Segoe UI', Arial, sans-serif", Kind: "rounded"},
	"instrument-serif": {ID: "instrument-serif", Family: "Instrument Serif", File: "instrument-serif", Weights: "400", Fallback: "Georgia, serif", Kind: "serif"},
	"manrope":          {ID: "manrope", Family: "Manrope", File: "manrope", Weights: "200 800", Fallback: "'Segoe UI', Arial, sans-serif", Kind: "sans"},
}

// ColorTokens a theme must define.
var ColorTokens = []string{"bg", "surface", "text", "muted", "primary", "primaryText", "accent", "accentText", "border", "sale"}

// Theme is a complete visual system for a storefront.
type Theme struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Vertical    string            `json:"vertical"`
	Description string            `json:"description"`
	Colors      map[string]string `json:"colors"`
	Display     string            `json:"display"` // font id
	Body        string            `json:"body"`    // font id
	Accent      string            `json:"accentFont,omitempty"`
	Radius      int               `json:"radius"`      // px, cards and inputs
	ButtonStyle string            `json:"buttonStyle"` // solid | outline | pill
	CardStyle   string            `json:"cardStyle"`   // flat | bordered | soft
	HeaderStyle string            `json:"headerStyle"` // centered | split | compact
	Density     string            `json:"density"`     // airy | regular | compact
	ImageRatio  string            `json:"imageRatio"`  // "3 / 4", "1 / 1", "4 / 5"
	ColorScheme string            `json:"colorScheme"` // light | dark
}

// Themes are the original Apexx storefront themes.
var Themes = map[string]Theme{
	"lienzo": {
		ID: "lienzo", Name: "Lienzo", Vertical: "Moda y accesorios",
		Description: "Editorial y silencioso: serifa con contraste alto, mucho aire y fotografía protagonista.",
		Colors: map[string]string{"bg": "#F6F4F0", "surface": "#FFFFFF", "text": "#191817", "muted": "#625C55", "primary": "#191817",
			"primaryText": "#FFFFFF", "accent": "#8A5536", "accentText": "#FFFFFF", "border": "#E2DCD3", "sale": "#9E3426"},
		Display: "cormorant", Body: "jost", Radius: 0, ButtonStyle: "outline", CardStyle: "flat", HeaderStyle: "centered",
		Density: "airy", ImageRatio: "3 / 4", ColorScheme: "light",
	},
	"voltio": {
		ID: "voltio", Name: "Voltio", Vertical: "Tecnología y electrónica",
		Description: "Oscuro y preciso: grotesca geométrica, datos en monoespaciada y un azul eléctrico que guía la compra.",
		Colors: map[string]string{"bg": "#0B0D12", "surface": "#141821", "text": "#F1F3F8", "muted": "#9AA3B6", "primary": "#5B8CFF",
			"primaryText": "#05070C", "accent": "#2DD4EE", "accentText": "#05070C", "border": "#252B39", "sale": "#FF6B81"},
		Display: "sora", Body: "sora", Accent: "jetbrains-mono", Radius: 10, ButtonStyle: "solid", CardStyle: "bordered", HeaderStyle: "split",
		Density: "compact", ImageRatio: "1 / 1", ColorScheme: "dark",
	},
	"mercadito": {
		ID: "mercadito", Name: "Mercadito", Vertical: "Súper, abarrotes y frescos",
		Description: "Cercano y rápido: letras redondas, colores frescos, grillas densas y botón de agregar en cada producto.",
		Colors: map[string]string{"bg": "#FFFFFF", "surface": "#F2F7EF", "text": "#1B271C", "muted": "#566557", "primary": "#1C7536",
			"primaryText": "#FFFFFF", "accent": "#F5A524", "accentText": "#2A1A00", "border": "#DCE6D8", "sale": "#C62E1C"},
		Display: "fredoka", Body: "nunito", Radius: 16, ButtonStyle: "pill", CardStyle: "soft", HeaderStyle: "compact",
		Density: "compact", ImageRatio: "1 / 1", ColorScheme: "light",
	},
	"balsamo": {
		ID: "balsamo", Name: "Bálsamo", Vertical: "Belleza y bienestar",
		Description: "Suave y refinado: serifa fina con cursivas, tonos empolvados y formas redondeadas que invitan a tocar.",
		Colors: map[string]string{"bg": "#FBF5F2", "surface": "#FFFFFF", "text": "#2D2729", "muted": "#6D6265", "primary": "#74495A",
			"primaryText": "#FFFFFF", "accent": "#C9D3C2", "accentText": "#26302A", "border": "#EBDDD7", "sale": "#9C3448"},
		Display: "instrument-serif", Body: "manrope", Radius: 20, ButtonStyle: "pill", CardStyle: "soft", HeaderStyle: "centered",
		Density: "airy", ImageRatio: "4 / 5", ColorScheme: "light",
	},
}

// DefaultThemeID is used when a site has no theme.
const DefaultThemeID = "lienzo"

// ThemeList returns the themes in a stable order.
func ThemeList() []Theme {
	order := []string{"lienzo", "voltio", "mercadito", "balsamo"}
	out := make([]Theme, 0, len(order))
	for _, id := range order {
		out = append(out, Themes[id])
	}
	return out
}

// FontList returns the bundled fonts sorted by family.
func FontList() []Font {
	out := make([]Font, 0, len(Fonts))
	for _, f := range Fonts {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}

// ThemeSettings is what a site stores: a base theme plus merchant overrides.
type ThemeSettings struct {
	ID      string            `json:"id"`
	Colors  map[string]string `json:"colors,omitempty"`
	Display string            `json:"display,omitempty"`
	Body    string            `json:"body,omitempty"`
	Radius  *int              `json:"radius,omitempty"`
}

// ResolvedTheme is a theme with overrides applied.
type ResolvedTheme struct {
	Theme
	DisplayFont Font
	BodyFont    Font
	AccentFont  *Font
}

// Resolve applies overrides; unknown ids fall back to the default theme.
func Resolve(ts *ThemeSettings) ResolvedTheme {
	id := DefaultThemeID
	if ts != nil && ts.ID != "" {
		if _, ok := Themes[ts.ID]; ok {
			id = ts.ID
		}
	}
	base := Themes[id]
	t := base
	t.Colors = map[string]string{}
	for k, v := range base.Colors {
		t.Colors[k] = v
	}
	if ts != nil {
		for k, v := range ts.Colors {
			if _, ok := base.Colors[k]; ok && validHex(v) {
				t.Colors[k] = strings.ToUpper(v)
			}
		}
		if _, ok := Fonts[ts.Display]; ok {
			t.Display = ts.Display
		}
		if _, ok := Fonts[ts.Body]; ok {
			t.Body = ts.Body
		}
		if ts.Radius != nil && *ts.Radius >= 0 && *ts.Radius <= 32 {
			t.Radius = *ts.Radius
		}
	}
	r := ResolvedTheme{Theme: t, DisplayFont: Fonts[t.Display], BodyFont: Fonts[t.Body]}
	if t.Accent != "" {
		f := Fonts[t.Accent]
		r.AccentFont = &f
	}
	return r
}

func validHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

func luminance(hex string) float64 {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	ch := func(c uint64) float64 {
		x := float64(c) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(v>>16&0xff) + 0.7152*ch(v>>8&0xff) + 0.0722*ch(v&0xff)
}

// Contrast returns the WCAG 2.x contrast ratio of two #RRGGBB colors.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// ContrastPairs are the text/background pairs that must reach 4.5:1.
var ContrastPairs = [][2]string{
	{"text", "bg"}, {"text", "surface"}, {"muted", "bg"}, {"muted", "surface"},
	{"primaryText", "primary"}, {"accentText", "accent"}, {"sale", "bg"}, {"sale", "surface"},
}

// ValidateTheme checks a site's theme settings and appends issues.
func ValidateTheme(ts *ThemeSettings, out *[]Issue) {
	if ts == nil {
		return
	}
	if _, ok := Themes[ts.ID]; !ok {
		*out = append(*out, Issue{Path: "/theme/id", Code: "unknown_theme", Message: "Tema desconocido: " + ts.ID, Blocking: true})
		return
	}
	for k, v := range ts.Colors {
		if _, ok := Themes[ts.ID].Colors[k]; !ok {
			*out = append(*out, Issue{Path: "/theme/colors/" + k, Code: "unknown_token", Message: "Color desconocido: " + k, Blocking: true})
		} else if !validHex(v) {
			*out = append(*out, Issue{Path: "/theme/colors/" + k, Code: "invalid_color", Message: "Usa un color en formato #RRGGBB", Blocking: true})
		}
	}
	for path, f := range map[string]string{"/theme/display": ts.Display, "/theme/body": ts.Body} {
		if f != "" {
			if _, ok := Fonts[f]; !ok {
				*out = append(*out, Issue{Path: path, Code: "unknown_font", Message: "Fuente no disponible: " + f, Blocking: true})
			}
		}
	}
	if ts.Radius != nil && (*ts.Radius < 0 || *ts.Radius > 32) {
		*out = append(*out, Issue{Path: "/theme/radius", Code: "out_of_range", Message: "El redondeo debe estar entre 0 y 32 px", Blocking: true})
	}
	rt := Resolve(ts)
	for _, p := range ContrastPairs {
		c := Contrast(rt.Colors[p[0]], rt.Colors[p[1]])
		if c < 4.5 {
			*out = append(*out, Issue{Path: "/theme/colors/" + p[0], Code: "low_contrast",
				Message:  fmt.Sprintf("El contraste entre %s y %s es %.2f:1; necesita al menos 4.5:1 para leerse bien", tokenLabel(p[0]), tokenLabel(p[1]), c),
				Blocking: true})
		}
	}
}

func tokenLabel(k string) string {
	return map[string]string{"bg": "el fondo", "surface": "las tarjetas", "text": "el texto", "muted": "el texto secundario",
		"primary": "el botón", "primaryText": "el texto del botón", "accent": "el acento", "accentText": "el texto sobre el acento",
		"border": "los bordes", "sale": "el precio de oferta"}[k]
}

// AssetVersion busts caches of theme CSS and fonts when the renderer changes.
const AssetVersion = "3"

func fontFace(f Font) string {
	return fmt.Sprintf("@font-face{font-family:%q;src:url(/s/fonts/%s.woff2) format(\"woff2\");font-weight:%s;font-style:normal;font-display:swap}",
		f.Family, f.File, f.Weights)
}

// Head returns the <head> fragment for the theme: preload of the display
// font, @font-face rules, color/font/radius variables and the stylesheets.
func (rt ResolvedTheme) Head() template.HTML {
	var b strings.Builder
	fmt.Fprintf(&b, `<link rel="preload" href="/s/fonts/%s.woff2" as="font" type="font/woff2" crossorigin>`, rt.DisplayFont.File)
	fmt.Fprintf(&b, `<link rel="icon" href="/favicon.ico">`)
	fmt.Fprintf(&b, `<link rel="stylesheet" href="/s/assets/store.css?v=%s"><link rel="stylesheet" href="/s/assets/theme-%s.css?v=%s">`, AssetVersion, rt.ID, AssetVersion)
	b.WriteString("<style>")
	seen := map[string]bool{}
	for _, f := range []Font{rt.DisplayFont, rt.BodyFont} {
		if !seen[f.ID] {
			b.WriteString(fontFace(f))
			seen[f.ID] = true
		}
	}
	if rt.AccentFont != nil && !seen[rt.AccentFont.ID] {
		b.WriteString(fontFace(*rt.AccentFont))
	}
	b.WriteString(":root{")
	for _, k := range ColorTokens {
		fmt.Fprintf(&b, "--c-%s:%s;", k, rt.Colors[k])
	}
	fmt.Fprintf(&b, "--f-display:%q,%s;--f-body:%q,%s;", rt.DisplayFont.Family, rt.DisplayFont.Fallback, rt.BodyFont.Family, rt.BodyFont.Fallback)
	if rt.AccentFont != nil {
		fmt.Fprintf(&b, "--f-accent:%q,%s;", rt.AccentFont.Family, rt.AccentFont.Fallback)
	} else {
		b.WriteString("--f-accent:var(--f-body);")
	}
	fmt.Fprintf(&b, "--radius:%dpx;--ratio:%s;color-scheme:%s}", rt.Radius, rt.ImageRatio, rt.ColorScheme)
	b.WriteString("</style>")
	return template.HTML(b.String())
}

// BodyClass returns the classes that select the theme's section styles.
func (rt ResolvedTheme) BodyClass() string {
	return fmt.Sprintf("t-%s btn-%s card-%s hdr-%s dens-%s", rt.ID, rt.ButtonStyle, rt.CardStyle, rt.HeaderStyle, rt.Density)
}
