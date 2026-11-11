package builder

func blk(id, typ string, kv ...string) Block {
	s := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		s[kv[i]] = kv[i+1]
	}
	return Block{ID: id, Type: typ, Settings: s}
}

func sec(id, typ string, settings map[string]any, blocks ...Block) SectionInstance {
	return SectionInstance{ID: id, Type: typ, Settings: settings, Blocks: blocks}
}

func footerFor(store, about string) SectionInstance {
	return sec("ft", "footer", map[string]any{
		"storeName": store, "about": about,
		"social": "Instagram | https://instagram.com\nTikTok | https://tiktok.com\nFacebook | https://facebook.com",
		"legal":  "© 2026 " + store + ". Todos los derechos reservados.",
	},
		blk("ft-1", "column", "title", "Tienda", "links", "Novedades | c\nMás vendidos | c\nOfertas | c?q=oferta"),
		blk("ft-2", "column", "title", "Ayuda", "links", "Envíos y entregas | #\nCambios y devoluciones | #\nPreguntas frecuentes | #faq"),
		blk("ft-3", "column", "title", "Nosotros", "links", "Nuestra historia | #\nTrabaja con nosotros | #\nAviso de privacidad | #"),
	)
}

// Starter returns the complete home template a site starts with for a theme.
func Starter(themeID string) Template {
	switch themeID {
	case "voltio":
		return Template{
			ID: "tpl_home", Kind: "home", Handle: "index", Name: "Inicio",
			SEO:   SEO{Title: "Voltio · Tecnología que sí rinde", Description: "Laptops, audio y accesorios con envío en 24 h, meses sin intereses y garantía real."},
			Theme: &ThemeSettings{ID: "voltio"},
			Regions: map[string][]SectionInstance{
				"header": {
					sec("ann", "announcement", map[string]any{"text": "Hasta 12 meses sin intereses con tarjetas participantes.", "linkLabel": "Ver condiciones", "linkUrl": "#faq"}),
					sec("hdr", "header", map[string]any{"logoText": "VOLTIO", "menu": "Laptops | c?q=laptop\nAudio | c?q=audio\nGaming | c?q=gaming\nAccesorios | c?q=accesorio\nOfertas | c?q=oferta", "showSearch": true}),
				},
				"main": {
					sec("hero", "hero", map[string]any{"eyebrow": "Nuevo · Serie X", "heading": "Potencia sin ruido. Batería para todo el día.",
						"subheading": "Equipos probados por nuestro equipo técnico, con instalación lista y soporte en español.",
						"ctaLabel":   "Comprar ahora", "ctaUrl": "c", "cta2Label": "Comparar modelos", "cta2Url": "c?q=laptop", "layout": "split", "height": "large", "monogram": "X"}),
					sec("bnf", "benefits", map[string]any{"heading": "Por qué comprar aquí"},
						blk("b1", "benefit", "title", "Envío en 24 h", "text", "En CDMX, GDL y MTY"),
						blk("b2", "benefit", "title", "12 MSI", "text", "Con tarjetas participantes"),
						blk("b3", "benefit", "title", "Garantía de 2 años", "text", "Directo con nosotros"),
						blk("b4", "benefit", "title", "Soporte técnico", "text", "Por chat, sin costo")),
					sec("cats", "collection-tiles", map[string]any{"heading": "Explora por categoría", "style": "square"},
						blk("t1", "tile", "title", "Laptops", "caption", "Trabajo y creatividad", "link", "c?q=laptop"),
						blk("t2", "tile", "title", "Audio", "caption", "Audífonos y bocinas", "link", "c?q=audio"),
						blk("t3", "tile", "title", "Gaming", "caption", "Consolas y periféricos", "link", "c?q=gaming"),
						blk("t4", "tile", "title", "Hogar inteligente", "caption", "Luces, cámaras y más", "link", "c?q=hogar")),
					sec("new", "product-grid", map[string]any{"heading": "Lo más nuevo", "linkLabel": "Ver catálogo", "limit": 8.0, "columns": 4.0}),
					sec("spec", "image-text", map[string]any{"eyebrow": "Hecho para durar", "heading": "Te ayudamos a elegir el equipo correcto",
						"body":     "Cuéntanos para qué lo usarás y te recomendamos tres opciones con sus especificaciones reales, sin letra chiquita.",
						"ctaLabel": "Hablar con un experto", "ctaUrl": "#faq", "reverse": true}),
					sec("deals", "product-carousel", map[string]any{"heading": "Ofertas de la semana", "limit": 10.0}),
					sec("brands", "logo-strip", map[string]any{"heading": "Distribuidor autorizado"},
						blk("l1", "logo", "name", "Norte"), blk("l2", "logo", "name", "Sierra"), blk("l3", "logo", "name", "Pacífico"), blk("l4", "logo", "name", "Converse")),
					sec("faq", "faq", map[string]any{"heading": "Preguntas frecuentes"},
						blk("q1", "question", "q", "¿Cuánto tarda mi pedido?", "a", "En ciudades principales llega en 24 horas hábiles; en el resto del país, de 2 a 4 días."),
						blk("q2", "question", "q", "¿Puedo pagar a meses?", "a", "Sí, hasta 12 meses sin intereses con tarjetas de crédito participantes."),
						blk("q3", "question", "q", "¿Cómo funciona la garantía?", "a", "Todos los equipos tienen 2 años de garantía con nosotros. Si falla, lo recogemos sin costo.")),
				},
				"footer": {
					sec("news", "newsletter", map[string]any{"heading": "Lanzamientos antes que nadie", "body": "Un correo al mes con novedades y ofertas reales. Sin spam.", "buttonLabel": "Avisarme"}),
					footerFor("Voltio", "Tecnología seleccionada y probada, con envío rápido y soporte en español."),
				},
			},
		}
	case "mercadito":
		return Template{
			ID: "tpl_home", Kind: "home", Handle: "index", Name: "Inicio",
			SEO:   SEO{Title: "Mercadito · Tu súper a domicilio", Description: "Frutas, verduras y despensa con entrega el mismo día. Paga al recibir o con tarjeta."},
			Theme: &ThemeSettings{ID: "mercadito"},
			Regions: map[string][]SectionInstance{
				"header": {
					sec("ann", "announcement", map[string]any{"text": "Envío gratis en compras desde $1,499 · Entrega hoy si pides antes de las 2 pm"}),
					sec("hdr", "header", map[string]any{"logoText": "mercadito", "menu": "Frutas y verduras | c?q=fruta\nDespensa | c?q=despensa\nLácteos | c?q=leche\nLimpieza | c?q=limpieza\nPromos | c?q=oferta", "showSearch": true}),
				},
				"main": {
					sec("hero", "hero", map[string]any{"eyebrow": "Fresco todos los días", "heading": "El súper de la semana, en tu puerta hoy mismo",
						"subheading": "Elegimos tu fruta como si fuera para nosotros. Si algo no te gusta, te lo reponemos.",
						"ctaLabel":   "Empezar a llenar el carrito", "ctaUrl": "c", "layout": "split", "height": "medium", "monogram": "m"}),
					sec("cats", "collection-tiles", map[string]any{"heading": "Pasillos", "style": "pill"},
						blk("t1", "tile", "title", "Frutas", "link", "c?q=fruta"), blk("t2", "tile", "title", "Verduras", "link", "c?q=verdura"),
						blk("t3", "tile", "title", "Lácteos", "link", "c?q=leche"), blk("t4", "tile", "title", "Panadería", "link", "c?q=pan"),
						blk("t5", "tile", "title", "Despensa", "link", "c?q=despensa"), blk("t6", "tile", "title", "Bebidas", "link", "c?q=bebida"),
						blk("t7", "tile", "title", "Limpieza", "link", "c?q=limpieza"), blk("t8", "tile", "title", "Mascotas", "link", "c?q=mascota")),
					sec("deals", "product-grid", map[string]any{"heading": "Promos de hoy", "linkLabel": "Ver todas", "limit": 12.0, "columns": 6.0}),
					sec("bnf", "benefits", map[string]any{},
						blk("b1", "benefit", "title", "Entrega el mismo día", "text", "Pide antes de las 2 pm"),
						blk("b2", "benefit", "title", "Frescura garantizada", "text", "Si no te gusta, lo reponemos"),
						blk("b3", "benefit", "title", "Paga como quieras", "text", "Tarjeta o al recibir")),
					sec("more", "product-carousel", map[string]any{"heading": "Lo que más se llevan", "limit": 12.0}),
					sec("story", "image-text", map[string]any{"eyebrow": "De la huerta a tu mesa", "heading": "Compramos directo a productores locales",
						"body":     "Así pagamos mejor a quien cultiva y tú recibes producto de temporada, más fresco y a mejor precio.",
						"ctaLabel": "Conoce a nuestros productores", "ctaUrl": "#"}),
					sec("quotes", "testimonials", map[string]any{"heading": "Lo que dicen nuestros vecinos"},
						blk("r1", "quote", "quote", "Llegó todo en 3 horas y los aguacates estaban perfectos.", "author", "Mariana", "detail", "Coyoacán"),
						blk("r2", "quote", "quote", "Me ahorro una hora de súper cada semana. Ya no regreso.", "author", "Jorge", "detail", "Zapopan"),
						blk("r3", "quote", "quote", "Me cambiaron un producto sin preguntas. Así da gusto.", "author", "Lucía", "detail", "San Pedro")),
				},
				"footer": {
					sec("news", "newsletter", map[string]any{"heading": "Promos del fin de semana", "body": "Te avisamos cada jueves qué está en oferta.", "buttonLabel": "Quiero las promos"}),
					footerFor("Mercadito", "Tu súper de confianza, con producto local y entrega el mismo día."),
				},
			},
		}
	case "balsamo":
		return Template{
			ID: "tpl_home", Kind: "home", Handle: "index", Name: "Inicio",
			SEO:   SEO{Title: "Bálsamo · Cuidado de la piel consciente", Description: "Rutinas sencillas con ingredientes que funcionan. Fórmulas veganas, probadas dermatológicamente."},
			Theme: &ThemeSettings{ID: "balsamo"},
			Regions: map[string][]SectionInstance{
				"header": {
					sec("ann", "announcement", map[string]any{"text": "Muestra de regalo en cada pedido · Envío gratis desde $1,499"}),
					sec("hdr", "header", map[string]any{"logoText": "Bálsamo", "menu": "Rostro | c?q=rostro\nCuerpo | c?q=cuerpo\nRituales | c?q=kit\nIngredientes | #ingredientes", "showSearch": true}),
				},
				"main": {
					sec("hero", "hero", map[string]any{"eyebrow": "Colección Calma", "heading": "Tu piel, en paz",
						"subheading": "Tres pasos, ingredientes que reconoces y resultados que se notan en cuatro semanas.",
						"ctaLabel":   "Descubrir la rutina", "ctaUrl": "c", "layout": "full", "height": "large", "monogram": "b"}),
					sec("intro", "rich-text", map[string]any{"eyebrow": "Nuestra filosofía", "heading": "Menos productos, mejores hábitos",
						"body": "Formulamos en pequeños lotes en Guadalajara, sin fragancias sintéticas y con empaques que puedes rellenar.", "align": "center"}),
					sec("best", "product-grid", map[string]any{"heading": "Favoritos de la comunidad", "linkLabel": "Ver todo", "limit": 4.0, "columns": 4.0}),
					sec("ritual", "image-text", map[string]any{"eyebrow": "Ritual de noche", "heading": "Limpia, nutre y descansa",
						"body":     "Un bálsamo limpiador que se derrite, un suero de niacinamida y una crema que repara mientras duermes.",
						"ctaLabel": "Armar mi ritual", "ctaUrl": "c?q=kit"}),
					sec("cats", "collection-tiles", map[string]any{"heading": "Encuentra lo que tu piel pide", "style": "tall"},
						blk("t1", "tile", "title", "Hidratación", "caption", "Piel seca o tirante", "link", "c?q=hidratante"),
						blk("t2", "tile", "title", "Luminosidad", "caption", "Tono apagado", "link", "c?q=vitamina"),
						blk("t3", "tile", "title", "Calma", "caption", "Piel sensible", "link", "c?q=calma")),
					sec("quotes", "testimonials", map[string]any{"heading": "Reseñas reales"},
						blk("r1", "quote", "quote", "Por fin una crema que no me irrita. La uso mañana y noche.", "author", "Fernanda", "detail", "Piel sensible"),
						blk("r2", "quote", "quote", "En un mes mi piel se ve más uniforme. Me encanta la textura.", "author", "Ana Sofía", "detail", "Piel mixta")),
					sec("faq", "faq", map[string]any{"heading": "Antes de comprar"},
						blk("q1", "question", "q", "¿Son productos veganos?", "a", "Sí, todas nuestras fórmulas son veganas y no se prueban en animales."),
						blk("q2", "question", "q", "¿Puedo devolver un producto abierto?", "a", "Sí, si no te funcionó tienes 30 días para cambiarlo por otro."),
						blk("q3", "question", "q", "¿Cómo sé qué rutina me conviene?", "a", "Escríbenos y una cosmetóloga te recomienda una rutina sin costo.")),
				},
				"footer": {
					sec("news", "newsletter", map[string]any{"heading": "Consejos para tu piel, una vez al mes", "body": "Recibe guías de cuidado y acceso anticipado a lanzamientos.", "buttonLabel": "Suscribirme"}),
					footerFor("Bálsamo", "Cuidado de la piel hecho en México, con ingredientes honestos."),
				},
			},
		}
	}
	return Template{
		ID: "tpl_home", Kind: "home", Handle: "index", Name: "Inicio",
		SEO:   SEO{Title: "Lienzo · Moda atemporal", Description: "Prendas esenciales en fibras naturales, confeccionadas en México. Envío gratis desde $1,499."},
		Theme: &ThemeSettings{ID: "lienzo"},
		Regions: map[string][]SectionInstance{
			"header": {
				sec("ann", "announcement", map[string]any{"text": "Envío gratis desde $1,499 · Cambios sin costo en 30 días"}),
				sec("hdr", "header", map[string]any{"logoText": "LIENZO", "menu": "Mujer | c?q=mujer\nHombre | c?q=hombre\nEsenciales | c?q=playera\nAccesorios | c?q=gorra\nJournal | #journal", "showSearch": true}),
			},
			"main": {
				sec("hero", "hero", map[string]any{"eyebrow": "Otoño · Invierno", "heading": "Prendas que duran más que la temporada",
					"subheading": "Lino, algodón orgánico y lana merino. Cortes limpios, hechos en talleres de la Ciudad de México.",
					"ctaLabel":   "Ver colección", "ctaUrl": "c", "cta2Label": "Esenciales", "cta2Url": "c?q=playera", "layout": "full", "height": "large", "monogram": "L"}),
				sec("cats", "collection-tiles", map[string]any{"heading": "Comprar por categoría", "style": "tall"},
					blk("t1", "tile", "title", "Playeras", "caption", "Algodón orgánico", "link", "c?q=playera"),
					blk("t2", "tile", "title", "Sudaderas", "caption", "Felpa pesada", "link", "c?q=sudadera"),
					blk("t3", "tile", "title", "Tenis", "caption", "Piel y lona", "link", "c?q=tenis")),
				sec("new", "product-grid", map[string]any{"heading": "Recién llegado", "linkLabel": "Ver todo", "limit": 8.0, "columns": 4.0}),
				sec("story", "image-text", map[string]any{"eyebrow": "Hecho en México", "heading": "Conocemos a quien cose cada prenda",
					"body":     "Trabajamos con cuatro talleres familiares y pagamos precio justo. Menos piezas, mejor hechas, para usarlas por años.",
					"ctaLabel": "Nuestra historia", "ctaUrl": "#journal"}),
				sec("manifesto", "rich-text", map[string]any{"eyebrow": "Manifiesto", "heading": "Vestir bien no tiene que ser complicado",
					"body": "Diseñamos un guardarropa que combina entre sí: colores neutros, telas nobles y cortes que favorecen.", "align": "center"}),
				sec("best", "product-carousel", map[string]any{"heading": "Los más queridos", "limit": 8.0}),
				sec("bnf", "benefits", map[string]any{},
					blk("b1", "benefit", "title", "Envío gratis", "text", "Desde $1,499"),
					blk("b2", "benefit", "title", "Cambios sin costo", "text", "Durante 30 días"),
					blk("b3", "benefit", "title", "Pago seguro", "text", "Tarjeta, Apple Pay y Google Pay")),
			},
			"footer": {
				sec("news", "newsletter", map[string]any{"heading": "Únete a la lista", "body": "Recibe primero los lanzamientos y un 10% en tu primera compra.", "buttonLabel": "Suscribirme"}),
				footerFor("Lienzo", "Ropa esencial, hecha con calma en la Ciudad de México."),
			},
		},
	}
}
