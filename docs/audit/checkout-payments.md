# Auditoría de checkout y pagos — VTEX (cuenta `elpaps`)

> Fecha: 2026-11-10 · Alcance: solo lectura sobre configuración de checkout y del gateway de pagos.
> Se crearon **carritos desechables** (orderForms) con datos de prueba evidentes (`test@example.com`, "Test Apexx") para llegar a los pasos de perfil y envío. **No se envió ningún pedido** y no se llamó a ningún endpoint de autorización, captura o cancelación.
> No se incluyen credenciales, cookies, tokens ni datos personales. Los valores secretos de las afiliaciones de pago se verificaron como enmascarados y no se copiaron.

## 1. Método

| Parámetro | Valor |
|---|---|
| Navegador | Chromium headless (Playwright), caché desactivada, igual que `frontend.md` |
| Móvil | "Moto G4", Slow 4G emulado (150 ms RTT, 1,6 Mbps bajada), CPU 4× más lenta |
| Escritorio | 1366×900 sin limitar red ni CPU |
| Corridas | 3 por página y modo, se reporta la **mediana** |
| Páginas | `/checkout/#/cart` con 1 ítem y con 3 ítems; `#/email`, `#/profile`, `#/shipping`, `#/payment` con un carrito que ya tenía perfil y dirección de prueba |
| Interacción | Clic en "+" de cantidad en el carrito: peticiones generadas, tamaño y latencia |
| APIs | `orderForm` (crear, agregar, perfil, envío), simulación, configuración; gateway `elpaps.vtexpayments.com.br` (solo GET) |
| Origen | Contenedor en EE. UU. tras un proxy (~75 ms de base de red, igual que `backend.md`) |

**Limitaciones:** la red de la auditoría bloqueó telemetría de VTEX (`telemetry.vtex.com`, `rc.vtex.com`, `activity-flow.vtex.com`), Google, `elpaps.vteximg.com.br` (archivos personalizados legacy) y `master--elpaps.myvtex.com`. En producción esas peticiones sí se harían, así que **los números reales serían peores**. Las rutas `#/profile` y `#/payment` se redirigen solas a `#/shipping` porque el carrito de prueba no tenía opción de envío elegida (ver H6); sus filas miden en la práctica la página de envío.

## 2. Rendimiento del checkout (medianas)

### Móvil (Slow 4G, CPU 4×)

| Paso | TTFB | FCP | **LCP** | CLS | **TBT** | Load | Peticiones | JS transferido | Llamadas a `/api/checkout` |
|---|---|---|---|---|---|---|---|---|---|
| Carrito, 1 ítem | 340 ms | 4,0 s | **4,0 s** | 0,09 | 761 ms | 7,6 s | 107 | 855 KB (46 archivos) | 3 |
| Carrito, 3 ítems | 356 ms | 4,0 s | **4,0 s** | **0,19** | 750 ms | 7,7 s | 113 | 855 KB (46) | 3 |
| Email | 329 ms | 3,9 s | **7,7 s** | **0,21** | 823 ms | 7,7 s | 102 | 855 KB (46) | 2 |
| Perfil → envío | 468 ms | 4,0 s | **7,1 s** | 0,13 | 823 ms | 7,8 s | 103 | 855 KB (46) | 2 |
| Envío | 292 ms | 3,8 s | **7,0 s** | 0,13 | 808 ms | 7,5 s | 100 | 855 KB (46) | 2 |
| Pago → envío | 307 ms | 3,9 s | **7,2 s** | 0,13 | 792 ms | 7,6 s | 104 | 855 KB (46) | 2 |

### Escritorio (sin limitar)

| Paso | TTFB | FCP | LCP | CLS | TBT | Load | Peticiones | Transferido total |
|---|---|---|---|---|---|---|---|---|
| Carrito, 1 ítem | 336 ms | 1,1 s | 3,3 s | 0,00 | 71 ms | 2,3 s | 102 | 1,04 MB |
| Carrito, 3 ítems | 308 ms | 1,1 s | 3,4 s | 0,04 | 64 ms | 2,5 s | 106 | 1,04 MB |
| Email | 295 ms | 1,2 s | 2,1 s | 0,06 | 65 ms | 2,5 s | 97 | 1,04 MB |
| Envío | 274 ms | 1,4 s | 2,2 s | 0,04 | 87 ms | 2,5 s | 97 | 1,04 MB |

**Lectura:** en móvil **ningún paso del checkout pasa Core Web Vitals** (LCP ≤ 2,5 s). El usuario espera unos 4 s para ver su carrito y unos 7 s en cada paso siguiente. Cada paso descarga los mismos 855 KB de JavaScript y 9 hojas CSS. Ese peso no cambia con el número de ítems: es el costo fijo de la aplicación.

### Interacción: cambiar la cantidad de un ítem

| Modo | Llamadas al backend | Respuesta | Latencia |
|---|---|---|---|
| Escritorio | 1 `POST /orderForm/{id}/items/update` (más 3 a 11 peticiones de telemetría/analítica) | ~4,1 KB gzip (orderForm completo) | 245–470 ms |
| Móvil | Igual | ~4,1 KB gzip | 420–540 ms |

Cada clic en "+" devuelve el **orderForm completo** (16 a 22 KB sin comprimir) aunque solo cambió un número.

### Peticiones al cargar el carrito

1. `GET /api/checkout/pub/gift-cards/providers`
2. `POST /api/checkout/pub/orderForm` (lectura del carrito hecha con POST)
3. `POST /api/checkout/pub/orderForm/{id}/items...` (una **mutación al solo abrir la página**)

Además se disparan **unas 28 peticiones de telemetría** de VTEX (`telemetry.vtex.com`) por cada carga de página.

## 3. Stack del checkout (Checkout v6)

El checkout es **otra aplicación**, independiente del storefront de VTEX IO. Corre en `io2.vtex.com` y usa tecnologías de hace 10 a 14 años:

| Componente | Versión | Año aprox. |
|---|---|---|
| checkout-ui | v6.155.0 | — |
| jQuery | 1.8.3 | 2012 |
| Bootstrap (CSS + JS) | 2.3.2 | 2013 |
| Knockout.js | 2.3.0 | 2013 |
| Underscore | 1.7.0 | 2014 |
| Dust.js (plantillas) | 2.3.5 | 2014 |
| Parsley (validación) | 2.0.3 | 2014 |
| curl.js (cargador AMD) | 0.8.10-vtex.2 | 2014 |
| Font Awesome | 3.2.1 | 2013 |
| es5-shim / es5-sham | 2.1.0 | 2013 |
| Shims de Internet Explorer 7, 8 y 9 | `IE7.js`, `IE8.js`, `IE9.js`, `css3-mediaqueries.js` | — |
| vtex.js | v2.13.1 | — |
| portal-ui | v1.21.0 | — |
| vtex-id-ui | 3.28.0 | — |

**JS más pesado (transferido):** `lib-common.min.js` 271 KB, `ko-templates.min.js` 142 KB, `checkout6-custom.js` (personalización de la tienda) **98 KB**, `checkout.min.js` 87 KB, jQuery 33 KB, bundle de Knockout 32 KB, `vtex-diagnostics-web` 29 KB, `vtexid.min.js` 26 KB.

**Personalización:** se hace con dos archivos globales (`checkout6-custom.js` y `checkout6-custom.css`, más un `checkout-custom.css` legacy en `/arquivos/`). Funciona como parches de jQuery sobre el DOM, sin componentes, versiones ni pruebas. Cualquier cambio de VTEX en el HTML puede romperlos.

## 4. API de checkout (orderForm)

### Viajes de red para completar una compra

| # | Llamada | Respuesta sin comprimir | Latencia medida |
|---|---|---|---|
| 1 | Crear/obtener `orderForm` | ~4 KB vacío | 220 ms |
| 2 | Agregar ítems | 14–18 KB | 214–290 ms |
| 3 | `attachments/clientProfileData` | 19 KB | 396 ms |
| 4 | `attachments/shippingData` | 22 KB | 520 ms |
| 5 | `attachments/paymentData` | ~22 KB | no medido (no se llegó al pago) |
| 6 | `POST /transaction` (crear pedido) | — | no ejecutado |
| 7 | Envío de datos de pago al gateway | — | no ejecutado |
| 8 | `gatewayCallback` | — | no ejecutado |

**Mínimo 8 viajes secuenciales.** Cada mutación devuelve el **orderForm completo** con 35 secciones de nivel superior (`giftRegistryData`, `purchaseAgentsData`, `merchantContextData`, `hooksData`...). Solo los pasos 1 a 4 suman unos **1,4 s de servidor**, antes de pagar.

### Comportamientos observados

| Prueba | Resultado |
|---|---|
| Agregar el mismo SKU dos veces con `POST /items` | Mantiene cantidad 1. El "agregar" no suma: se comporta como "fijar" sin decirlo |
| `expectedOrderFormSections: ["items","totalizers"]` | Se ignora: 16,4 KB, igual que sin el filtro |
| Compresión | Sí: 16,4 KB → 3,4 KB con gzip |
| `GET` de un orderForm con id inventado (`000…0`) | **200** con un carrito vacío que adopta ese id |
| Código postal sin cobertura | **200** con errores `cannotBeDelivered` dentro de `messages[]` y 0 opciones de envío |
| Cantidad inválida (`-1`) | **400** con `{"error":{"code":"CHK0023",...},"operationId":...}` |
| Caché | `no-store, no-cache, must-revalidate`; sin `ETag` |
| Idempotencia | No hay `Idempotency-Key`. Los reintentos dependen del comportamiento implícito de cada endpoint |

### Configuración de checkout de la cuenta

`GET /api/checkout/pvt/configuration/orderForm` expone 20 opciones: pagos (preautorización, cuotas), impuestos, mínimos de cantidad y valor, `allowMultipleDeliveries`, `allowManualPrice`, `allowMultipleCoupons`, `maxNumberOfWhiteLabelSellers`, reCAPTCHA (`recaptchaValidation`, `recaptchaMinScore`), enmascarado de estado en dirección, cookies de propiedad del carrito, `apps` (campos personalizados) y otras. El endpoint `window-to-cancel` devolvió **404**.

## 5. Gateway de pagos (`elpaps.vtexpayments.com.br`)

### Modelo observado

| Entidad | En la cuenta | Qué es |
|---|---|---|
| Payment systems | 4: Promissory, Visa, Mastercard, American Express | Medio de pago visible al comprador. 30 propiedades cada uno: cuotas, BIN, emisor, país, vencimiento, recurrencia, documento... |
| Afiliaciones (conectores configurados) | 4: TestConnector ("Promo 12 MSI"), Mercado Pago V1, Banamex (ficha de depósito), Promissory | Instancia de un conector con sus credenciales |
| Reglas (condiciones de pago) | 6: 3 tarjetas con 1 a 12 MSI, Promissory activo, Mercado Pago y ficha Banamex inactivos | Une medio de pago, conector, antifraude, canal de venta, cuotas, fechas, BIN excluidos y valor mínimo |
| Conectores disponibles | **364** | Catálogo de integraciones de pago del gateway |
| Transacciones | 2 321 | Cada una con interacciones, liquidaciones, reembolsos, cancelaciones, antifraude, huella del dispositivo, IP y user agent |

Las tres reglas de tarjeta tienen **antifraude activado** y cuotas de 1 a 12 a 0% de interés, para todos los canales (`:ALL:`).

### Latencias (TTFB, 10 muestras)

| Endpoint | p50 | máx |
|---|---|---|
| `GET /api/pvt/merchants/payment-systems` | 325 ms | 654 ms |
| `GET /api/pvt/rules` | 301 ms | 413 ms |
| `GET /api/pvt/affiliations` (1 muestra) | 750 ms | — |
| `GET /api/pvt/connectors` (1 muestra) | 660 ms, **2,1 MB** | — |
| `GET /api/pvt/transactions?page=1` (1 muestra) | 783 ms, 81 KB por 15 transacciones | — |

### Errores y seguridad

| Prueba | Resultado |
|---|---|
| Endpoint inexistente (`/payment-systems/2/installments`) | **404 con una página HTML de IIS** (`404 - File or directory not found`), no JSON |
| Regla con id inexistente | **200** con cuerpo `null` |
| Secretos en afiliaciones (`AccessToken`, `PublicKey` de Mercado Pago) | **Enmascarados** en la respuesta (correcto) |
| Transacciones | El listado expone IP, user agent, huella del dispositivo e id de sesión del comprador a cualquier app key con ese permiso |
| Caché | `no-store` |

## 6. Hallazgos

Ordenados por impacto en conversión y en costo de operación.

### H1 · Checkout lento en móvil en todos los pasos (crítico)
- LCP de 4,0 s en el carrito y de **7,0 a 7,7 s** en email y envío; TBT de unos 800 ms; 100 a 113 peticiones por paso.
- El checkout es el último lugar donde se puede perder una venta que ya estaba decidida.
- **Apexx:** checkout de una sola página, renderizado en servidor, con menos de **60 KB de JS** y LCP menor a **1,5 s** en móvil. Presupuesto controlado en CI.

### H2 · Stack de 2012 a 2014 en producción (crítico)
- jQuery 1.8.3, Bootstrap 2.3.2, Knockout 2.3.0, Dust 2.3.5, curl.js y shims para Internet Explorer 7, 8 y 9 se descargan en 2026.
- Es difícil de mantener, de asegurar y de contratar gente que lo conozca.
- **Apexx:** el checkout comparte componentes, diseño y builder con el resto de la tienda. Sin librerías legacy, solo navegadores con soporte vigente.

### H3 · Personalización con parches globales de 98 KB (alto)
- `checkout6-custom.js` (98 KB) y dos CSS personalizados modifican el DOM con jQuery. No hay versiones, vista previa ni pruebas.
- Una actualización de VTEX puede romper la personalización sin aviso.
- **Apexx:** el checkout es **editable en el builder** con bloques (campos extra, banners, sellos de confianza, upsell), con vista previa y publicación versionada y reversible. La lógica personalizada se resuelve con **extensiones declaradas**, nunca parcheando el DOM.

### H4 · Ocho viajes secuenciales con el orderForm completo en cada uno (alto)
- Cada mutación devuelve las 35 secciones del orderForm (16 a 22 KB). El filtro `expectedOrderFormSections` se ignora.
- Datos del cliente, envío y pago van en llamadas separadas: los pasos 1 a 4 suman unos 1,4 s de servidor y faltan el pago y la transacción.
- **Apexx:** API de checkout con **parches parciales** (`PATCH /checkouts/{id}` con varias secciones a la vez) y respuestas que devuelven solo lo que cambió. Compra completa en **3 viajes**: carrito, datos combinados y pago.

### H5 · Sin idempotencia explícita; semántica sorpresiva (alto)
- `POST /items` con un SKU existente no suma cantidad. Abrir el carrito ya dispara una mutación.
- `GET` de un id de carrito inventado responde 200 con un carrito nuevo con ese id.
- No hay `Idempotency-Key` para reintentos seguros en redes móviles inestables.
- **Apexx:** `Idempotency-Key` obligatorio en toda mutación de carrito, checkout y pago. Ids de carrito firmados y verificables. Leer nunca modifica.

### H6 · Flujo por pasos que redirige sin explicar (medio)
- Entrar a `#/profile` o `#/payment` lleva automáticamente a `#/shipping` si falta elegir una opción de envío. El usuario no sabe por qué.
- Con un código postal sin cobertura, la API responde 200 y los errores quedan en `messages[]`.
- **Apexx:** checkout en una sola página con validación en línea y mensajes accionables. Los errores de cobertura se detectan **en la página de producto**, antes de agregar al carrito.

### H7 · Telemetría pesada en el camino crítico (medio)
- Unas 28 peticiones a `telemetry.vtex.com` por cada carga de página del checkout, más analítica en cada clic.
- **Apexx:** telemetría agrupada en una sola llamada con `sendBeacon` al cerrar o en segundo plano, nunca durante el render ni la interacción.

### H8 · Errores del gateway en HTML de IIS y 200 con `null` (medio)
- Una ruta inexistente devuelve una página HTML de IIS. Un recurso inexistente responde 200 con `null`.
- **Apexx:** RFC 9457 (`application/problem+json`) también en pagos, con códigos estables y documentados.

### H9 · Configuración de pagos lenta y pesada (medio)
- Cada lectura de configuración del gateway tarda 300 a 750 ms. El catálogo de conectores pesa **2,1 MB** en una sola respuesta sin paginar.
- **Apexx:** configuración de pagos en caché caliente (menos de 10 ms) y catálogo de integraciones paginado y buscable.

### H10 · Exposición de datos del comprador en transacciones (medio, privacidad)
- El listado de transacciones incluye IP, user agent, huella del dispositivo y sesión del comprador para cualquier app key con permiso de lectura.
- **Apexx:** datos sensibles del comprador separados por permiso específico, enmascarados por defecto y con registro de auditoría de quién los consultó.

## 7. Capacidades que Apexx debe igualar o superar

1. **Carrito persistente** asociado a cookie y a usuario, con restauración entre dispositivos.
2. **Simulación** de precio, envío y disponibilidad por código postal antes del checkout.
3. **Múltiples entregas** en un pedido y **retiro en tienda** (pickup points).
4. **Cupones múltiples**, regalos seleccionables (`selectableGifts`) y totales por sección (`totalizers`).
5. **Campos personalizados** del carrito (`customData`, `apps`) y datos de factura (`invoiceData`).
6. **Gift cards** como medio de pago con proveedores configurables.
7. **Medios de pago** de tarjeta con **meses sin intereses** (1 a 12 MSI), efectivo o ficha de depósito (Banamex), Mercado Pago y pago contra entrega o promisorio.
8. **Reglas de pago** por canal, emisor, BIN, país, monto mínimo, fechas y nivel de tarjeta, con **antifraude** por regla.
9. **Catálogo amplio de conectores**. VTEX tiene 364; Apexx debe priorizar los de cada país: Mercado Pago, Stripe, Adyen, Conekta, OpenPay, Kushki, PayU, OXXO y SPEI.
10. **reCAPTCHA** configurable, **mínimo de compra** y **límite de cantidad** por ítem.
11. **Transacciones** con historial de interacciones, liquidación, reembolso parcial y cancelación.
12. **Sellers white-label** y pedidos de marketplace con pagos divididos.

## 8. Decisiones de diseño para Apexx

| # | Decisión | Resuelve |
|---|---|---|
| C1 | Checkout de una página, SSR, menos de 60 KB de JS y LCP menor a 1,5 s en móvil | H1, H2 |
| C2 | Checkout editable en el builder con bloques versionados y vista previa | H3 |
| C3 | Extensiones declaradas (campos, validaciones, reglas) en lugar de parches de DOM | H3 |
| C4 | `PATCH` parcial con respuestas de diferencias; compra en 3 viajes | H4 |
| C5 | `Idempotency-Key` obligatorio; ids firmados; GET sin efectos | H5 |
| C6 | Validación en línea y cobertura de envío visible desde la página de producto | H6 |
| C7 | Telemetría agrupada fuera del camino crítico | H7 |
| C8 | Errores RFC 9457 en todas las APIs, incluido pagos | H8 |
| C9 | Configuración de pagos en caché caliente y catálogo de conectores paginado | H9 |
| C10 | PII del comprador con permisos específicos, enmascarada y auditada | H10 |
| C11 | Pagos con tokenización en el proveedor; Apexx nunca toca el número de tarjeta (alcance PCI SAQ A) | Seguridad |

## 9. Pendiente

- Medir el paso de pago real y el envío de la transacción en un workspace de pruebas, con un conector de prueba y autorización explícita.
- Repetir las mediciones sin bloquear telemetría ni analítica para tener el costo real en producción.
- Medir el checkout desde una región de LatAm.
