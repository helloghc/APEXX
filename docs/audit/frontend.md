# Auditoría de frontend: tienda VTEX "elpaps"

Fecha: 2026-11-10 · Alcance: storefront para el comprador (VTEX IO Store Framework y Checkout v6).
Todo fue de solo lectura: no se crearon pedidos ni se cambió configuración.

> **Contexto importante.** `elpaps` es una tienda demo de desarrollo protegida con contraseña. Para entrar se usó una cookie de autenticación de administrador generada con la app key de la cuenta. Con esa cookie VTEX sirve el HTML con `cache-control: no-store`. Una tienda pública podría cachear algo más en el CDN, pero el peso de los archivos (HTML, JS, CSS, imágenes) y el número de peticiones son los mismos. Los scripts de terceros (Google, Facebook, telemetría de VTEX) quedaron **bloqueados** por la red de la auditoría y no se descargaron. En producción los números serían **peores** que los de esta tabla.

## 1. Método

| Parámetro | Valor |
|---|---|
| Navegador | Chromium headless (Playwright), caché desactivada (visita en frío) |
| Escritorio | 1366×900, sin limitar red ni CPU |
| Móvil | Perfil "Moto G4" de Playwright, red Slow 4G emulada (150 ms RTT, 1,6 Mbps de bajada, 750 kbps de subida), CPU 4× más lenta |
| Corridas | 3 por página y modo. Se reporta la **mediana** |
| Métricas | TTFB y DCL/load (Navigation Timing). FCP, LCP, CLS y long tasks (PerformanceObserver). TBT = suma de (long task − 50 ms). Bytes = `encodedDataLength` de CDP, es decir, transferidos por la red |
| Páginas | Home `/`, categoría `/sports`, producto `/tenis-converse-…/p`, búsqueda `/shoe?_q=shoe&map=ft`, carrito `/checkout/#/cart` |
| Análisis estático | REA 6.8.0 (`rea analyze-web-bundle` vía CDP) sobre la página de producto: 41 scripts y 3,48 MB de código fuente sin comprimir |

## 2. Resultados (medianas)

### Móvil (Slow 4G, CPU 4×)

| Página | TTFB | FCP | **LCP** | CLS | **TBT** | Load | Peticiones | Transferido | JS | HTML (descomprimido) | Nodos DOM |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Home | 448 ms | 3,2 s | **3,5 s** | **0,40** | 844 ms | 8,0 s | 88 | 1,01 MB | 556 KB (33 archivos) | 1,19 MB | 911 |
| Categoría | 1.265 ms | 6,7 s | **13,7 s** | 0,13 | **6.364 ms** | 19,2 s | 246 | 1,47 MB | 881 KB (40) | 2,86 MB | 3.326 |
| Producto | 876 ms | 3,5 s | **3,6 s** | 0,00 | 980 ms | 10,3 s | 133 | 1,30 MB | 861 KB (40) | 0,95 MB | 942 |
| Búsqueda | 986 ms | 3,9 s | **7,9 s** | 0,14 | **3.283 ms** | 13,5 s | 190 | 1,38 MB | 881 KB (40) | 1,85 MB | 2.083 |

### Escritorio (sin limitar)

| Página | TTFB | FCP | LCP | CLS | TBT | Load | Peticiones | Transferido | JS | CSS | Imágenes | XHR/API |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Home | 735 ms | 2,8 s | 2,8 s | **0,24** | 67 ms | 4,3 s | 90 | 1,05 MB | 556 KB | 71 KB (25 archivos) | 273 KB | 10 KB (10) |
| Categoría | 1.840 ms | 6,4 s | **6,4 s** | 0,08 | 1.222 ms | 6,9 s | 266 | 1,62 MB | 882 KB | 105 KB (46) | 301 KB | 92 KB (105) |
| Producto | 589 ms | 2,6 s | 2,6 s | 0,04 | 173 ms | 3,2 s | 146 | 1,40 MB | 861 KB | 107 KB (50) | 222 KB | 90 KB (33) |
| Búsqueda | 2.055 ms | 4,2 s | 4,7 s | 0,10 | 525 ms | 4,7 s | 215 | 1,66 MB | 881 KB | 105 KB (46) | 415 KB | 77 KB (88) |

Umbrales "buenos" de Core Web Vitals: LCP ≤ 2,5 s, CLS ≤ 0,1, INP ≤ 200 ms (el TBT es el indicador de laboratorio más cercano). **Ninguna página pasa en móvil.**

### TTFB del HTML desde el servidor (curl con gzip, 8 muestras)

| Página | p50 | mín | máx | HTML gzip |
|---|---|---|---|---|
| Home | 336 ms | 238 | 904 | 137 KB |
| Categoría | 866 ms | 664 | 2.119 | 238 KB |
| Producto | 466 ms | 190 | 597 | 115 KB |
| Búsqueda | 1.116 ms | 512 | 2.023 | 179 KB |

Las cabeceras muestran `x-vtex-backend-elapsed-time` ≈ 0,40 s en la página de producto: casi todo el TTFB es renderizado del lado del servidor (SSR) y no viaje por la red. La categoría y la búsqueda tardan 2 a 3 veces más.

### Carrito / Checkout

Checkout v6 se carga desde `io2.vtex.com` y `vtex.vtexassets.com`, que estaban bloqueados en la auditoría. Por eso **sus números de rendimiento no son válidos** (LCP 0,6 a 1,4 s solo con un shell vacío). Lo que sí se pudo ver es su stack: **jQuery 1.8.3 (2012), Bootstrap 2.3.2 (2013), checkout-ui v6.155.0** y un `checkout6-custom.js` de 98 KB. Es otra aplicación, distinta del storefront IO, y otra base tecnológica.

## 3. Cómo habla el frontend con el backend

Toda la tienda usa **GraphQL** sobre `/_v/segment/graphql/v1` y `/_v/private/graphql/v1`, la mayoría por GET. REA encontró en los bundles **49 operaciones**. Algunas: `productSearchV3`, `Products`, `Product`, `orderForm`, `addToCart`, `updateItems`, `estimateShipping`, `selectDeliveryOption`, `selectPickupOption`, `itemsWithSimulation`, `Autocomplete`, `ProductBenefits`, `ProductCategoryTree`, `pickupPointAvailability`, `availabilitySubscribe`, `AddToList`/`RemoveFromList` (wishlist), `ReviewsByProductId`, `AverageRatingByProductId`, `TotalReviewsByProductId`, `Profile`, `pwaData`, `getSettings`, `AppSettings`, `installedApp`, `RouteData`, `NavigationPage`, `RoutePreviews` y también `IntrospectionQuery`.

Llamadas observadas en la carga (escritorio, primera corrida):

| Página | Operación | Veces | Bytes | Latencia p50 |
|---|---|---|---|---|
| Categoría | `TotalReviewsByProductId` | **45** | 38,7 KB | **2.722 ms** |
| Categoría | `AverageRatingByProductId` | **45** | 38,7 KB | **2.732 ms** |
| Búsqueda | `TotalReviewsByProductId` + `AverageRatingByProductId` | **72** | 33,5 KB | 1,7–1,8 s |
| Producto | `Products` (relacionados) | 1 | 59,8 KB | 1.227 ms |
| Producto | `orderForm` | 2 | 3,6 KB | 791 ms |
| Todas | `pwaData` | 4 | 3,8 KB | 330–420 ms |
| Todas | `/api/sessions` | 1–2 | 1,9–2,8 KB | 230–540 ms |
| Todas | `orderForm`, `Profile`, `installedApp`, `getSettings`, `AppSettings` | 1–2 c/u | ~1 KB c/u | 0,4–2 s |

## 4. Anatomía de la página (análisis de bundles)

- **Estado inline `__RUNTIME__`**: 814 KB en producto, 1,0 MB en home, 1,5 MB en búsqueda y **2,29 MB en categoría**. En producto se reparte en `extensions` 529 KB (528 definiciones de bloques), `cacheHints` 118 KB, `components` 89 KB (220 componentes), `messages` 32 KB (423 textos traducidos) e `introspectionResult` 4 KB. Es el 85% del HTML.
- **74 apps IO** cargadas en la página de producto, cada una con su versión, por ejemplo `vtex.store@2.147.2`, `vtex.render-runtime@8.136.3`, `vtex.store-components@3.179.1`, `vtex.styleguide@9.146.16`, `vtex.minicart@2.69.0`, `vtex.login@2.70.0` y `vtex.reviews-and-ratings@2.13.0`.
- **React 16.9.0** (agosto de 2019), `react-intl` 3.9.1, Apollo, `ramda` 0.26.1, `regenerator-runtime` 0.11.1 (polyfill de ES2017), `lazysizes` 5.3.2, `fg-loadcss` 2.1.0, `animate.css` 3.7.0. Hay dos versiones de `vtex.css-handles` (0.4.4 y 1.0.1), así que se duplica código.
- **40 JS y 41 a 50 CSS** por página. Los bundles se agrupan por app (`files=vtex.x@v,common,Comp&files=…`), lo que fragmenta en decenas de peticiones. Los JS más grandes son `vtex.store` 95 KB, `render-runtime/common` 87 KB, `store-drawer + modal-layout` 70 KB, `minicart + delivery-promise` 53 KB y `graphql.min.js` 39 KB. En total son **3,48 MB de JS sin comprimir** que el navegador analiza en cada visita.
- **Long tasks en móvil**: 9 a 11 en home y producto, 19 a 22 en categoría y búsqueda.
- **Etiquetas instaladas sin configurar**: Google Tag Manager con `gtmId = ""`, que escribe un error en la consola, y Facebook Pixel. A eso se suman tres beacons de VTEX (`activity-flow`, `sp.vtex.com`, `rc.vtex.com`).

## 5. Caché y CDN

| Recurso | `cache-control` observado |
|---|---|
| HTML | `no-store, no-cache` (con sesión de admin). Además `HEAD` responde **405** |
| JS / CSS versionados | `public, max-age=31536000`. Algunos sin `immutable` y 3 a 4 JS sin cabecera |
| Imágenes | `max-age=3600` (home), `345600` (4 días) o `31536000`. Política inconsistente |
| XHR / GraphQL | La mayoría sin `cache-control` o con `no-cache`. Las reseñas por producto no se cachean |
| Checkout | HTML `s-maxage=60`. JS de checkout sin cabecera de caché |

## 6. SEO y accesibilidad (vistazo rápido)

| Página | `<title>` | meta description | canonical | robots | h1 | JSON-LD |
|---|---|---|---|---|---|---|
| Home | **vacío** | **falta** | sí | index,follow | 1 | WebSite, ItemList ×2 |
| Categoría | **"Sports - undefined"** | sí | sí | index,follow | 1 | BreadcrumbList, ItemList (90 KB) |
| Producto | correcto | sí | sí | index,follow | **2** | Product, BreadcrumbList, ItemList |
| Búsqueda | **"shoe - undefined"** | **falta** | sí | noindex,follow | 1 | BreadcrumbList, ItemList |
| Carrito | vacío | no | no | – | 3 | – |

- `robots.txt` contiene `Disallow: /`, lo que es normal en una tienda de desarrollo. El sitemap existe y está separado por marca, categoría y producto.
- Hay un binding `en-IN` en el tenant, pero **no hay `hreflang`**.
- Todas las imágenes tienen `alt`. Pero **ni la home ni la categoría usan `srcset`**, y **40 de 93 imágenes de la categoría** se descargan a más del doble del ancho en que se muestran. Las imágenes del catálogo se sirven en WebP, lo cual está bien.
- El JSON-LD `ItemList` de la categoría pesa 90 KB inline.

## 7. Hallazgos ordenados por impacto, y la decisión de Apexx

| # | Hallazgo VTEX (medido) | Impacto | Decisión de diseño de Apexx |
|---|---|---|---|
| 1 | Categoría con **LCP de 13,7 s y TBT de 6,4 s** en móvil, 246 peticiones y un HTML de 2,86 MB. Búsqueda con LCP de 7,9 s | Conversión en listados, que es donde más tráfico hay | PLP y búsqueda renderizadas en el servidor, con HTML en streaming y cacheadas en el edge por URL y facetas. **Presupuesto PLP: LCP < 1,8 s en móvil, TBT < 150 ms, ≤ 40 peticiones** |
| 2 | **N+1 de reseñas**: 90 llamadas GraphQL (45 + 45) en una categoría, cada una de unos 2,7 s | Satura red y CPU, y bloquea el hilo principal | Los datos de la tarjeta de producto (precio, rating y conteo) llegan en el **mismo payload** de la búsqueda, precomputados en el índice. Cero llamadas por tarjeta |
| 3 | Estado `__RUNTIME__` de **0,8 a 2,3 MB** dentro del HTML (definiciones de 528 bloques, `cacheHints` y 423 mensajes) | Descarga, parseo y memoria en cada página, incluso con caché | El layout se compila en build y no viaja al cliente. Solo se hidratan las islas interactivas, con sus props mínimas. **Presupuesto: HTML < 50 KB gzip y estado inline < 10 KB** |
| 4 | **861 a 881 KB de JS transferido** (3,48 MB sin comprimir) en 40 archivos, con React 16.9 de 2019, polyfills de ES2017 y librerías duplicadas | TBT alto, mala interactividad (INP) en gama media y baja | Arquitectura de islas (Astro, con Preact o Solid en las islas). JS solo en galería, botón de compra y minicart. **Presupuesto: JS de producto < 100 KB gzip, home < 70 KB** y nada de JS para mostrar contenido |
| 5 | **41 a 50 hojas CSS** por página, una por app | Muchas peticiones que bloquean el render | CSS crítico inline (< 14 KB) y una sola hoja hasheada e `immutable` por ruta |
| 6 | HTML con `no-store`, SSR de **0,4 a 2,1 s** y `HEAD` responde 405 | El TTFB depende del origen en cada visita | HTML cacheado en el edge con `stale-while-revalidate` y purga por eventos al cambiar el catálogo (< 2 s). Personalización (sesión, carrito) en una petición aparte y pequeña. **TTFB < 100 ms desde el edge** |
| 7 | **CLS de 0,40** en home móvil y 0,24 en escritorio | Saltos visuales y clics fallidos | Dimensiones reservadas en imágenes, sliders y banners; fuentes con `size-adjust`. **CLS < 0,05** en CI |
| 8 | Checkout en otra app, con **jQuery 1.8.3 y Bootstrap 2.3.2**, desde otro dominio (`io2.vtex.com`) | Doble stack que mantener, mismo origen roto, personalización frágil vía `checkout6-custom.js` | Checkout en el mismo framework y origen que la tienda, con componentes headless y en una página. **Presupuesto: < 120 KB de JS** |
| 9 | Imágenes sin `srcset` en home y categoría; 40 de 93 sobredimensionadas; caché de imágenes de 1 h a 1 año sin criterio | Bytes desperdiciados en móvil | Servicio de imágenes propio (AVIF/WebP, `srcset` automático por ancho, `fetchpriority=high` en la imagen LCP) y `immutable` de 1 año por hash |
| 10 | SEO roto por defecto: títulos vacíos o con "undefined", descripciones faltantes, 2 `h1`, sin `hreflang` | Indexación y CTR | Plantillas SEO validadas en build: un test falla si una ruta tiene título o descripción vacíos, más de un h1 o falta `hreflang` con varios locales |
| 11 | Etiquetas de terceros cargadas sin configurar (GTM sin ID) más 3 beacons de VTEX | JS y red desperdiciados; privacidad | Terceros solo con ID configurado, cargados después de la interacción o mediante un worker (Partytown) y con consentimiento |
| 12 | `IntrospectionQuery` en el bundle y 49 operaciones GraphQL distintas | Superficie grande, consultas sin persistir | Consultas persistidas por hash (GET cacheable en CDN) e introspección deshabilitada en producción |

## 8. Presupuestos de rendimiento de Apexx (bloquean el merge en CI)

| Métrica (móvil, Slow 4G, CPU 4×) | VTEX elpaps (mediana medida) | Meta Apexx |
|---|---|---|
| LCP de producto | 3,6 s | **< 1,5 s** |
| LCP de categoría | 13,7 s | **< 1,8 s** |
| TBT de categoría | 6.364 ms | **< 150 ms** |
| CLS de home | 0,40 | **< 0,05** |
| JS de producto (gzip) | 861 KB | **< 100 KB** |
| HTML de producto (gzip / descomprimido) | 115 KB / 953 KB | **< 30 KB / < 120 KB** |
| Peticiones en categoría | 246 | **< 40** |
| TTFB del HTML (edge) | 450–1.270 ms | **< 100 ms** |
| Llamadas API por carga de categoría | ~105 | **≤ 3** |

## 9. Uso de REA

- `rea analyze-web-bundle` sobre la página de producto, con Chromium en CDP local y `--allowed-origins` para `elpaps.myvtex.com`, `elpaps.vtexassets.com` e `io.vtex.com.br`. Resultado: 41 scripts capturados y analizados, 926.063 nodos AST visitados, 0 fallos de parseo y estado `partial`. Encontró 49 operaciones GraphQL y `/api/sessions`. No encontró source maps porque no se pidieron.
- Limitación declarada por REA: el análisis es estático y no ejecuta el código. Los endpoints que se construyen con strings y la lógica del servidor pueden quedar fuera.

Los datos crudos (HAR equivalente de CDP, evidencia de REA, scripts) quedaron fuera del repositorio porque contienen cookies de sesión.
