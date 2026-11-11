# Auditoría de cierre

Fecha: 2026-11-11. Alcance: todo lo construido en las sub-fases A a L
([detalle](../fases/fase-7-paneles-A-L.md)). Cada auditoría quedó como prueba
o guion repetible; los comandos están al final.

## Resumen

| Auditoría | Resultado |
| --- | --- |
| Acceso a rutas | 295 rutas `/v1`: ninguna responde sin credenciales (salvo 4 públicas a propósito); la llave pública de tienda no entra a ninguna de las 255 rutas de gestión |
| Aislamiento entre tiendas | 107 tablas con datos de tienda, todas con seguridad por fila forzada; el rol de la aplicación no puede saltársela ni leer llaves o sesiones |
| Reversión | Pedido con cupón, tarjeta de regalo, tarjeta bancaria y producto de vendedor, enviado en parte y cancelado: dinero, saldo, comisión, uso del cupón y existencias vuelven a su punto; ninguna cuenta contable queda con saldo |
| Caídas | `kill -9` en plena ráfaga de compras y base de datos caída: cero cobros dobles, cero reservas colgadas, contabilidad cuadrada; el servidor vuelve en 1,9 s |
| Velocidad | Tienda: mediana 4 a 12 ms, percentil 95 menor a 27 ms (inicio, catálogo, búsqueda, ficha, sugerencias, simular carrito) |
| Uso humano, panel | 62 pantallas en escritorio y en celular: sin errores, sin pantallas vacías, sin desborde horizontal |
| Uso humano, comprador | Compra completa en celular con errores a propósito (cupón inexistente, datos faltantes, tarjeta rechazada), cuenta con código, pedido visible |
| Uso humano, roles | Dueño atiende el pedido (guía y reembolso parcial); editor de catálogo solo ve lo suyo; vendedor solo ve su portal |
| Pruebas automáticas | Suite completa de Go y del panel en verde |

## Defectos que encontraron las auditorías (todos corregidos)

| Auditoría | Defecto |
| --- | --- |
| Acceso | La llave pública de tienda podía leer 12 rutas de gestión: cola de opiniones, favoritos, reglas y términos de búsqueda, listas de precios e historial, existencias por ubicación |
| Reversión | Cancelar un pedido devolvía al inventario también las piezas ya enviadas |
| Caídas | Una compra interrumpida por un corte quedaba "en proceso" para siempre al reintentar |
| Caídas | Con la base caída, comprar respondía "error interno" en vez de decir que no se cobró nada |
| Caídas | Un error de base de datos al resolver la cuenta de pagos se reportaba (y se guardaba) como "la tienda no conectó Stripe" |
| Uso humano | En celular, el icono de favoritos se salía del encabezado |
| Uso humano | En Equipo, la persona de un vendedor aparecía como "Sin acceso" |

Los defectos encontrados durante la construcción están en la ficha de la fase.

## Cómo se comporta ante una caída

- **Se cae el proceso**: las compras en vuelo quedan como pedidos pendientes
  con su reserva de inventario. Al volver, el conciliador pregunta al
  proveedor de pagos por cada uno y lo cierra como pagado o cancelado. El
  comprador que reintenta con la misma compra recibe: "se está procesando"
  mientras pueda seguir viva, el pedido ya pagado si lo hubo, o una compra
  nueva si no llegó a crearse nada.
- **Se cae la base de datos**: la tienda ya visitada sigue navegable (inicio,
  catálogo, búsqueda, ficha, carrito y simulación salen de memoria). Comprar
  responde 503 con "no se guardó ni se cobró nada". Al volver la base, el
  servicio queda listo en 1 segundo.
- Una tienda recién reiniciada que nadie ha visitado no puede armar su página
  sin base de datos: responde 503 hasta que la base vuelve.

## Lo que esta auditoría no cubre

Hay que decirlo claro, porque son condiciones para salir a producción:

- **Stripe real**: todo el dinero se probó con el proveedor de prueba interno.
  Falta una corrida con una cuenta de Stripe en modo test (cobro, 3-D Secure,
  meses sin intereses, tarjeta guardada para suscripciones, reembolsos, facturas).
- **Carga real**: las mediciones son de una sola máquina y pocas conexiones.
  Falta una prueba de carga sostenida y de varios nodos.
- **Google Cloud en alta disponibilidad**: los guiones existen y se validaron
  en local; no se han ejecutado en un proyecto real.
- **Prueba de penetración externa** y revisión legal (aviso de privacidad, CFDI).
- **CFDI**: la factura es la de Stripe; el timbrado fiscal mexicano no está integrado.
- **Idiomas y varias tiendas por cuenta**: no incluidos (ver plan de fases).
- **Comparación activa con VTEX** de tiempos de indexación: requiere tu
  autorización porque escribe en la cuenta de VTEX.

## Repetir las auditorías

```sh
# Acceso, aislamiento, reversión y recuperación (pruebas permanentes)
APEXX_TEST_ADMIN_URL=postgres://postgres@127.0.0.1:5432/postgres \
  go test ./internal/api/ -run 'TestEveryRoute|TestEveryTenantTable|TestFullOrderReversion|TestInterruptedCheckout' -v

# Con el servidor y la tienda de QA en marcha (docs/qa.md)
node qa/panel-crawl.js      http://127.0.0.1:8097 <tienda> <carpeta-de-capturas>
node qa/human-journey.js    http://127.0.0.1:8097 <tienda> <carpeta-de-capturas>
node qa/storefront-speed.mjs http://127.0.0.1:8097 <tienda> <handle-de-producto>
psql apexx -v tenant=<tienda> -At -F ': ' -f qa/invariants.sql   # todo debe dar 0
```
