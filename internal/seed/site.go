package seed

import "github.com/helloghc/apexx/internal/builder"

// HomeTemplate is the demo home page (theme "Lienzo"). The main section ids
// are stable because tests and QA guides refer to them.
func HomeTemplate() builder.Template {
	st := builder.Starter("lienzo")
	return builder.Template{
		ID: "tpl_home", Kind: "home", Handle: "index", Name: "Página de inicio",
		SEO:   builder.SEO{Title: "Apexx Demo · Deportes y moda", Description: "Calzado deportivo y moda con envío a todo México."},
		Theme: &builder.ThemeSettings{ID: "lienzo"},
		Regions: map[string][]builder.SectionInstance{
			"header": st.Regions["header"],
			"main": {
				{ID: "sec_hero", Type: "hero", TypeVersion: "2.0.0", Settings: map[string]any{
					"eyebrow": "Nueva temporada", "heading": "Nueva temporada", "subheading": "Hasta 30% en calzado deportivo",
					"ctaLabel": "Ver colección", "ctaUrl": "c", "layout": "full", "height": "large", "monogram": "A"}},
				{ID: "sec_benefits", Type: "benefits", TypeVersion: "1.1.0", Settings: map[string]any{"heading": "Por qué comprar aquí"},
					Blocks: []builder.Block{
						{ID: "b1", Type: "benefit", Settings: map[string]any{"title": "Envío gratis", "text": "desde $1,499"}},
						{ID: "b2", Type: "benefit", Settings: map[string]any{"title": "Devoluciones", "text": "30 días"}},
					}},
				{ID: "sec_new", Type: "product-carousel", TypeVersion: "2.2.0", Settings: map[string]any{"heading": "Novedades", "limit": 8.0}},
				{ID: "sec_promos", Type: "rich-text", TypeVersion: "1.1.0", Settings: map[string]any{"heading": "Promociones", "body": "10% en compras desde $3,000"}},
			},
			"footer": st.Regions["footer"],
		},
	}
}
