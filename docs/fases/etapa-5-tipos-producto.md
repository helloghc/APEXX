# Etapa 5 — Tipos de producto (noviembre 2026)

Estado: **hecha**. Cada función con prueba de integración, recorrido en navegador (panel en escritorio, tienda en celular) y medición.

| Función | Dónde | Prueba |
|---|---|---|
| Kits con existencias reales por componente | Producto › Kit; ficha «Este kit incluye» | `TestBundlesStockFromComponents` |
| Productos digitales con enlace firmado, límite y vencimiento | Producto › Archivos digitales; gracias, cuenta y correo; Pedido › Descargas | `TestDigitalDownloads` |
| Preventa con anticipo y cobro del saldo | Producto › Preventa; ficha, carrito y checkout | `TestPreordersWithDeposit` |
| Personalización con precio (texto, lista, casilla) | Producto › Personalización; ficha, carrito, pedido | `TestProductCustomizations` |
| Comparador de productos | Botón Comparar en tarjetas y ficha; `/comparar` | `TestCompareProducts` |

## Cómo funciona

- **Kits.** `bundle_components` (con RLS) define el kit. El kit no tiene existencias: reservar un kit reserva cada componente con bloqueo de fila, la disponibilidad es el mínimo de kits que forman los componentes, y cada línea de pedido congela la composición con la que se vendió, así que cancelar, editar o devolver regresa exactamente esas piezas aunque el kit cambie después. No se permiten kits dentro de kits ni existencias directas en un kit.
- **Digitales.** Los archivos (hasta 100 MB) viven en el almacenamiento de la tienda; la tabla `download_grants` da un permiso por pedido y archivo al pagarse y lo revoca al cancelar, reembolsar o quitar el archivo. El enlace lleva HMAC por tienda; el contador sube con bloqueo de fila (seis clics simultáneos con dos descargas restantes: exactamente dos pasan); reanudar con `Range` no gasta descargas. Un carrito sin peso no cobra envío ni pide dirección y el pedido solo digital queda surtido.
- **Preventa.** `preorders` guarda fecha, anticipo (5–100 %), límite y vendidas. La preventa aparta de su propia bolsa (ubicación virtual `_preorder`) sin tocar el inventario; el checkout cobra solo el anticipo y el resto queda en `orders.deferred` y en la cuenta `customer_receivable` del libro. Un clic manda el enlace de pago a todos los que deben. Cancelar devuelve solo el anticipo y libera la pieza.
- **Personalización.** Campos por producto (texto, lista, casilla) con suma al precio; el carrito valida y suma, en mercados convertidos convierte la suma, y cada combinación distinta es su propia línea (clave `ref`). El pedido guarda los valores en la línea y en el título, que es lo que ven correos, hoja de surtido y almacén.
- **Comparador.** Hasta cuatro productos elegidos en el navegador; la página muestra precio, disponibilidad, marca y especificaciones, marca las filas iguales y puede ocultarlas.

## Defectos que encontraron las auditorías (todos corregidos)

- La llave pública de la tienda podía listar los archivos digitales (auditoría de rutas).
- El checkout de un carrito solo digital pedía dirección y fallaba («Falta tu dirección»).
- El navegador guardaba el archivo como «download» al llevar `filename*`; ahora el nombre va en ASCII.
- El botón de pago de una preventa mostraba el total y no el anticipo; las tarjetas de regalo podían pagar la parte diferida.
- Las opciones de entrega prometían llegada en días para un producto que se envía en semanas; ahora muestran la fecha de la preventa.

## Velocidad medida (servidor de desarrollo, 20 repeticiones)

- Tienda: ficha de kit 2.5 ms, ficha con preventa y personalización 2.4 ms, ficha digital 2.0 ms, comparar 3 productos 1.9 ms; carrito con kit 1.7 ms, con preventa y personalización 1.4 ms, mixto de 4 líneas 1.3 ms (mediana; peor p95 4.4 ms).
- Panel: kits, archivos digitales, preventas y personalización entre 1.5 y 2.5 ms (mediana; peor p95 4.8 ms).

## Pendiente

- Archivo subido por el comprador en la personalización.
- Cargo automático del saldo de preventa con la tarjeta guardada (hoy, enlace de pago).
