# Auditoría de indexación — VTEX (cuenta `elpaps`)

> Fecha: 2026-11-10 · Alcance: **solo lectura**. No se modificó catálogo, precio, inventario ni configuración.
> La prueba activa "cambio → tiempo hasta verse" **no se ejecutó**, porque requiere escrituras que el sistema de permisos no autorizó. Abajo está el protocolo reversible para correrla cuando el dueño lo apruebe (sección 7).
> Sin credenciales ni datos personales. Datos crudos fuera del repositorio.

## 1. Resumen

La indexación es el cuello de botella estructural de VTEX, como en Magento: cada dato existe en varias copias, y cada copia se actualiza por su propio proceso asíncrono. Lo medimos así:

| Evidencia | Valor medido |
| --- | --- |
| Tiempo promedio de indexación que reporta el propio admin (Intelligent Search → Historial de indexación) | **397 155 ms = 6 min 37 s** (la UI mostró 6 min 4 s una hora antes) |
| Duración por producto en una ráfaga de unos 150 productos encolados | **p50 ≈ 226 s, máx. 1 265 s (21 min)**, 19 productos observados |
| Productos en catálogo frente a productos encontrables en búsqueda | **18 506 frente a 18 484**: 22 productos no aparecen en búsqueda |
| Estado del indexador | 18 502 actualizados · 3 en proceso · **1 en error** |
| Índices distintos para el mismo producto | **2 indexadores** (Solr legacy de `catalog_system` e indexador de Intelligent Search) + cachés de 5 min y 6 h |
| Política de envío "Transportadora" | `lastIndexedAt` **2021-05-19** (5 años); otras 3 políticas nunca indexadas, una con error de validación en portugués |
| `lastmod` del sitemap | La fecha de hoy en **los 1 000 productos** del archivo: indica cuándo se generó, no cuándo cambió el producto |

## 2. Método

1. **Índice legacy (Solr):** `GET /api/catalog_system/pvt/products/GetIndexedInfo/{productId}` para 150 productos. La respuesta es un documento Solr literal (`zkConnected`, `QTime`, `_version_`): VTEX expone su motor de índice.
2. **Muestreo cruzado pasivo:** 372 SKUs (150 productos, tomados de 10 páginas de la búsqueda entre las posiciones 0 y 2 500). Para cada SKU se leyó el mismo dato en 6 superficies: inventario (verdad: `onHand − reserved`), pricing calculado (canal 1), búsqueda legacy (`catalog_system/pub`), índice Solr, Intelligent Search y simulación de checkout (pidiendo 50 unidades).
3. **Deriva:** la misma muestra se repitió 3 veces (19:13, 19:24 y 19:34 UTC).
4. **Historial de indexación:** la página del admin y su API GraphQL (`getIndexingHistoryList`, `indexingHistoryAverageIndexingTime`, `indexingHistoryStatusShare`, `countProductsOnSearch`), todo de solo lectura.
5. **Cabeceras de caché** de cada superficie y **frescura del sitemap**.
6. **Experimento de atribución:** qué tipo de lectura provoca que un producto entre a la cola de indexación (sección 5).

## 3. Mapa de copias del mismo dato

| Superficie | Backend (cabecera `x-vtex-janus-router-backend-app`) | Caché | Qué guarda de stock | Qué guarda de precio |
| --- | --- | --- | --- | --- |
| Inventario (verdad) | `logist-orch` | ninguna | Cantidad real por bodega | — |
| Pricing (verdad) | `pricing` | sin `cache-control` | — | Precio por SKU y canal |
| Catálogo (escritura) | `ctgcp-api` | — | — | — |
| Índice legacy Solr | `catalogapi` | — | **Cubeta de potencia de 2** por producto (8, 16, 512) | Mejor precio del producto, redondeado a 1 decimal |
| Búsqueda legacy pública | `portal-search` | **`s-maxage=300`** (5 min; se vio `age: 119`) | 100, 10 o 99 999: no es la cantidad real | Precio exacto |
| Intelligent Search | `io-admins` / IS | gestionada por IS | **Siempre 10 000** (valor fijo) | Precio exacto |
| Simulación de checkout | checkout | — | **Cantidad real** (coincide en 372/372) | Precio exacto |
| Sitemap | `sitemap` | **`s-maxage=21600`** (6 h; se vio `age: 2286`) | — | — |

**Lectura:** el único lugar donde el comprador ve la disponibilidad real es el checkout. El listado, la búsqueda y el índice muestran sustitutos (100, 10 000, cubetas), así que la tienda no puede decir "quedan 3" con verdad, y un agotamiento solo aparece en el listado cuando el indexador procesa el producto.

## 4. Resultados del muestreo cruzado (372 SKUs)

| Comparación | Coincide | No coincide | Detalle |
| --- | ---: | ---: | --- |
| Simulación de checkout vs. inventario | **372** | 0 | El checkout lee la verdad |
| Búsqueda legacy (cantidad) vs. inventario | 46 | **326** | Muestra 100 cuando hay 10 a 20 unidades |
| Intelligent Search (cantidad) vs. inventario | 0 | **372** | Siempre 10 000 |
| Índice Solr (cantidad) vs. suma real del producto | 4 | **145 de 150 productos** | Cubetas de potencia de 2: 14 reales → 8; 91 a 212 reales → 16; 997 reales → 512 |
| Disponible sí/no, en todas las superficies | 372 | 0 | No hubo agotados en la muestra |
| Precio (búsqueda, IS, checkout) vs. pricing | 371 | 0 | Coinciden |
| Precio del índice Solr vs. mejor precio del producto | 149 | 0 | 90 con diferencia solo por redondeo a 1 decimal |

**Deriva entre las 3 corridas (21 min):** 0 cambios en todas las superficies. La tienda demo no tiene actividad, así que el muestreo pasivo **no permite medir el retraso de propagación**. Para eso hace falta la prueba activa (sección 7).

**Antigüedad de los documentos Solr:** `commercialOfferLastUpdateUtc` p50 de 14,1 días, p90 de 21,9 días y máximo de 28,8 días. En el 100% de los casos `catalogLastUpdateUtc == commercialOfferLastUpdateUtc`: **cualquier cambio reescribe el documento completo del producto**, aunque solo cambie el stock de un SKU. Ese es el patrón que vuelve caro un cambio masivo, igual que el *full reindex* de Magento.

## 5. El indexador en acción (Intelligent Search)

Página del admin "Historial de indexación" (`/admin/search/indexing-history`):

| Producto | Inicio (UTC) | Última actualización | Duración | Estado |
| --- | --- | --- | ---: | --- |
| 2734 | 19:13:06 | 19:30:56 | **1 070 s** | Updated |
| 776 | 19:13:08 | 19:22:04 | 536 s | Updated |
| 777 | 19:13:06 | 19:19:37 | 391 s | Updated |
| 2739 | 19:13:10 | 19:17:20 | 251 s | Updated |
| 766 | 19:24:09 | 19:27:57 | 228 s | Updated |
| 527 | 19:24:08 | 19:27:48 | 220 s | Updated |
| 498 | 19:24:08 | 19:36:58 | **770 s** | Updated (estuvo en *Updating · success* 9 min) |
| 5175 | 19:13:06 | 19:34:11 | **1 265 s** | Updated |
| 817 | 19:34:01 | 19:37:49 | 228 s | Updated |
| 944 | 19:34:01 | 19:37:47 | 226 s | Updated |
| 500 | 19:33:58 | 19:37:44 | 226 s | Updated |
| 793 | 19:34:01 | 19:37:30 | 209 s | **Updated · conflict** |

- Las horas de inicio (19:13:06, 19:24:08 y 19:33:58) **coinciden al segundo con el arranque de nuestras 3 corridas de muestreo de solo lectura**. Nosotros no escribimos nada: alguna lectura hace que VTEX encole el producto para reindexarlo. Lo más probable es una autocorrección del indexador cuando detecta diferencias. Ver el resultado del experimento de atribución en la sección 5.1.
- Con solo unos 150 productos encolados a la vez, cada producto tardó **entre 3,3 y 21 minutos** en indexarse, y uno terminó con estado `conflict`. Extrapolado de forma conservadora, un cambio masivo sobre los 18 506 productos (por ejemplo, un cambio de precios de Black Friday) deja la tienda mostrando datos viejos durante horas. Es una extrapolación, no una medición.
- El índice Solr legacy (`GetIndexedInfo`) **no cambió** durante esas reindexaciones (0 de 150 documentos reescritos entre la corrida 1 y la 3). Los dos indexadores corren por separado y pueden divergir entre sí.
- Ráfagas de reindexación también a las 17:47 y 18:42 UTC (documentos Solr con `catalogLastUpdateUtc` de hoy). La de 18:42 coincide con el benchmark de la auditoría de backend, también de solo lectura.

### 5.1 Experimento de atribución

Se leyó un producto distinto con cada tipo de llamada y se observó durante 2 minutos si entraba al historial de indexación.

| Lectura aislada (1 producto distinto cada una) | ¿Entró a la cola en 2 min? (y en una revisión 5 min después) |
| --- | --- |
| `GetIndexedInfo` | No |
| Búsqueda legacy `fq=productId` | No |
| Intelligent Search `product.id` | No |
| Inventario + pricing de sus SKUs | No |
| Simulación de checkout (50 uds por SKU) | No |

**Conclusión:** ninguna lectura aislada encola el producto. Pero las 3 corridas completas (unas 1 900 lecturas en 50 s cada una) coincidieron al segundo con 3 ráfagas de reindexación. El disparador depende del volumen o de la combinación de lecturas, y **no queda atribuido**: es una correlación fuerte (3 de 3), no una causa demostrada. En la práctica, una integración que solo lee (un ERP o un comparador de precios) puede terminar generando carga de reindexación en la tienda.

## 6. Por qué se traba con cambios masivos

Arquitectura inferida a partir de la evidencia, análoga a los *indexers* de Magento:

1. **Escritura y lectura separadas por colas:** el catálogo, el inventario y el pricing escriben en sus propios servicios. Un proceso asíncrono reconstruye **documentos de producto completos** para cada índice (Solr legacy e Intelligent Search).
2. **Documento completo por cambio:** cambiar 1 SKU reescribe el producto entero, como muestran los timestamps iguales de catálogo y oferta. Con 10 000 cambios de precio se encolan 10 000 reconstrucciones completas.
3. **Varias cachés encima:** la búsqueda legacy cachea 5 min y el sitemap 6 h. El cambio no se ve hasta que se cumple el tiempo, aunque el índice ya se haya actualizado.
4. **Sin garantía de frescura ni visibilidad para el comerciante:** el promedio de 6 min 37 s es un promedio de todo el índice. No hay un objetivo por superficie y el comerciante no ve cuánto atraso tiene su tienda en este momento.
5. **Valores sustitutos:** para que el índice no cambie con cada venta, guarda stock agrupado (cubetas) o fijo (10 000). Eso evita reindexar en cada venta, pero a cambio la tienda no muestra el stock real.

## 7. Prueba activa pendiente: protocolo reversible

Para ejecutarla cuando el dueño apruebe escrituras en la tienda demo:

1. Leer y guardar el valor original exacto: inventario del SKU 3512 en la bodega `1_1`, precio base del SKU 3512 y nombre del producto 2668.
2. Hacer **un** cambio a la vez: stock +1, precio +1,00 o el nombre con un sufijo " (idx test)".
3. Cada 2 s (cada 10 s después del minuto 2), hasta 15 min, consultar las 7 superficies de la sección 3 y anotar cuándo refleja el cambio cada una.
4. **Revertir** al valor original con la misma API y medir de nuevo la propagación de la reversión.
5. Verificar que el estado final sea idéntico byte a byte al original.

Resultado esperado: una tabla "superficie → retraso p50/máx. del cambio → retraso de la reversión".

## 8. Diseño de Apexx que elimina el indexador

| # | Decisión | Qué resuelve |
| --- | --- | --- |
| I1 | **Proyecciones escritas en la misma transacción** (outbox transaccional): cada escritura en Postgres registra un evento en la misma transacción, y el proyector actualiza las vistas de lectura en memoria **solo con el campo que cambió** | Sin "documento completo por cambio"; sin colas de reconstrucción |
| I2 | **Objetivo de frescura por superficie < 2 s** (catálogo, búsqueda, storefront, checkout), medido y expuesto en `GET /v1/system/freshness` | El promedio de 6 min 37 s pasa a ser un objetivo medible por tienda |
| I3 | **Documentos versionados** (`version` monotónica por producto); las proyecciones descartan versiones viejas y reintentar es idempotente | Sin divergencia entre índices |
| I4 | **Stock real en el listado**, con disponibilidad sí/no y "quedan N" desde la misma fuente que el checkout | Termina con 100 / 10 000 / cubetas |
| I5 | **Sin reindexación completa:** el relleno inicial (importación, migración) se hace por *streaming* en lotes con contrapresión, sin bloquear las escrituras en línea | Cambios masivos sin trabar la tienda |
| I6 | **Prioridad y contrapresión:** stock y precio van primero; textos y SEO después; las cuotas son por tienda, así que una tienda que importa 1 millón de productos no frena a las demás | Aislamiento entre tiendas |
| I7 | **Observabilidad del atraso por tienda:** cola pendiente, edad del evento más viejo y p99 del retraso en métricas y en el admin | El comerciante ve si su tienda está al día |
| I8 | **Invalidación por evento en el CDN** (surrogate keys por producto y por tienda), no por tiempo | Sin cachés de 5 min ni sitemaps de 6 h |
| I9 | **Sitemap con `lastmod` real** (fecha de la última versión del producto) | SEO correcto |
| I10 | **Las lecturas nunca disparan escrituras:** si una proyección diverge se registra una alerta, pero no se reindexa en silencio | Sin carga oculta |

### Tabla de objetivos

| Superficie | Evidencia de desfase en VTEX | Objetivo Apexx |
| --- | --- | --- |
| Búsqueda / listado | Stock sustituto (100, 10 000, cubetas); caché de 5 min; 22 productos no encontrables | Stock real; < 2 s desde la escritura; 100% de los productos activos encontrables |
| Índice de catálogo | Documento completo por cambio; promedio 6 min 37 s; hasta 18 min por producto | Actualización del campo cambiado; p99 < 2 s |
| Ficha / storefront | Depende del índice y de la caché | Invalidación por evento; < 2 s |
| Checkout | Correcto (lee la verdad) | Igual, misma fuente que el listado |
| Logística | Políticas con `lastIndexedAt` de 2021 o nulo; error en portugués con estado 1 | Cálculo directo desde la configuración, sin índice; errores en el idioma de la tienda |
| Sitemap | `lastmod` = fecha de generación; caché de 6 h | `lastmod` real; regeneración por evento |
