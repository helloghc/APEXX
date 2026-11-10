# Auditoría de backend y APIs — VTEX (cuenta `elpaps`)

> Fecha: 2026-11-10 · Alcance: solo lectura y simulaciones de carrito (ninguna escritura sobre la tienda).
> Cuenta de desarrollo/demo del propietario del proyecto. No se incluyen credenciales ni datos personales de clientes.

## 1. Método

- **Origen de las mediciones:** contenedor en la nube (EE. UU.) a través de un proxy HTTPS. El apretón de manos TLS hasta VTEX es de unos **75 ms**, que forman parte de cada latencia medida. Para comparar con Apexx hay que restar esa base o medir ambos desde el mismo origen.
- **Latencia:** 20 peticiones secuenciales por endpoint midiendo TTFB (tiempo hasta el primer byte). Se informan p50, p95 y máximo.
- **Barrido funcional:** 34 endpoints de catálogo, precios, logística, promociones, checkout, OMS, Master Data, búsqueda, tenant y License Manager.
- **Comportamiento:** 8 escenarios de simulación de carrito, pruebas de paginación y de errores.
- Los datos crudos quedan fuera del repositorio.

## 2. Panorama de la cuenta

| Dato | Valor observado |
| --- | --- |
| Productos | 18 506 (catálogo); 18 484 indexados en búsqueda |
| Pedidos | 2 316 |
| Canales de venta (trade policies) | 4: MXN (México), IDR, VND, THB |
| Bodegas / docks / políticas de envío | 1 / varios / 4 |
| Entidades de Master Data | 26 (incluye duplicados por mayúsculas: `CL`/`cl`, `AD`/`ad`, `BK`/`bk`) |
| Promociones, cupones, impuestos activos | 0 / 0 / 0 |
| Configuración de precios | `migrationStatus: "Error"` (migración de pricing en estado de error) |
| Storefront | VTEX IO Store Framework (`render@8`), protegido con login de admin |

## 3. Latencias medidas (TTFB, 20 muestras)

| Endpoint | p50 | p95 | máx |
| --- | ---: | ---: | ---: |
| Producto (`/api/catalog/pvt/product/{id}`) | 156 ms | 417 ms | 447 ms |
| Contexto de SKU (`stockkeepingunitbyid`) | 152 ms | 363 ms | 394 ms |
| Búsqueda legacy (10 productos) | 142 ms | 381 ms | 429 ms |
| Intelligent Search (`product_search`) | 187 ms | **715 ms** | **1 065 ms** |
| Precio de un SKU (pricing v2) | 146 ms | 346 ms | 356 ms |
| Inventario de un SKU | 137 ms | 177 ms | 223 ms |
| Árbol de categorías | 131 ms | 161 ms | 190 ms |
| Listado de pedidos (OMS) | 136 ms | 235 ms | 372 ms |
| Simulación de carrito (1 ítem) | — | — | 535 ms (primera), 174–293 ms (siguientes) |
| Detalle de pedido | — | — | 696 ms |

**Lectura:** el p50 está dominado por la red (~75 ms de base). El problema es la **cola**: p95 de 2,5 a 4 veces el p50 en casi todos los servicios, y la búsqueda llega a 1 s. Esa variabilidad es lo que sufre un comprador en horas pico.

## 4. Hallazgos

Ordenados por impacto en un producto que compite con VTEX.

### H1 · Stock mostrado distinto del stock real (crítico)
- La búsqueda y el catálogo informan `AvailableQuantity: 100` para el SKU 3512.
- El inventario real es `totalQuantity 20 − reservedQuantity 6 = 14`.
- La simulación de carrito, pidiendo 500 unidades, entrega 14.
- Hay **tres verdades distintas** sobre el mismo dato: índice de búsqueda, catálogo y checkout. El comprador ve disponibilidad que no existe y lo descubre en el carrito.
- **Apexx:** el inventario es una sola fuente, publicada por eventos al índice y a la caché en menos de 2 s. La disponibilidad que muestra la tienda tiene una antigüedad conocida y garantizada.

### H2 · Respuestas enormes y sin compresión (alto)
- La búsqueda legacy devuelve **574 KB para 10 productos** (~57 KB por producto), **sin gzip** aunque el cliente lo pida.
- Cada producto arrastra cuotas de pago, muestras de SLA de envío por región, teasers, especificaciones completas y descripción HTML.
- **Apexx:** selección de campos (`fields=`) o GraphQL, compresión Brotli/zstd obligatoria y respuestas de listado de menos de 2 KB por producto.

### H3 · Límites de paginación que impiden recorrer el catálogo (alto)
- Búsqueda: `_to - _from` máximo 50 y `_from` máximo **2 500**. Con 18 484 productos, el 86% del catálogo **no se puede recorrer** por esta API.
- `GetProductAndSkuIds`: máximo 250 por página. SKU ids: 1 000 por página.
- Los ERP terminan combinando tres APIs para sincronizar.
- **Apexx:** paginación por cursor sin techo, endpoint de exportación en streaming (NDJSON) y webhooks de cambio para no tener que recorrer nada.

### H4 · Cinco formatos de error distintos (alto para integradores)
| API | Respuesta a "no encontrado" |
| --- | --- |
| Catálogo producto | `404` con cuerpo vacío |
| Catálogo SKU | `404` con un string JSON: `"SKU not found"` |
| Pricing | `404` con texto plano |
| OMS | `404` con `{"error":{"code":"OMS007","message":...}}` |
| Inventario | **`200`** con cantidad 0 para un SKU que no existe |
| Simulación | **`200`** con el error dentro de `messages[]` |
- **Apexx:** un solo formato (RFC 9457 `application/problem+json`) con códigos estables en todas las APIs.

### H5 · Modelo y nombres inconsistentes (medio, costo de desarrollo permanente)
- Tres representaciones del mismo producto: `catalog/pvt` (PascalCase), `catalog_system` (otro PascalCase con campos distintos) y búsqueda (camelCase mezclado con claves como `"Format IMG"` y `"Group 1"`).
- Errores tipográficos congelados en el contrato público: `commertialOffer`, `maximumValueAceptable`, `SecutityPrivacy`.
- Precios en decimales en catálogo y pricing (`1684.38`) pero en centavos enteros en checkout (`168438`).
- **Apexx:** un modelo canónico, camelCase, montos siempre en unidades menores con código de moneda (`{"amount":168438,"currency":"MXN"}`), especificación OpenAPI única y linter de contrato en CI.

### H6 · Búsqueda con cola de latencia alta (medio)
- Intelligent Search: p95 de 715 ms y máximo de 1 065 ms. Mismo resultado vía `vtexcommercestable` (1,65 s en frío) y `myvtex` (0,64 s).
- Autocompletar devolvió vacío para `"sh"` pese a haber miles de productos.
- **Apexx:** índice en memoria (Tantivy o Typesense) con p99 menor a 30 ms y autocompletar por prefijo y por error tipográfico desde el primer carácter.

### H7 · Simulación de carrito pesada y con reglas implícitas (medio)
- 12,5 KB para un carrito de un ítem; 16 secciones de nivel superior.
- El total (`totals`) no incluye envío: el cliente debe sumarlo desde `logisticsInfo`.
- Hay un tope de 50 unidades por ítem que no aparece en la configuración de checkout; solo se descubre en un mensaje.
- Envío lineal por unidad (1 ud = 122,00; 10 uds = 1 022,00 MXN) sin agrupar paquetes.
- **Apexx:** respuesta de simulación compacta con total final explícito (ítems, descuentos, envío, impuestos), reglas de cantidad visibles en la configuración y cálculo de envío por paquete o volumen.

### H8 · Pedido con 61 campos de nivel superior (medio)
- El detalle de pedido pesa 13 KB y tiene 61 claves (`marketplaceItems`, `budgetData`, `giftRegistryData`, `purchaseAgentData`...), la mayoría nulas.
- **Apexx:** pedido con núcleo pequeño y extensiones opcionales; historial de eventos consultable (`/orders/{id}/events`).

### H9 · Caché opaca (medio)
- Búsqueda legacy: `s-maxage=300` (5 minutos de caché compartida). Un cambio de precio o stock puede tardar hasta 5 minutos en verse.
- Pricing no expone `cache-control` ni `etag`.
- No hay cabeceras de rate limit (`X-RateLimit-*`) en ninguna respuesta: el integrador descubre el límite al recibir un 429.
- **Apexx:** invalidación por evento (no por tiempo), `ETag` en todo recurso y cabeceras de cuota estándar.

### H10 · Deuda de datos visible (bajo, pero revelador)
- Entidades de Master Data duplicadas por diferencia de mayúsculas.
- Migración de pricing en estado `Error`.
- Políticas de envío con `lastIndexedAt` y `processingStatus`: la logística también depende de un indexado asíncrono.
- **Apexx:** migraciones transaccionales con estado visible y reversibles; identificadores de entidad sin ambigüedad de mayúsculas.

## 5. Capacidades confirmadas que Apexx debe igualar o superar

Lo que esta cuenta demuestra que VTEX resuelve y que debemos tener desde el diseño:

1. **Multi-moneda y multi-país por canal de venta** (MXN, IDR, VND, THB en una cuenta).
2. **Precios escalonados por cantidad** (`fixedPrices` con `minQuantity` 10 y 15) y por canal.
3. **Precio de lista contra precio de venta** con descuento mostrado.
4. **Inventario con reservas** (`reservedQuantity`) por bodega.
5. **Retiro en tienda** (pickup points ligados a bodegas).
6. **Políticas de envío** con peso cúbico, dimensiones máximas, horarios, feriados y entrega programada.
7. **Marketplace**: pedidos con origen `Marketplace`, sellers registrados y comisiones.
8. **Gift cards** con proveedor propio, preautorización y cancelación.
9. **Master Data**: entidades propias con esquemas JSON (v2) y enmascarado de PII (`X-VTEX-MD-PII-Masked`).
10. **Configuración de checkout**: reCAPTCHA, múltiples cupones, valores mínimos de compra, entregas múltiples, sellers white-label.
11. **Workflow de pedido** con estados, cancelación, edición e historial de cambios (`changesAttachment`).
12. **Suscripciones** (planes) e **impuestos** como calculadoras configurables.

## 6. Decisiones de diseño para Apexx que salen de esta auditoría

| # | Decisión | Resuelve |
| --- | --- | --- |
| D1 | Inventario como fuente única con eventos; disponibilidad con antigüedad máxima de 2 s | H1, H9 |
| D2 | Contrato único OpenAPI 3.1 con linter en CI (nombres, montos, errores) | H4, H5 |
| D3 | Montos en unidades menores + moneda en toda la plataforma | H5 |
| D4 | Paginación por cursor, exportación NDJSON y webhooks de cambio | H3 |
| D5 | Proyección de campos y compresión obligatoria; presupuesto de 2 KB por producto en listados | H2 |
| D6 | Búsqueda en memoria con p99 < 30 ms y autocompletar tolerante a errores | H6 |
| D7 | Simulación con totales finales explícitos y reglas visibles | H7 |
| D8 | Pedido = núcleo + extensiones + log de eventos | H8 |
| D9 | Invalidación por eventos, `ETag` universal y cabeceras de cuota | H9 |
| D10 | Migraciones transaccionales con estado y reversión | H10 |

## 7. Pendiente

- Repetir las latencias desde una región de LatAm y en hora pico.
- Auditar el gateway de pagos (`elpaps.vtexpayments.com.br`), que no está en la lista de dominios permitidos.
- Crear una promoción y un cupón de prueba en un workspace de desarrollo para auditar el motor de promociones, hoy vacío en la cuenta.
- Pruebas de rate limit controladas, solo con autorización explícita y fuera de producción.
