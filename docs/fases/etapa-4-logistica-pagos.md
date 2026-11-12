# Etapa 4 — Logística y pagos conectados (noviembre 2026)

Estado: **hecha**. Cada función con prueba de integración, recorrido en navegador (panel en escritorio, tienda en celular) y medición.

| Función | Dónde | Prueba |
|---|---|---|
| Cargos extra por pedido (fijo o %, por canal, entrega, grupo, monto) | Configuración › Cargos extra | `TestOrderFeesAndDuties`, `TestFeesFor` |
| Aranceles por mercado (código arancelario, origen, tasa por código, monto exento) | Producto › Aduanas; Canales | `TestOrderFeesAndDuties`, `TestDutiesFor` |
| Edición de pedidos con vista previa y cambio de producto en devoluciones | Pedido › Editar productos / Devolución | `TestOrderEditAndExchange` |
| Paqueterías conectadas: tarifa en vivo, guía, rastreo, cancelación | Envíos › Paqueterías y opciones | `TestCarriersLabelsTrackingAndReturns`, `TestCarrierAppLabelReversal`, paquete `carriers` |
| Devoluciones del cliente con guía prepagada | Configuración › Devoluciones; Mi cuenta | idem |
| OXXO y SPEI | Pagos; checkout; Mi cuenta | `TestVoucherPayments` |
| Tarjetas guardadas y compra en un paso | Checkout; Mi cuenta › Mis tarjetas; ficha | `TestSavedCards` |
| Servicio de impuestos externo con respaldo | Configuración › Impuestos | `TestExternalTaxService` |
| Almacenes de terceros (3PL) | Ubicaciones; pedido | `TestThirdPartyWarehouse` |

## Defectos que encontraron las auditorías (todos corregidos)

- Cargo fijo de 35 MXN cobrado como 35 USD en el mercado de dólares (prueba humana).
- La edición de pedidos y los cambios de producto respondían 500 siempre (consulta sin tipos; no tenían prueba).
- La edición de pedidos regalaba los cargos extra y el impuesto agregado.
- El endpoint de prueba de OXXO marcaba pagada la referencia de otra tienda antes de validarla (aislamiento).
- El checkout pedía de nuevo el correo a un cliente con sesión.
- La compra en un paso fallaba en tiendas que exigen aceptar términos.
- La casilla de OXXO/SPEI no cambiaba hasta que respondía el servidor.
- **Velocidad del panel**: cada cambio de pantalla tardaba ~800 ms por la espera mínima de React al descargar una pantalla; ahora 10–100 ms (transición + descarga previa).

## Velocidad medida (servidor de desarrollo)

- Tienda: ficha 4.5 ms, carrito con envío e impuestos 4.3 ms, búsqueda 5.5 ms, catálogo 9.5 ms (mediana).
- Panel: 86 lecturas del API, mediana 1.6 ms, la peor 10.5 ms (p95); pantallas visibles en 10–105 ms.
- Tarifa en vivo y servicio de impuestos: nunca más de 800 ms aunque el proveedor se cuelgue (probado).

## Fuera de este entorno

Probar con cuentas reales de paquetería (o agregador), Stripe en vivo con OXXO y transferencias, y un servicio de impuestos real.
