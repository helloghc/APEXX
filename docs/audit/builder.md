# Auditoría del builder de sitios y diseño del Builder de Apexx

> Fecha: 2026-11-10 · Fuentes: storefront de la cuenta demo `elpaps` (VTEX IO Store Framework), leído solo con GET; documentación pública de Shopify (shopify.dev).
> No se guardó nada en Site Editor. No se incluyen credenciales, cookies ni datos personales.

## 1. Cómo construye VTEX el sitio

### 1.1 Evidencia de `elpaps`

Cada página de VTEX IO envía al navegador un objeto `__RUNTIME__` con el árbol completo de bloques ("extensions"), los componentes, sus props y el contenido. Lo extrajimos de cuatro páginas:

| Página | Bloques en el árbol | Profundidad máx. | Apps distintas en el árbol | Componentes JS | Tamaño del árbol inline | Bloques con contenido propio de Site Editor |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Home (`store.home`) | **601** | 17 | 33 | 190 | 592 KB | **6** |
| Categoría (`store.search#department`) | **781** | **20** | 35 | 224 | 794 KB | 2 |
| Búsqueda (`store.search`) | 781 | 20 | 35 | 224 | 786 KB | — |
| Producto (`store.product`) | 528 | 17 | 36 | 220 | 517 KB | 2 |

Otros datos de la misma cuenta:

- **Tema:** `elpaps.store-theme@4.5.7`, sobre `vtex.store@2.147.2` y `vtex.render-runtime@8.136.3`.
- **61 apps** (con versión) participan en la home. `vtex.css-handles` aparece en **dos versiones** a la vez (0.4.4 y 1.0.1).
- **El menú pesa más que la página:** 276 de los 601 bloques de la home son de `vtex.menu` (menús de header y footer en desktop y mobile). `vtex.flex-layout` suma otros 137 bloques solo para filas y columnas.
- **Hidratación:** 408 bloques con `hydration: always` y 188 `on-view`. Render: 520 en servidor, 74 diferidos, 7 solo cliente. Casi todo se hidrata con React aunque sea contenido estático.
- **19 rutas** (home, producto, 6 variantes de búsqueda, cuenta, wishlist, 3 páginas custom, 2 páginas de 404, etc.). Las rutas admiten layouts condicionales (`allowConditions: true`).
- **Cuatro sistemas de contenido conviven** en la misma página: `vtex.admin-pages@4.x` (Site Editor), `vtex.admin-cms@1.x`, `vtex.admin-cms-graphql-rc@1.x` y `vtex.pages-graphql@2.x`.
- **El 99% de la página vive en código.** En la home, solo 6 de 601 bloques tienen contenido guardado en Site Editor. El resto, incluidos los textos (por ejemplo `"## Compra por departamento"` en `rich-text#home-depts-title`), está escrito en los archivos `blocks.jsonc` del tema.
- **Estructura de la home** (bloques de primer nivel): `slider-layout#hero`, `rich-text#home-depts-title`, `flex-layout.row#home-depts`, `rich-text#home-new-title`, `list-context.product-list#home-new`, `flex-layout.row#home-promos`, `rich-text#home-moda-title`, `list-context.product-list#home-moda`, `flex-layout.row#home-news` y `back-to-top-button`. Son 10 secciones visibles que se expanden a 601 nodos.

### 1.2 Qué cuesta mover un módulo en VTEX

Caso: subir el bloque de promociones (`flex-layout.row#home-promos`) por encima de "Novedades".

Site Editor permite editar props y contenido de los bloques que declaran un esquema, y ocultar algunos. **Agregar, quitar o reordenar bloques es un cambio de código en el tema.**

| Paso | Herramienta | Quién | Tiempo típico |
| --- | --- | --- | --- |
| 1. Clonar el repo del tema y tener el entorno (Node, VTEX Toolbelt, login) | Git + CLI `vtex` | Desarrollador | 15–60 min la primera vez |
| 2. Editar `store/blocks/home/*.jsonc` y mover la referencia del bloque | Editor de código | Desarrollador | 5 min |
| 3. `vtex use <workspace>` + `vtex link` para ver el cambio | CLI | Desarrollador | 2–10 min (build del tema) |
| 4. Revisión de QA en el workspace | Navegador | QA / negocio | 10–60 min |
| 5. Subir versión (`vtex release` / bump en `manifest.json`), `vtex publish` | CLI | Desarrollador | 5–15 min |
| 6. `vtex install` en un workspace de producción, prueba A/B o `vtex promote` | CLI | Desarrollador | 5–20 min más la propagación de caché |
| 7. Si algo falla: volver a instalar la versión anterior | CLI | Desarrollador | 5–20 min |

**Total: entre 1 y 3 horas de un desarrollador con acceso a la CLI** para mover un bloque, con riesgo real. Un error de sintaxis en `blocks.jsonc` o un bloque mal referenciado rompe la página entera. Además, el contenido que el negocio edita en Site Editor puede quedar huérfano si el desarrollador renombra el bloque (`#home-promos` → `#home-promos-2`), porque el contenido se asocia al ID del bloque.

### 1.3 Dolores estructurales observados

1. **Código y contenido mezclados.** Los textos viven en el tema, así que cambiar un título también puede requerir un deploy.
2. **El árbol se envía entero al navegador.** De 517 a 794 KB de definiciones por página, aunque el usuario nunca las vea (ver `frontend.md`).
3. **Granularidad excesiva.** Para una fila de 3 tarjetas se anidan `flex-layout.row` → `flex-layout.col` → `rich-text`/`image`, con hasta 20 niveles. El editor muestra esos nodos técnicos, no conceptos de negocio.
4. **Sin presupuestos.** Nada impide agregar un carrusel con 40 productos que se hidratan al cargar.
5. **Varios CMS a la vez** y migraciones entre ellos.

## 2. Cómo lo hace Shopify (Online Store 2.0)

**Modelo.** Un tema tiene `layout/`, `templates/` (JSON o Liquid), `sections/`, `blocks/`, `snippets/`, `config/settings_schema.json` y `settings_data.json`.

- **Templates JSON:** listan secciones y su orden, con los settings de cada instancia.
- **Secciones:** archivos Liquid con un `{% schema %}` JSON que declara `settings`, `blocks`, `max_blocks`, `presets`, `enabled_on` y `disabled_on`.
- **Bloques:** pueden ser de sección (solo viven en su sección) o de tema (reutilizables, en `/blocks`).
- **App blocks:** bloques de tipo `@app` que inyectan funcionalidad de una app sin tocar el código del tema.
- **Theme editor:** permite agregar, quitar y reordenar secciones y bloques sin código, con vista previa.

**Límites documentados** (shopify.dev):

- "JSON templates can render up to **25 sections**, and each section can have up to **50 blocks**." ([JSON templates](https://shopify.dev/docs/storefronts/themes/architecture/templates/json-templates))
- "A theme can contain up to **1,000 JSON templates**." (misma página)
- "A theme can contain at most **300 theme blocks**", y cuentan incluso los no usados. ([Blocks](https://shopify.dev/docs/storefronts/themes/architecture/blocks))
- Los bloques de sección "only work in the section they're defined within and can't be used elsewhere." (misma página)
- "There's a limit of 50 blocks per section." ([Section schema](https://shopify.dev/docs/storefronts/themes/architecture/sections/section-schema))
- "Statically rendered sections can't be removed or reordered by merchants." ([Sections](https://shopify.dev/docs/storefronts/themes/architecture/sections))
- "You can't share section data across JSON theme templates." (JSON templates)

**Dolores de Shopify:**

1. **Crear una sección nueva requiere un desarrollador que sepa Liquid**, un lenguaje propietario. El editor solo combina lo que el tema ya trae.
2. **Lock-in con Liquid.** Pasar a headless (Hydrogen + React) obliga a reescribir el frontend completo y perder el theme editor.
3. **Actualizar el tema puede pisar personalizaciones.** Los settings se conservan, pero los cambios hechos al código del tema se tienen que portar a mano a la nueva versión.
4. **No se reutiliza contenido entre plantillas.** Un banner compartido entre la home y una colección se configura dos veces.
5. **El rendimiento depende de cada tema y app.** Las apps inyectan scripts y nada limita lo que una sección agrega.
6. **Publicación todo o nada del tema completo.** No hay programación nativa por sección ni A/B integrado en el editor.

## 3. Comparativa de dolores

| Necesidad del comerciante | VTEX IO | Shopify OS 2.0 | **Apexx (objetivo)** |
| --- | --- | --- | --- |
| Reordenar una sección | Código + CLI + deploy (1–3 h) | Arrastrar en el editor (minutos) | Arrastrar, ver en vivo y publicar en **< 2 s** |
| Crear un tipo de sección nuevo | Desarrollador IO (React + `interfaces.json` + `contentSchemas.json`) | Desarrollador Liquid | Desarrollador con componentes estándar + esquema, o **IA** a partir de una descripción |
| Separar código y contenido | No: textos en `blocks.jsonc` | Parcial | **Sí**: el tema nunca contiene contenido del comerciante |
| Volver atrás | Reinstalar versión (5–20 min) | Historial de versiones del tema, manual | **Rollback en 1 clic**, < 2 s |
| Programar un cambio | Limitado | Requiere apps | **Nativo** por sección o página |
| A/B testing | Workspaces A/B a nivel de app | Requiere apps | **Nativo** por sección |
| Límites de estructura | Ninguno: árboles de 781 nodos | 25 secciones / 50 bloques | Sin límites artificiales, con **presupuesto de rendimiento** |
| Rendimiento garantizado | No (LCP 13,7 s medido en categoría) | Depende del tema | **El editor bloquea lo que rompa el presupuesto** |
| Actualizar el tema sin perder cambios | Riesgo de contenido huérfano | Riesgo de perder código | Migraciones de esquema versionadas y automáticas |
| Contenido compartido entre páginas | No directo | No | **Secciones globales** reutilizables con una sola fuente |

## 4. Diseño del Builder de Apexx

### 4.1 Principios

1. **El comerciante piensa en secciones de negocio** ("Banner principal", "Carrusel de productos", "Preguntas frecuentes"), nunca en filas, columnas ni nodos técnicos.
2. **Código y contenido separados.** El tema define tipos de sección. El sitio guarda instancias con su contenido. Actualizar el tema nunca borra contenido.
3. **Todo es una versión inmutable.** Cada publicación es un snapshot con checksum. Publicar es cambiar un puntero; volver atrás también.
4. **El rendimiento es una regla del editor.** Cada sección declara su costo y el editor suma el total de la página antes de publicar.
5. **Sin lenguaje propietario.** Las secciones son componentes estándar (HTML con plantillas de Go para el render en servidor, y Web Components o Preact para las islas interactivas) más un JSON Schema.

### 4.2 Modelo de datos

```
Site (tienda)
 ├─ Theme (tokens de diseño + biblioteca de SectionDefinitions versionada)
 ├─ Template (home, product, collection, search, cart, page, custom…)
 │   ├─ regions: header / main / footer
 │   └─ SectionInstance[] (orden = posición en el array)
 │        └─ Block[] (hijos tipados, opcionales)
 ├─ GlobalSection (instancias compartidas: header, footer, banners reutilizables)
 ├─ Revision (snapshot inmutable de todo lo anterior)
 ├─ Release (qué Revision está publicada en qué canal y desde cuándo)
 └─ Experiment (A/B por sección o por template)
```

**Entidades y campos** (nombres para el código Go):

| Entidad | Campos |
| --- | --- |
| `Site` | `id`, `tenantId`, `name`, `domains[]`, `defaultLocale`, `locales[]`, `themeId`, `publishedRevisionId`, `draftRevisionId` |
| `Theme` | `id`, `name`, `version` (semver), `tokens` (`colors`, `typography`, `spacing`, `radius`, `breakpoints`), `sectionTypes[]` (referencias a `SectionDefinition@version`) |
| `SectionDefinition` | `type`, `version`, `name`, `category`, `description`, `icon`, `settingsSchema` (JSON Schema 2020-12), `blocks` (`allowed[]`, `min`, `max`), `dataSources[]` (`product`, `collection`, `search`, `cart`, `content`), `enabledOn[]`, `render` (`template`, `island`), `hydration` (`none`, `visible`, `idle`, `interaction`), `budget` (`htmlBytes`, `jsBytes`, `cssBytes`, `requests`, `aboveFoldImages`), `migrations[]`, `presets[]` |
| `Template` | `id`, `siteId`, `kind`, `handle`, `name`, `conditions` (por ejemplo `collection.handle == "verano"`), `regions` (`header`, `main`, `footer`), `seo` (`title`, `description`, `og`), `updatedAt` |
| `SectionInstance` | `id` (ULID estable), `type`, `typeVersion`, `settings` (validado contra `settingsSchema`), `blocks[]`, `globalRef` (si es compartida), `visibility` (`devices[]`, `schedule` {`from`, `to`, `timezone`}, `audiences[]`, `locales[]`), `experimentId`, `locked` |
| `Block` | `id`, `type`, `settings`, `visibility` |
| `Revision` | `id`, `siteId`, `parentId`, `createdBy`, `createdAt`, `message`, `checksum` (SHA-256 del snapshot), `snapshot` (templates + globals + tokens), `status` (`draft`, `in_review`, `approved`, `scheduled`, `published`, `superseded`), `budgetReport` |
| `Release` | `id`, `siteId`, `revisionId`, `channel` (`production`, `preview`), `publishedAt`, `publishedBy`, `scheduledFor`, `durationMs`, `rolledBackFrom` |
| `Experiment` | `id`, `siteId`, `target` (`sectionId` o `templateId`), `variants[]` (`key`, `weight`, `patch`), `metric` (`conversion`, `addToCart`, `revenuePerVisit`), `status`, `startedAt`, `endedAt` |

### 4.3 Flujo de publicación: atómico, en menos de 2 segundos, con rollback en 1 clic

1. **Editar:** cada cambio del editor es un *patch* JSON (RFC 6902) sobre el borrador, guardado con autosave y deshacer/rehacer ilimitado.
2. **Validar** al guardar y antes de publicar:
   - `settings` contra JSON Schema;
   - bloques permitidos y límites;
   - accesibilidad (alt de imágenes, contraste con los tokens, jerarquía de encabezados);
   - SEO (título, descripción, un solo h1);
   - presupuesto de rendimiento.
3. **Congelar:** publicar crea una `Revision` inmutable con checksum. Si el flujo de aprobaciones está activo, la revisión pasa a `in_review`.
4. **Renderizar:** un worker en Go renderiza a HTML estático cada página afectada (las plantillas de producto y colección se renderizan como *shell* + datos en el edge). Las páginas no afectadas se reutilizan por checksum.
5. **Activar:** se sube el HTML al almacenamiento del edge con la clave `revisionId` y se cambia un solo puntero `site → publishedRevisionId`. El CDN se purga por *surrogate key* del sitio. **Objetivo: < 2 s de punta a punta** para una página.
6. **Rollback:** el puntero vuelve a la `Revision` anterior. El HTML ya existe, así que no se re-renderiza nada (**< 1 s**).
7. **Programar:** `Release.scheduledFor` publica a una hora exacta (por ejemplo, Black Friday a las 00:00 en la zona de la tienda), con aviso si la revisión base cambió.

### 4.4 Cómo un no experto mueve un módulo

1. Abre el editor. A la izquierda ve la lista de secciones de la página con nombres de negocio. A la derecha ve **la tienda real** en un iframe con los productos reales.
2. Arrastra "Promociones" encima de "Novedades", en la lista o directamente sobre la vista previa. El cambio se ve en menos de 100 ms porque solo se re-renderiza esa región.
3. La barra superior muestra el semáforo de la página: peso, LCP estimado, accesibilidad y SEO.
4. Pulsa **Publicar**. En menos de 2 s ve "Publicado" con un enlace al historial y el botón **Deshacer publicación**.

**Total: menos de 30 segundos, sin desarrollador y sin riesgo**, porque cualquier publicación se revierte en 1 clic.

Otras capacidades del editor:

- **Vista por dispositivo** (móvil, tablet, desktop) y por audiencia o idioma.
- **Duplicar una sección** a otra plantilla o convertirla en **sección global** (una sola fuente, editada en un lugar).
- **Comentarios y aprobaciones** sobre una revisión (roles: editor, aprobador, publicador, desarrollador).
- **Comparar revisiones** (diff visual y diff JSON) antes de publicar.

### 4.5 Catálogo inicial de secciones (v1)

| Categoría | Secciones |
| --- | --- |
| Encabezado y navegación | Barra de anuncios, Header (logo, menú, búsqueda, carrito), Mega menú, Breadcrumbs |
| Héroes y banners | Hero (imagen o video), Carrusel de banners, Banner dividido, Cuenta regresiva |
| Productos | Carrusel de productos (colección, búsqueda, recomendados, vistos recientemente), Grilla de productos, Producto destacado, Comprar el look, Comparador |
| Colecciones | Grilla de categorías, Lista de marcas, Colección destacada |
| Ficha de producto | Galería, Info y compra, Selector de variantes, Disponibilidad y envío, Especificaciones, Reseñas, Productos relacionados |
| Contenido | Texto enriquecido, Imagen con texto, Preguntas frecuentes, Testimonios, Video, Logos de marcas, Íconos de beneficios |
| Conversión | Newsletter, Formulario, Pop-up con reglas, Barra de envío gratis |
| Pie de página | Footer, Medios de pago, Redes sociales, Selector de país e idioma |
| Apps | Ranuras para extensiones (equivalente a los app blocks) con presupuesto propio |

### 4.6 Cómo un desarrollador crea una sección

Estructura de una sección en el repositorio del tema:

```
sections/hero/
  section.json      # definición: esquema, presupuesto, hidratación, migraciones
  hero.html.tmpl    # plantilla de Go (render en servidor, sin JS)
  hero.island.ts    # opcional: interactividad como isla (Web Component / Preact)
  hero.css          # CSS con alcance a la sección, usando los tokens del tema
  preview.png
```

- `apexx dev`: servidor local con hot reload y datos de una tienda de prueba.
- `apexx check`: valida el esquema, mide el presupuesto real (HTML, JS, CSS) y corre pruebas de accesibilidad.
- `apexx push`: publica una nueva versión de la sección. Los sitios que la usan reciben la actualización como **borrador**; las migraciones declaradas transforman los `settings` antiguos automáticamente.

**IA:** a partir de una descripción ("una sección con 3 beneficios con ícono y texto, fondo de color de marca") se generan el esquema, la plantilla y el CSS. El resultado pasa por las mismas validaciones (`apexx check`) antes de estar disponible. El comerciante también puede pedir **una página completa** ("landing de Día de las Madres con 2 colecciones y cuenta regresiva"): la IA arma la plantilla solo con secciones existentes y queda como borrador.

### 4.7 Renderizado y presupuestos de rendimiento

- **HTML estático por página publicada**, generado en Go y servido desde el edge. Los precios y el stock, que cambian, se inyectan en el edge o con una isla pequeña. Nunca se envía el árbol de la página al navegador.
- **Islas:** solo las secciones con `hydration != none` cargan JS, y solo cuando hace falta (`visible`, `idle`, `interaction`).
- **CSS por sección** concatenado y minificado por página: 1 archivo crítico inline + 1 diferido, no 41 a 50 como en VTEX.
- **Imágenes:** `srcset`, AVIF/WebP, `width`/`height` siempre (CLS 0) y `fetchpriority=high` solo para la imagen LCP, que el renderizador detecta como la primera imagen *above the fold*.

**Presupuesto por plantilla** (el editor suma los `budget` de sus secciones):

| Métrica | Producto | Categoría/búsqueda | Home | Comportamiento del editor |
| --- | ---: | ---: | ---: | --- |
| HTML (comprimido) | 30 KB | 40 KB | 40 KB | Aviso al 80%, bloqueo al 100% |
| JS total | 100 KB | 120 KB | 80 KB | Bloqueo |
| CSS total | 30 KB | 30 KB | 30 KB | Aviso |
| Peticiones | 30 | 40 | 35 | Aviso |
| Imágenes above the fold | 2 | 4 | 2 | Aviso |
| LCP estimado (móvil 4G) | 1,5 s | 1,8 s | 1,8 s | Bloqueo, con opción de override por un rol "desarrollador" que queda auditado |

**Accesibilidad y SEO automáticos:** el `alt` es obligatorio en el esquema de imagen (con sugerencia por IA). El contraste se valida contra los tokens. Se garantiza un `h1` por página. `title`, `description`, `canonical`, `hreflang` y JSON-LD (`Product`, `BreadcrumbList`, `Organization`) se generan del modelo de datos.

### 4.8 Permisos y aprobaciones

| Rol | Puede |
| --- | --- |
| Editor | Editar borradores y contenido |
| Aprobador | Aprobar o rechazar revisiones |
| Publicador | Publicar, programar y revertir |
| Desarrollador | Subir versiones de secciones y temas; hacer override de presupuesto (auditado) |
| Dueño | Todo, incluidos roles y dominios |

Cada acción queda en un registro de auditoría con el `revisionId`.

## 5. Formato JSON listo para implementar

### 5.1 Template: home

```json
{
  "id": "tpl_01JC8Z3K9QH4N2V7XW5T6Y0RMA",
  "siteId": "site_elpaps",
  "kind": "home",
  "handle": "index",
  "name": "Página de inicio",
  "conditions": null,
  "seo": {
    "title": "Tienda elpaps · Moda, deportes y hogar",
    "description": "Compra moda, calzado deportivo y artículos para el hogar con envío a todo México.",
    "og": { "image": "media_01JC8Z5HERO" }
  },
  "regions": {
    "header": [
      { "id": "sec_announce", "globalRef": "glb_announcement_bar" },
      { "id": "sec_header", "globalRef": "glb_header" }
    ],
    "main": [
      {
        "id": "sec_01JC8Z4HERO",
        "type": "hero",
        "typeVersion": "1.2.0",
        "settings": {
          "heading": "Nueva temporada",
          "subheading": "Hasta 30% en calzado deportivo",
          "media": { "image": "media_01JC8Z5HERO", "alt": "Corredora con tenis blancos en una pista" },
          "cta": { "label": "Ver colección", "link": { "kind": "collection", "handle": "running" } },
          "height": "medium",
          "textAlign": "left",
          "overlay": 0.25
        },
        "blocks": [],
        "visibility": {
          "devices": ["mobile", "tablet", "desktop"],
          "schedule": { "from": "2026-11-13T00:00:00", "to": "2026-11-20T23:59:59", "timezone": "America/Mexico_City" },
          "audiences": [],
          "locales": []
        },
        "experimentId": null,
        "locked": false
      },
      {
        "id": "sec_01JC8Z4DEPTS",
        "type": "collection-grid",
        "typeVersion": "1.0.0",
        "settings": { "heading": "Compra por departamento", "columns": { "mobile": 2, "desktop": 4 } },
        "blocks": [
          { "id": "blk_sports", "type": "collection-card", "settings": { "collection": "sports" } },
          { "id": "blk_home", "type": "collection-card", "settings": { "collection": "home" } },
          { "id": "blk_fashion", "type": "collection-card", "settings": { "collection": "fashion" } },
          { "id": "blk_toys", "type": "collection-card", "settings": { "collection": "toys" } }
        ]
      },
      {
        "id": "sec_01JC8Z4PROMO",
        "type": "promo-banners",
        "typeVersion": "1.0.0",
        "settings": { "layout": "two-up" },
        "blocks": [
          { "id": "blk_p1", "type": "banner", "settings": { "image": "media_promo1", "alt": "Vestidos de verano", "link": { "kind": "collection", "handle": "vestidos" } } },
          { "id": "blk_p2", "type": "banner", "settings": { "image": "media_promo2", "alt": "Tenis Converse", "link": { "kind": "brand", "handle": "converse" } } }
        ],
        "experimentId": "exp_01JC8Z6PROMO"
      },
      {
        "id": "sec_01JC8Z4NEW",
        "type": "product-carousel",
        "typeVersion": "2.1.0",
        "settings": {
          "heading": "Novedades",
          "source": { "kind": "collection", "handle": "novedades", "sort": "release-desc" },
          "limit": 12,
          "showPrice": true,
          "showRating": true
        },
        "blocks": []
      }
    ],
    "footer": [
      { "id": "sec_newsletter", "globalRef": "glb_newsletter" },
      { "id": "sec_footer", "globalRef": "glb_footer" }
    ]
  },
  "updatedAt": "2026-11-10T19:40:00Z"
}
```

### 5.2 SectionDefinition: hero

```json
{
  "type": "hero",
  "version": "1.2.0",
  "name": "Banner principal",
  "category": "heroes",
  "description": "Imagen o video de ancho completo con título, texto y botón.",
  "icon": "image",
  "enabledOn": ["home", "collection", "page", "custom"],
  "render": { "template": "hero.html.tmpl", "island": null },
  "hydration": "none",
  "dataSources": [],
  "blocks": { "allowed": [], "min": 0, "max": 0 },
  "budget": { "htmlBytes": 3000, "jsBytes": 0, "cssBytes": 2500, "requests": 1, "aboveFoldImages": 1 },
  "settingsSchema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "required": ["heading", "media"],
    "additionalProperties": false,
    "properties": {
      "heading":    { "type": "string", "title": "Título", "maxLength": 80, "x-apexx-widget": "text", "x-apexx-heading-level": "auto" },
      "subheading": { "type": "string", "title": "Texto", "maxLength": 160, "x-apexx-widget": "textarea" },
      "media": {
        "type": "object",
        "title": "Imagen o video",
        "required": ["image", "alt"],
        "properties": {
          "image": { "type": "string", "title": "Imagen", "x-apexx-widget": "media", "x-apexx-media": { "minWidth": 1600, "aspectRatios": ["16:9", "21:9", "4:5"] } },
          "video": { "type": "string", "title": "Video (opcional)", "x-apexx-widget": "media-video" },
          "alt":   { "type": "string", "title": "Texto alternativo", "minLength": 5, "maxLength": 140, "x-apexx-ai-suggest": true }
        }
      },
      "cta": {
        "type": "object",
        "title": "Botón",
        "properties": {
          "label": { "type": "string", "maxLength": 30 },
          "link":  { "type": "object", "x-apexx-widget": "link-picker" }
        }
      },
      "height":    { "type": "string", "enum": ["small", "medium", "large", "full"], "default": "medium", "title": "Altura" },
      "textAlign": { "type": "string", "enum": ["left", "center", "right"], "default": "left", "title": "Alineación" },
      "overlay":   { "type": "number", "minimum": 0, "maximum": 0.8, "default": 0.2, "title": "Oscurecer imagen", "x-apexx-widget": "slider" }
    }
  },
  "presets": [
    { "name": "Banner de temporada", "settings": { "heading": "Nueva temporada", "height": "large", "textAlign": "center" } }
  ],
  "migrations": [
    { "from": "1.1.x", "to": "1.2.0", "ops": [ { "op": "move", "from": "/image", "path": "/media/image" }, { "op": "move", "from": "/imageAlt", "path": "/media/alt" } ] }
  ]
}
```

Las extensiones `x-apexx-*` le indican al editor qué control mostrar (selector de medios, selector de enlaces, slider) sin salir de JSON Schema estándar. Cualquier validador de JSON Schema las ignora.

## 6. Resumen de metas del Builder

| Meta | Valor |
| --- | --- |
| Mover una sección y publicar (no experto) | < 30 s en total, publicación < 2 s |
| Rollback | 1 clic, < 1 s |
| Vista previa tras un cambio | < 100 ms |
| HTML de una página publicada | 30–40 KB comprimido (VTEX: 115–238 KB comprimido con 517–794 KB de árbol) |
| Bloques visibles en el editor | Secciones de negocio (≈10 en la home), no 601 nodos técnicos |
| Contenido en código | 0% (VTEX elpaps: 99%) |
