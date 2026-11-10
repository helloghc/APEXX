# Auditoría de conectores e integraciones — VTEX (cuenta `elpaps`) y diseño del ecosistema Apexx

> Fecha: 2026-11-10 · Alcance: **solo lectura** sobre la tienda (GET, navegador sin compras).
> Objetivo: entender cómo funcionan los conectores más usados de VTEX para replicar su **funcionalidad** en Apexx con código propio (sala limpia). No se copió código propietario. El código abierto consultado se cita con su licencia.
> Decisión de negocio ya tomada: **Stripe es el único proveedor de pagos** de Apexx (Stripe Connect + Stripe Invoicing), y la factura fiscal mexicana (**CFDI 4.0**) se emite con un PAC como conector.

## 1. Método

| Fuente | Qué se obtuvo |
| --- | --- |
| Runtime del storefront (`__RUNTIME__` de home, PLP, PDP y búsqueda) | 71 apps IO cargadas, con versión |
| **REA 6.8.0** `analyze-web-bundle` sobre home (42 scripts) y PDP (41 scripts) vía CDP | Operaciones GraphQL, llamadas `fetch`, rutas y scripts de terceros |
| Navegador (Playwright + CDP) | `dataLayer`, pixels activos y peticiones a terceros |
| APIs de solo lectura de la cuenta | Afiliados de marketplace, afiliaciones y catálogo de conectores de pago, sellers |
| `github.com/vtex/openapi-schemas` (licencia en el repo) | Contratos de **Payment Provider Protocol**, **Marketplace Protocol**, **Orders Feed/Hook**, **change notification**, **Giftcard Provider Protocol** |
| Repos públicos de `vtex-apps` (clonados en local, fuera del repo de Apexx) | `google-tag-manager` (AGPL-3.0), `facebook-pixel` (LGPL-3.0), `pixel-manager`, `payment-provider-example`, `availability-notify`, `reviews-and-ratings` (estos cuatro sin archivo de licencia; `package.json` dice `UNLICENSED`). **Solo se leyeron para entender el flujo; Apexx no reutiliza nada de ese código.** |

Limitaciones: los repos de los conectores de marketplace (Mercado Libre, Amazon), de envíos y de ERP no son públicos. Sus flujos se reconstruyeron a partir de los contratos OpenAPI públicos y de la configuración visible en la cuenta. `developers.vtex.com` y los dominios de terceros (Facebook, Google, Weni) estaban bloqueados por la red de la auditoría.

## 2. Integraciones encontradas en `elpaps`

| Integración | Evidencia | Tipo |
| --- | --- | --- |
| **Mercado Libre** | Afiliado `RML` "Mercado Livre - elpaps - gold_special", canal de venta 1, endpoint interno `meliintegration.vtexinternal.com` | Marketplace (conector nativo de VTEX) |
| **Mercado Pago v1** | Afiliación de pago configurada: 40 parámetros (planes de meses sin intereses `now3Plan`…`now30Plan`, antifraude, autocaptura, `mpFee`, `marketplace`) | Pago |
| **Banamex (ficha de depósito)** | Afiliación `BanamexInvoiceConnector` | Pago offline |
| **TestConnector / Promissory** | Visa, Mastercard y Amex van a `TestConnector`; `Promissory` es el único medio marcado como entregado | Pago (pruebas) |
| **Meta Pixel** | `vtex.facebook-pixel@2.2.5`; `fbq` cargado; `connect.facebook.net/fbevents.js` | Analítica y anuncios |
| **Google Tag Manager** | `vtex.google-tag-manager@2.10.0`, instalado con **`gtmId = ""`**: el contenedor nunca carga, aunque la app sigue empujando `pageView` y `homeView` al `dataLayer` | Analítica (rota) |
| **Pixel Manager** | `vtex.pixel-manager@1.9.0`: bus de eventos del navegador al que se cuelgan los pixels | Infraestructura |
| **Reseñas** | `vtex.reviews-and-ratings@2.13.0`: datos en VBase y Master Data; 4 queries GraphQL por producto | Contenido |
| **PIM externo** | El tema propio (`elpaps.store-theme@4.5.7`) tiene 9 componentes `Pim*` que leen especificaciones `PIM_Long_Desc`, `PIM_QA_Section` y `PIM_Tech_Specs`: el PIM escribe en campos de especificación del catálogo | PIM |
| **Weni (chat / agente)** | `cdn.cloud.weni.ai/agentic-cx.js` en el HTML | Chat / WhatsApp |
| **Telemetría VTEX** | `activity-flow.vtex.com/af/af.js`, `io.vtex.com.br/rc/rc.js` (request-capture), `vtex.sae-analytics`, `vtex.checkout-splunk` | Telemetría propia |
| **reCAPTCHA Enterprise** | `google.com/recaptcha/enterprise.js` en todas las páginas | Antibots |

**Catálogo de conectores de pago disponibles:** 364. Entre ellos hay 3 de Stripe (`stripe-payment-provider-connector-v3`, `stripe-stripe-payments-provider-v0` y uno de un partner), Mercado Pago v2, PayPal (3), Openpay (2), Conekta, Adyen (4), Kueski (3), Aplazo, Clip (2), PayU (4), Nuvei y Cybersource (3). La mayoría son de terceros publicados bajo el Payment Provider Protocol.

## 3. Cómo se conectan las integraciones a VTEX: los cinco protocolos

| Protocolo | Contrato (de `openapi-schemas`) | Dirección y disparador | Lo usan |
| --- | --- | --- | --- |
| **Payment Provider Protocol (PPP)** | `GET /manifest`, `POST /payments`, `/cancellations`, `/settlements`, `/refunds`, `/inbound/{action}`, `/authorization/*` | VTEX llama al proveedor. Si la respuesta queda `undefined`, el gateway **reintenta** (en VTEX IO el conector no puede devolver la llamada y debe pedir `retry`) | Stripe, Mercado Pago, Adyen, Kueski… |
| **Marketplace Protocol** (seller externo y marketplace externo) | Seller: `POST /pvt/orderForms/simulation`, `POST /pvt/orders`, `/fulfill`, `/cancel`. Marketplace: `/invoice`, `/tracking`, `/cancel`, `/changes/.../send-agreement`. Mapper: `channel-manager/connector`, `mkp-category-mapper` | Pedido entrante: el marketplace empuja el pedido y VTEX simula. Catálogo saliente: el conector **lee** el catálogo | Mercado Libre, Amazon, Liverpool, Walmart (vía conectores o integradores) |
| **Afiliados + change notification** | `GET/PUT /fulfillment/pvt/affiliates`; `POST /notificator/{seller}/changenotification/{sku}/price` y `/inventory`; `POST catalog_system/pvt/skuseller/changenotification/{sku}` | Un cambio en VTEX genera una **notificación sin datos**: "algo cambió en el SKU X". El afiliado luego **vuelve a pedir** simulación o catálogo | Mercado Libre (`RML`), conectores de marketplace |
| **Broadcaster** (eventos internos de IO) | La app declara `events.broadcasterNotification` con `sender: vtex.broadcaster` | Notifica cambios de SKU **después de la indexación** | `availability-notify`, apps de feeds |
| **Orders Feed / Hook** | `GET/POST/DELETE /api/orders/feed/config`, `GET/POST /api/orders/feed` (leer y confirmar), `/api/orders/hook/config` | **Feed:** cola que el ERP consulta y confirma. **Hook:** POST a una URL por cambio de estado del pedido | ERP, WMS, envíos, facturación |
| **Pixel (navegador)** | Eventos `vtex:*` en `postMessage` (pixel-manager) | Del navegador a scripts de terceros | GTM, Meta, TikTok |

**Eventos de pixel observados en el código** (pixel-manager, GTM, Meta): `pageView`, `pageInfo`, `productView`, `productImpression`, `productClick`, `addToCart`, `removeFromCart`, `viewCart`, `beginCheckout`, `addShippingInfo`, `addPaymentInfo`, `orderPlaced`, `orderPlacedTracked`, `refund`, `search`, `promoView`, `promotionClick`, `addToWishlist`, `login`, `signUp`, `share`, `userData`, `cartChanged`, `cartLoaded`, `cartId`, `categoryView` y `departmentView`. Meta los traduce a `PageView`, `ViewContent`, `AddToCart` y `Purchase`. GTM los traduce a eventos de GA4.

## 4. Conectores más usados: cómo funcionan y qué duele

| Conector | Propósito | Entidades y dirección | Disparador en VTEX | Autenticación | Fallas y reintentos | Dolores conocidos u observados |
| --- | --- | --- | --- | --- | --- | --- |
| **Mercado Libre** | Publicar catálogo y recibir pedidos | Productos, precio y stock salen; pedidos entran | Afiliado + change notification + simulación; pedidos por Marketplace Protocol | OAuth de Mercado Libre guardado en el conector de VTEX | Reintentos internos opacos para el comerciante | Precio y stock dependen del **indexador** y de la notificación sin datos: desfase y sobreventa. Mapeo de categorías y atributos manual. Tipo de publicación fijo por afiliado (`gold_special`) |
| **Amazon / Walmart / Liverpool / Coppel** | Marketplaces | Igual que Mercado Libre | Marketplace Protocol, con frecuencia vía integrador de terceros | Credenciales de cada marketplace | Dependen del integrador | Doble integración (VTEX ↔ integrador ↔ marketplace); costo extra por pedido |
| **Google Merchant Center** | Shopping y listados gratuitos | Catálogo, precio y disponibilidad salen | Feed XML o app; el feed se regenera por lotes | Cuenta de Google | Rechazos de productos visibles solo en Merchant Center | Feed desfasado del precio real (rechazos por "precio no coincide"); en `elpaps` la ruta de feed responde 404 |
| **Meta (Pixel + Catálogo)** | Anuncios y catálogo de Instagram/Facebook | Eventos del navegador; catálogo por feed | Pixel en el navegador; feed por lotes | ID de pixel; token del catálogo | Ninguna en el navegador: si el script falla, el evento se pierde | **Solo del lado del navegador**: bloqueadores y Safari ITP pierden conversiones; no usa Conversions API del servidor |
| **TikTok (Pixel / Shop)** | Anuncios y tienda | Igual que Meta | Pixel; catálogo por feed | Token | Igual que Meta | Igual que Meta |
| **Google Analytics 4 / GTM** | Analítica | Eventos | `dataLayer` desde pixel-manager | ID de GTM | Ninguna | En `elpaps` está **instalado sin ID**: no mide nada y nadie se entera |
| **Envíos (Envia, Skydropx, 99minutos, FedEx, DHL)** | Cotizar, generar guía y rastrear | Pedido sale; guía y tracking entran | Order hook o feed + `/invoice/{n}/tracking` | API key de la paquetería | Depende del integrador | Cotización por política de envío estática (la API de logística no cotiza en vivo); el tracking se actualiza por llamadas manuales a la API de pedidos |
| **ERP (SAP, NetSuite, Dynamics, Odoo, Bling/Tiny)** | Catálogo, stock, precios, pedidos y facturas | Catálogo y stock entran; pedidos salen | Orders Feed (consultar y confirmar) + APIs de catálogo y pricing | App key/token de VTEX | El ERP debe confirmar cada ítem del feed; si no, se reentrega | Límites de paginación y rate limits al sincronizar catálogos grandes (ver `backend.md` H3); cada carga masiva **dispara reindexación** |
| **PIM** | Contenido enriquecido | Especificaciones entran | API de catálogo (especificaciones) | App key | Lotes | Los campos del PIM viven como especificaciones (`PIM_*`) y el tema necesita código propio para mostrarlos |
| **Klaviyo / correo marketing** | Correos y SMS, carrito abandonado | Clientes, eventos y pedidos | Pixel + order hook | API key | Depende de la app | Eventos solo del navegador; el carrito abandonado depende del pixel |
| **Reseñas (VTEX, Yotpo)** | Calificaciones | Reseñas | GraphQL por producto | — | — | **N+1:** 4 queries por producto; 90 llamadas en una categoría (`frontend.md`) |
| **Búsqueda externa (Algolia, Constructor)** | Búsqueda | Catálogo sale | Broadcaster o feed | API key | Reindexado por lotes | Doble indexación (VTEX + proveedor) |
| **Chat / WhatsApp (Weni)** | Atención y ventas | Conversaciones, carrito, pedido | Script en el navegador + APIs | Token | — | Otro script de terceros en cada página |
| **Pagos (Mercado Pago, Stripe, Adyen)** | Cobro | Pago | PPP | Credenciales del proveedor en la afiliación | Reintentos del gateway si la respuesta es `undefined` | Configuración de 40 campos (Mercado Pago); planes MSI duplicados entre conector y reglas; consultas al gateway de 300 a 750 ms (`checkout-payments.md`) |
| **Facturación electrónica (CFDI vía PAC)** | Factura fiscal | Pedido sale; XML/PDF y folio entran | Order hook + `/invoice` | API key del PAC | Depende del integrador | VTEX no emite CFDI: siempre es un integrador aparte |

## 5. Hallazgos

### H1 · Los conectores reciben "algo cambió", no qué cambió (crítico)
Las change notifications y el broadcaster mandan solo el id del SKU. El conector tiene que volver a pedir precio y stock, y lo que lee viene de un índice o caché que puede estar atrasado (`backend.md` H1 e `indexing.md`). Mercado Libre y Google reciben así precios y stock viejos: rechazos de feed y sobreventa.
**Apexx:** eventos **con datos y versión** (`product.updated` trae el producto completo con `version`); el conector nunca tiene que volver a preguntar.

### H2 · Tres mecanismos distintos para lo mismo (alto)
Hooks, feed con confirmación, broadcaster y change notification resuelven el mismo problema ("avísame cuando cambie algo") con contratos, reintentos y seguridad distintos.
**Apexx:** **un solo contrato de eventos** (CloudEvents 1.0) con dos formas de entrega: **webhook firmado (push)** y **feed por cursor (pull) con replay**.

### H3 · Integraciones rotas en silencio (alto)
GTM instalado sin ID y feed de Google en 404, sin ninguna alerta. El comerciante no tiene un panel de salud por conector.
**Apexx:** cada conector tiene **estado, último evento entregado, retraso (lag), errores y tasa de éxito**, con alertas por correo y en el admin.

### H4 · Analítica solo del navegador (alto)
Meta y GA4 dependen de scripts del navegador: se pierden conversiones con bloqueadores, Safari ITP y redes lentas. Además suman peso a cada página (`frontend.md`).
**Apexx:** eventos de comercio **del lado del servidor** (Meta Conversions API, GA4 Measurement Protocol, TikTok Events API) desde el bus de eventos, deduplicados con el pixel del navegador por `event_id`. En el navegador queda solo un pixel opcional, cargado en `idle`.

### H5 · Pagos configurados por formulario gigante (medio)
La afiliación de Mercado Pago tiene 40 campos. Los meses sin intereses se configuran en el conector y también en las reglas, y en `elpaps` las tarjetas reales siguen apuntando a `TestConnector`.
**Apexx:** un solo proveedor (**Stripe**) con **onboarding de Stripe Connect** (el comerciante conecta su cuenta en un clic), meses sin intereses y métodos locales activados desde el panel de Stripe y leídos por API. Una prueba de salud bloquea la publicación del checkout si el modo de pago no es `live`.

### H6 · Marketplaces con mapeo manual y doble integrador (medio)
Las categorías y atributos se mapean a mano por marketplace, y muchas tiendas pagan un integrador además de VTEX.
**Apexx:** conectores de marketplace **propios** con mapeo de categorías asistido (sugerencias con IA, editable) y validación previa: "estos 37 productos serán rechazados por falta de GTIN".

### H7 · Facturación fuera del flujo (medio)
VTEX no emite CFDI; siempre depende de un tercero conectado por hook.
**Apexx:** la facturación es parte del pedido: **Stripe Invoicing** para el comprobante comercial y el cobro, más un **conector PAC (Facturapi)** para timbrar el **CFDI 4.0**, con reintentos y estado visible en el pedido (tabla `invoices`). Stripe Invoicing **no** genera CFDI; para México siempre se necesita el PAC.

### H8 · Extensiones con acceso amplio (medio)
Una app de VTEX IO o un script de pixel corre con acceso amplio a la página o a la cuenta.
**Apexx:** permisos declarados por conector (`scopes`: `orders:read`, `products:write`…) y lógica personalizada en **WebAssembly** sin red ni disco salvo lo declarado.

## 6. Diseño Apexx: Connector SDK y contrato de eventos

### 6.1 Contrato de eventos (CloudEvents 1.0)

```json
{
  "specversion": "1.0",
  "id": "evt_01JC9A3K7Q4N2V7XW5T6Y0RMA",
  "source": "apexx://tenants/elpaps",
  "type": "inventory.level.updated",
  "subject": "sku/SKU-3512",
  "time": "2026-11-10T19:40:00.123Z",
  "datacontenttype": "application/json",
  "apexxtenant": "elpaps",
  "apexxsequence": 182734,
  "apexxversion": 42,
  "data": { "sku": "SKU-3512", "available": 14, "levels": [{ "locationId": "cdmx-1", "onHand": 20, "reserved": 6 }] }
}
```

- `apexxsequence`: orden total por tenant (es el id del outbox). Permite **replay desde cualquier punto** y detectar huecos.
- `apexxversion`: versión del recurso; el conector descarta eventos con versión menor a la que ya aplicó (**idempotencia**).
- `data` lleva el **estado completo** del recurso, no solo el id (H1).

**Tipos iniciales:**

| Dominio | Tipos |
| --- | --- |
| Catálogo | `product.created`, `product.updated`, `product.archived`, `variant.updated` |
| Precio | `price.updated` (canal, SKU, tramos) |
| Inventario | `inventory.level.updated` |
| Pedido | `order.created`, `order.paid`, `order.fulfilled`, `order.cancelled`, `order.refunded` |
| Pago | `payment.authorized`, `payment.captured`, `payment.failed`, `payment.refunded` |
| Factura | `invoice.issued`, `invoice.failed` |
| Cliente | `customer.created`, `customer.updated` (datos personales solo si el conector tiene el scope `customers:pii`) |
| Builder | `site.published`, `site.rolled_back` |
| Comercio (analítica del servidor) | `checkout.started`, `cart.updated` |

### 6.2 Entrega por webhook

| Cabecera | Valor |
| --- | --- |
| `Apexx-Event-Id` | id del evento (idempotencia del receptor) |
| `Apexx-Signature` | `t=<unix>,v1=<hex(HMAC-SHA256(secret, t + "." + body))>`; ventana de 5 min contra repetición; 2 secretos activos durante la rotación |
| `Apexx-Delivery-Attempt` | 1..N |

- **Reintentos:** backoff exponencial con jitter (10 s, 30 s, 2 min, 10 min, 1 h… hasta 24 h), solo ante 5xx, 429 o timeout (10 s). Un 4xx distinto de 429 va directo a la DLQ.
- **DLQ** por conector, con reenvío individual o masivo desde el admin.
- **Orden:** por `subject` (todos los eventos de un SKU en orden); entre subjects, en paralelo.
- **Rate limit** por conector y por tenant (token bucket), respetando `Retry-After`.
- **Circuit breaker:** tras N fallas seguidas el conector pasa a `degraded`, se pausa y se avisa al comerciante. Los eventos se acumulan y se entregan al recuperarse.

### 6.3 Entrega por feed (pull)

`GET /v1/events?after=<sequence>&types=order.*&limit=500` devuelve eventos en orden y un `nextCursor`. El ERP guarda su cursor; no hay "confirmar ítem por ítem" como en el Orders Feed de VTEX. Retención: 30 días, y más con exportación a almacenamiento.

### 6.4 SDK de conectores (Go)

```go
type Connector interface {
    Manifest() Manifest                                   // id, scopes, eventos que consume, configuración (JSON Schema)
    Handle(ctx context.Context, e Event) error            // idempotente por e.ID y e.Version
    Backfill(ctx context.Context, from Cursor) error      // carga inicial por streaming, sin reindexar nada
    Health(ctx context.Context) HealthReport
}
```

El runtime del SDK pone el cursor, los reintentos, la DLQ, los límites, las métricas (`lag_seconds`, `delivered_total`, `failed_total`) y los secretos por tenant (cifrados con la llave del tenant). La lógica personalizada del comerciante corre en WASM (wazero) con los scopes declarados.

### 6.5 Panel de salud por conector

Estado (`healthy`, `degraded`, `paused`), retraso en segundos, último evento entregado, tasa de éxito en 24 h, tamaño de la DLQ, próximos reintentos y botón "Reenviar desde…".

## 7. Pagos y facturación con Stripe (decisión tomada)

| Pieza | Diseño |
| --- | --- |
| **Stripe Connect** | Cada tienda conecta su cuenta (onboarding alojado por Stripe). Los cobros se hacen como *direct charges* en la cuenta del comerciante, con `application_fee_amount` como comisión de Apexx. Se guarda solo el `account_id` (tabla `payment_accounts`). **Validar con Stripe** las condiciones de Connect para cuentas en México. |
| **Checkout** | Payment Element / Checkout de Stripe (campos alojados: PCI SAQ A), 3-D Secure automático. En México: tarjetas, **meses sin intereses**, **OXXO** y **SPEI** (transferencia). |
| **Consistencia** | `Idempotency-Key` en cada llamada a Stripe derivada del id del pedido. Los webhooks de Stripe (`payment_intent.succeeded`, `charge.refunded`, `charge.dispute.created`) se verifican con su firma y se procesan una sola vez por `event.id`. |
| **Reversión** | Cancelación → `refund` en Stripe → reposición de stock → asientos de reversa en el libro mayor → `order.refunded`. Cada paso es idempotente y reintentable. |
| **Stripe Invoicing** | Comprobante comercial y cobro de facturas B2B (pago a plazo, recordatorios automáticos). |
| **CFDI 4.0 (México)** | **Conector PAC (Facturapi)**: al pagar el pedido se timbra el CFDI con los datos fiscales del comprador (RFC, régimen, uso de CFDI, código postal), con reintentos y estado visible. Cancelación de CFDI con motivo SAT al reembolsar. Público en general con factura global si el comprador no pide factura. |

## 8. Hoja de ruta de conectores (lanzamiento en México)

| # | Conector | Por qué primero | Mecanismo Apexx |
| --- | --- | --- | --- |
| 1 | **Stripe** (pagos, Connect, Invoicing) | Sin pagos no hay tienda; decisión tomada | Adaptador nativo + webhooks de Stripe |
| 2 | **Facturapi (CFDI 4.0)** | Obligación fiscal en México | Evento `order.paid` → timbrado → `invoice.issued` |
| 3 | **Mercado Libre** | Marketplace dominante en México; `elpaps` ya lo usa | Eventos con datos de precio y stock, pedidos entrantes, mapeo de categorías asistido |
| 4 | **Google Merchant Center** | Tráfico gratuito y Shopping | Content API por evento (no feed por lotes) |
| 5 | **Meta (Catálogo + Conversions API)** | Anuncios en Instagram y Facebook | Catálogo por evento + eventos del servidor deduplicados |
| 6 | **GA4 + GTM (servidor)** | Medición confiable | Measurement Protocol desde el bus de eventos |
| 7 | **Envia.com / Skydropx** | Paquetería multi-carrier en México | Cotización en vivo en el checkout, guía al pagar, tracking por webhook |
| 8 | **WhatsApp Business (Cloud API)** | Confirmaciones, carrito abandonado, atención | Eventos de pedido y carrito → plantillas aprobadas |
| 9 | **Klaviyo** | Correo y SMS de marketing | Eventos del servidor + sincronización de clientes con consentimiento |
| 10 | **Kit ERP (Odoo, NetSuite, SAP Business One)** | Tiendas medianas y grandes | Feed por cursor + APIs bulk de catálogo, stock y precio |

**Después:** Amazon México, Walmart, Liverpool, Coppel, TikTok Shop, Algolia y Constructor (opcionales: Apexx trae búsqueda propia) y reseñas (nativas en Apexx, con importador desde VTEX Reviews o Yotpo).

## 9. Resumen

- Los conectores de VTEX sufren el mismo problema de fondo que el storefront: dependen del **indexador** y reciben "algo cambió" en lugar del dato. Esa es la causa de raíz del desfase en Mercado Libre y Google.
- Apexx los reemplaza con **un solo contrato de eventos con datos y versión**, entregado por **webhook firmado o feed por cursor**, con reintentos, DLQ, replay y panel de salud.
- **Stripe** es el único proveedor de pagos; el **CFDI** se resuelve con un conector PAC dentro del flujo del pedido.
