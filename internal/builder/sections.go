package builder

func intp(i int) *int           { return &i }
func floatp(f float64) *float64 { return &f }
func boolp(b bool) *bool        { return &b }

// DefaultSections is the starter section catalog.
func DefaultSections() []SectionDefinition {
	return []SectionDefinition{
		{
			Type: "hero", Version: "1.2.0", Name: "Banner principal", Category: "heroes",
			Description: "Imagen de ancho completo con título, texto y botón.",
			EnabledOn:   []string{"home", "collection", "page"}, Hydration: "none",
			Budget: Budget{HTMLBytes: 3000, CSSBytes: 2500, Requests: 1, AboveFoldImages: 1},
			SettingsSchema: Schema{
				Type: "object", Required: []string{"heading", "image", "alt"}, AdditionalProperties: boolp(false),
				Properties: map[string]Schema{
					"heading":    {Type: "string", Title: "Título", MaxLength: intp(80), Widget: "text"},
					"subheading": {Type: "string", Title: "Texto", MaxLength: intp(160), Widget: "textarea"},
					"image":      {Type: "string", Title: "Imagen", Widget: "media"},
					"alt":        {Type: "string", Title: "Texto alternativo", MinLength: intp(5), MaxLength: intp(140)},
					"ctaLabel":   {Type: "string", Title: "Texto del botón", MaxLength: intp(30)},
					"ctaUrl":     {Type: "string", Title: "Enlace del botón", Widget: "link-picker"},
					"height":     {Type: "string", Title: "Altura", Enum: []any{"small", "medium", "large"}, Default: "medium"},
					"overlay":    {Type: "number", Title: "Oscurecer imagen", Minimum: floatp(0), Maximum: floatp(0.8), Default: 0.2, Widget: "slider"},
				},
			},
			Template: `<div class="hero hero--{{.S.height}}"><img src="{{.S.image}}" alt="{{.S.alt}}" width="1600" height="900" fetchpriority="high">{{if .H1}}<h1>{{.S.heading}}</h1>{{else}}<h2>{{.S.heading}}</h2>{{end}}{{with .S.subheading}}<p>{{.}}</p>{{end}}{{with .S.ctaLabel}}<a href="{{$.S.ctaUrl}}">{{.}}</a>{{end}}</div>`,
		},
		{
			Type: "rich-text", Version: "1.0.0", Name: "Texto", Category: "contenido",
			Description: "Título y párrafo.", Hydration: "none",
			Budget: Budget{HTMLBytes: 1500, CSSBytes: 500},
			SettingsSchema: Schema{
				Type: "object", Required: []string{"heading"}, AdditionalProperties: boolp(false),
				Properties: map[string]Schema{
					"heading": {Type: "string", Title: "Título", MaxLength: intp(120)},
					"body":    {Type: "string", Title: "Texto", MaxLength: intp(2000), Widget: "textarea"},
				},
			},
			Template: `{{if .H1}}<h1>{{.S.heading}}</h1>{{else}}<h2>{{.S.heading}}</h2>{{end}}{{with .S.body}}<p>{{.}}</p>{{end}}`,
		},
		{
			Type: "benefits", Version: "1.0.0", Name: "Íconos de beneficios", Category: "contenido",
			Description: "De 2 a 4 beneficios con título y texto.", Hydration: "none",
			Blocks: BlockRules{Allowed: []string{"benefit"}, Min: 2, Max: 4},
			Budget: Budget{HTMLBytes: 1500, CSSBytes: 800},
			SettingsSchema: Schema{Type: "object", AdditionalProperties: boolp(false), Properties: map[string]Schema{
				"heading": {Type: "string", Title: "Título", MaxLength: intp(80)},
			}},
			Template: `{{with .S.heading}}<h2>{{.}}</h2>{{end}}<ul>{{range .Blocks}}<li><strong>{{index .Settings "title"}}</strong> {{index .Settings "text"}}</li>{{end}}</ul>`,
		},
		{
			Type: "product-carousel", Version: "2.1.0", Name: "Carrusel de productos", Category: "productos",
			Description: "Productos de una colección con precio.", Hydration: "visible",
			Budget: Budget{HTMLBytes: 6000, JSBytes: 6000, CSSBytes: 1500, Requests: 2},
			SettingsSchema: Schema{
				Type: "object", Required: []string{"heading"}, AdditionalProperties: boolp(false),
				Properties: map[string]Schema{
					"heading":    {Type: "string", Title: "Título", MaxLength: intp(80)},
					"collection": {Type: "string", Title: "Colección", Widget: "collection-picker"},
					"limit":      {Type: "integer", Title: "Cantidad", Minimum: floatp(1), Maximum: floatp(24), Default: 8.0},
				},
			},
			Template: `<h2>{{.S.heading}}</h2><ul class="carousel">{{range .Products}}<li><a href="{{.URL}}">{{.Title}}</a> <span>{{.Price}}</span></li>{{end}}</ul>`,
		},
		{
			Type: "video-wall", Version: "1.0.0", Name: "Muro de videos", Category: "contenido",
			Description: "Ejemplo de sección pesada: el builder la frena si rompe el presupuesto.", Hydration: "idle",
			Budget: Budget{HTMLBytes: 4000, JSBytes: 90000, CSSBytes: 3000, Requests: 6, AboveFoldImages: 2},
			SettingsSchema: Schema{Type: "object", AdditionalProperties: boolp(false), Properties: map[string]Schema{
				"heading": {Type: "string", Title: "Título", MaxLength: intp(80)},
			}},
			Template: `<h2>{{.S.heading}}</h2>`,
		},
	}
}
