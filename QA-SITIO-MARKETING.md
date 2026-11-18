# QA — Sitio de marketing Shulada

Hand-off para QA humano / staging. Rama: `claude/hola-c650ea`.

## Qué se construyó (este ciclo)

1. **Rediseño de landings del mega-menú** — cada landing con estructura y "vista" propias (no el mismo hero+features repetido). Giros, resultado, tamaño, necesidad, plataforma.
2. **Confianza y legal**: `/privacidad` (LFPDPPP), `/terminos`, `/seguridad`, `/estado`.
3. **Soporte y crecimiento**: `/contacto` (form real), `/ayuda`, `/migrar`, `/developers`, `/partners`.
4. **Capacidades con página propia**: `/suscripciones`, `/impuestos`, `/lealtad`, `/pos`, `/devoluciones`.
5. **Comercial**: `/planes` (precio creíble sin cifras), `/comparar` (vs VTEX/Shopify/Tiendanube), `/nosotros`.
6. **Home**: sección de confianza honesta + barra de confianza.

Total: 45 páginas de marketing. Ninguna lógica de backend se modificó salvo el mapa de rutas (`internal/api/marketing.go`); `/contacto` reusa el endpoint existente `POST /v1/leads`.

## Cómo correrlo en staging

Las plantillas van **incrustadas con `go:embed`** → hay que **recompilar** el binario para ver cambios.

```
go build ./...          # debe pasar sin errores
go test ./internal/api/ # debe pasar
# levantar el servidor como en staging (requiere su DB); el sitio queda en /
```

Verificado antes del hand-off: `go build ./...` OK · `go vet` OK · tests de `api/builder/help/sitecfg` OK · gofmt limpio · **todos los enlaces internos resuelven** (sin 404).

## Checklist de QA (por probar manualmente)

### Navegación y layout
- [ ] Mega-menú: Plataforma / Soluciones / Recursos abren y cierran bien (hover + click + teclado); nunca dos paneles abiertos.
- [ ] Mega-menú en móvil (hamburguesa) no desborda horizontalmente.
- [ ] Footer: 5 columnas; en móvil colapsa a 2–3 sin romperse.
- [ ] Todas las páginas se ven bien en claro y oscuro (el sitio respeta el tema del sistema).
- [ ] Responsive 360 / 768 / 1280 en: home, una landing de giro, `/comparar`, `/contacto`, una página legal.

### Formularios (críticos — tocan backend real)
- [ ] `/empezar`: envía a `/v1/leads`, muestra el estado de éxito; validación de nombre y correo; el honeypot funciona; enlace a `/privacidad` visible.
- [ ] `/contacto`: envía a `/v1/leads` (el "Motivo" se antepone al mensaje); éxito y validación OK.
- [ ] Rate limiting del lead: al enviar muchas veces, responde 429 con mensaje claro.

### Páginas nuevas (contenido y CTAs)
- [ ] `/privacidad`, `/terminos`, `/nosotros`: revisar que los **placeholders** estén marcados (ver abajo) y que el texto legal lo valide tu equipo legal.
- [ ] `/seguridad`, `/estado`: afirmaciones coinciden con la realidad del sistema (PCI vía Stripe, aislamiento, reversible, bitácora).
- [ ] `/comparar`: la comparación con competidores es veraz; disclaimer presente.
- [ ] `/developers`: las capacidades (API, webhooks, cXML) existen; no prometer endpoints inexistentes.
- [ ] `/suscripciones` `/impuestos` `/lealtad` `/pos` `/devoluciones`: cada afirmación debe corresponder a algo que el producto realmente hace.
- [ ] Todos los botones "Ver una tienda en vivo" llevan a una tienda real; "Entrar al panel" a `/admin/`.

### SEO / meta
- [ ] Cada página tiene `<title>` y `meta description` propios (vienen del mapa en `marketing.go`).

## ⚠️ Placeholders a completar ANTES de producción
Buscar en el código `[` para encontrarlos. No publicar el aviso legal sin esto:
- `[RAZÓN SOCIAL, S.A. DE C.V.]`, `[RFC]`, `[DOMICILIO FISCAL COMPLETO]`, `[CIUDAD / JURISDICCIÓN]`, `[CIUDAD, MÉXICO]` — en `/privacidad`, `/terminos`, `/nosotros`.
- `[NÚMERO DE WHATSAPP]` — en `/contacto`.
- Correos (`hola@`, `soporte@`, `seguridad@`, `privacidad@` `shulada.com`) — confirmar que existen y se monitorean.
- **Revisión legal** del Aviso de Privacidad y Términos por un abogado antes de publicar.

## No incluido (decidido, pendiente de construir)
- **Sitio en inglés** (`/en/...`, opción A elegida) — no construido aún; es la siguiente oleada grande.
- **Demo en vivo del panel** (sin login, blindada) — es feature de backend con implicaciones de seguridad; requiere diseño dedicado (tenant demo aislado, rol restringido, auto-reset, rate limits). No construido.
- **Modelo de cobro (D14)** — por definir por el negocio; el mensaje de `/planes` ya es neutral.
