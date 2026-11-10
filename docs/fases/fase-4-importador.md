# Fase 4 · Importador VTEX → Apexx

> Estado: **funcional y probado contra la cuenta real `elpaps`** (300 productos importados, fidelidad 50/50).
> VTEX se usa **solo en lectura**: el importador hace únicamente peticiones `GET`.

## 1. Qué hace

Recorre **todo** el catálogo de una cuenta VTEX y lo escribe en una tienda Apexx:

- productos y variantes (SKUs),
- precios por canal, incluidos los precios escalonados por cantidad,
- stock por bodega.

Escribe con las mismas funciones que usa la API de Apexx. Por eso cada cambio importado:

- deja su evento en el outbox, y el proyector lo publica en milisegundos;
- queda en el registro de auditoría encadenado, con el autor `importer:vtex`;
- respeta el aislamiento por tienda (RLS).

**Sin el techo de 2 500 de VTEX.** La búsqueda de VTEX corta en 2 500 resultados. El importador pagina con `GetProductAndSkuIds` de 250 en 250 hasta el total real de la cuenta, que en `elpaps` son 18 506 productos.

## 2. Cómo se ejecuta

```bash
export VTEX_APP_KEY=…      # solo por variables de entorno; nunca por flags ni en logs
export VTEX_APP_TOKEN=…
# más las variables de base de datos de Apexx (DATABASE_URL, SYSTEM_DATABASE_URL, APEXX_MASTER_KEY)

apexx tenant -id elpaps -name "elpaps" -owner-email … -owner-password …
apexx import -tenant elpaps -vtex-account elpaps -limit 300 -dry-run   # mapear sin escribir
apexx import -tenant elpaps -vtex-account elpaps                        # catálogo completo
```

| Flag | Valor por defecto | Descripción |
| --- | --- | --- |
| `-tenant` | (obligatorio) | Tienda Apexx de destino |
| `-vtex-account` | (obligatorio) | Cuenta VTEX de origen |
| `-limit` | `0` (todo) | Importar solo los primeros N productos |
| `-workers` | `8` | Productos procesados en paralelo |
| `-rps` | `10` | Máximo de peticiones por segundo a VTEX |
| `-dry-run` | `false` | Leer y mapear sin escribir |
| `-channel-map` | `1=mx:MXN` | Política comercial VTEX → canal y moneda Apexx (`1=mx:MXN,2=id:IDR`) |

Al terminar imprime un reporte JSON con productos, SKUs, precios, stock, errores por tipo, peticiones a VTEX, reintentos, duración y productos por segundo.

**Robustez:**
- Reintenta con backoff exponencial ante `429`, `5xx` y errores de red, y respeta `Retry-After`.
- Se detiene limpio con Ctrl+C.
- Un producto con error no detiene la corrida: queda contado en `productsWithErrors`.

## 3. Mapeo VTEX → Apexx

| VTEX | Apexx | Regla |
| --- | --- | --- |
| `Product.Id` | `product.id` | `vtex-{Id}`: estable, así reimportar actualiza en lugar de duplicar |
| `Product.LinkId` | `product.handle` | En minúsculas y con guiones. Si ya existe, se agrega `-{Id}`. Si está vacío, se usa `p-{Id}` |
| `Product.Name` (o `Title`) | `product.title` | |
| `Product.Description` | `product.description` | Se copia tal cual (HTML) |
| `Product.IsActive` | `product.status` | `true` → `active`, `false` → `draft` |
| `Product.RefId` | `attributes.refId` | También se guarda `attributes.vtexProductId` |
| `Sku.BrandName` | `product.brand` | |
| `Sku.ProductCategories` | `product.categories` | Nombres de la ruta de categorías, sin repetir |
| `Sku.Id` | `variant.sku` | |
| `Sku.SkuName` | `variant.title` | |
| `Sku.AlternateIds.Ean` | `variant.barcode` | |
| `Sku.Dimension.weight` | `variant.weightGrams` | Se toma en gramos, como lo configura el catálogo de `elpaps` |
| `Sku.SkuSpecifications` | `variant.options` | `{nombre: primer valor}` |
| `Sku.IsActive = false` | `variant.options.vtexActive = "false"` | |
| `pricing.basePrice` / `listPrice` | tier `minQty=1` + `compareAt` | Decimales × 100 = centavos. `compareAt` solo si el precio de lista es mayor |
| `pricing.fixedPrices[tradePolicy]` | tiers por `minQuantity` | Tienen prioridad sobre `basePrice` en ese canal. **También vienen en decimales** |
| `inventory.balance[].totalQuantity` | `stock_levels.on_hand` por bodega | La bodega VTEX (`1_1`) pasa a ser la ubicación Apexx |
| `hasUnlimitedQuantity = true` | `on_hand = 1 000 000` | |
| `reservedQuantity` | no se importa | Las reservas de VTEX pertenecen a carritos de VTEX |

**Unidades de precio, verificadas en la cuenta real:**
- La API de pricing devuelve **decimales** en todos los campos: `basePrice 1684.38` y `fixedPrices.value 1674000` (IDR).
- El checkout de VTEX devuelve **centavos** (`168438`, `167400000`).
- El importador convierte con `round(valor × 100)`, que coincide exactamente con el checkout (ver §4).

## 4. Resultados de la corrida real (`elpaps`, 10 nov 2026)

| Medición | Resultado |
| --- | --- |
| Productos importados | **300** (límite de la prueba) de 18 506 en la cuenta |
| SKUs / juegos de precios / filas de stock | 389 / 388 / 389 |
| Productos con error | **0** |
| Avisos | 1 SKU sin precio en VTEX (`price_not_found`): se importa sin precio |
| Peticiones a VTEX / reintentos | 1 469 / 0 |
| Duración | 2 min 27 s, a **10 peticiones/s** (2 productos/s; ~4,9 peticiones por producto) |
| Reimportar 50 productos | Sin duplicados: los conteos no cambian y `version` sube de 1 a 2 |
| Auditoría | 1 399 registros con autor `importer:vtex`; 1 400 eventos en el outbox |

### Fidelidad (50 SKUs al azar, comparados en vivo contra VTEX)

| Dato | Coincidencias |
| --- | --- |
| Stock (`on_hand` Apexx vs `totalQuantity` VTEX) | **50 / 50** |
| Precio unitario Apexx vs API de pricing VTEX | **50 / 50** |
| Precio unitario Apexx vs **precio final del checkout VTEX** (`sc=1`) | **50 / 50** |

### Catálogo completo

No se ejecutó en esta sesión. La velocidad depende del límite de peticiones por segundo:

| `-rps` | Duración estimada para 18 506 productos (~90 000 peticiones) |
| --- | --- |
| 10 (valor por defecto, prudente con una tienda en producción) | ~2 h 30 min |
| 40 | ~38 min |

Se recomienda correrlo de noche con `-rps 10` a `20`, o pedir a VTEX un límite mayor para la ventana de migración.

## 5. Pendiente (próximas iteraciones)

| Falta | Plan |
| --- | --- |
| **Especificaciones de producto** (`/products/{id}/specification`) | Mapear a `attributes`; las de SKU ya entran como `options` |
| **Imágenes** | Descargar desde `vtexassets` al almacenamiento de medios de Apexx (imgproxy) y referenciarlas en el producto |
| **Árbol de categorías** | Hoy solo se guardan los nombres en `categories`. Falta importar el árbol (`category/tree`) como colecciones navegables |
| **Promociones** (`rnb`) | Mapear los tipos equivalentes (porcentaje, compra X paga Y) al motor de Apexx |
| **Clientes y pedidos históricos** | Requiere cifrado por tienda y consentimiento; se diseñará aparte |
| **Sincronización continua** | Mientras dure la migración: reimportar periódicamente solo lo que cambió, a partir de las notificaciones de catálogo de VTEX |
| **Reservas** | No se migran a propósito; Apexx empieza sin carritos abiertos |
| **Velocidad** | Leer el precio y el stock por lotes cuando VTEX lo permita, para bajar de ~4,9 peticiones por producto |
