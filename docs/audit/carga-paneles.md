# Carga de cada vista del panel

Fecha: 12 de noviembre de 2026. Herramienta: `tools/qa/carga/medir.js` (ver `tools/qa/README.md`).

## Cómo se mide

El medidor abre el panel en Chromium y recorre cada vista que se alcanza desde el menú: pestañas, subpáginas y una ficha por lista. En la tienda de QA son 242 vistas.

Por cada vista se cronometra desde el clic hasta que queda **completa**:
- no hay peticiones `/v1` en curso;
- no hay esqueletos de carga;
- no aparece «Cargando…».

Se registran también las llamadas a la API y el JavaScript descargado.

Hay dos condiciones de red:

| Condición | Qué simula |
|---|---|
| Local | Servidor en la misma máquina: mide solo el trabajo del servidor y del navegador |
| Red real | 80 ms de ida y vuelta, 20 Mbps, HTTPS con HTTP/2 (como detrás de Caddy) |

Con red real, cada petición que espera a otra cuesta un viaje completo. Ahí se ven las cascadas que en local no se notan.

## Resultados: tienda de QA (120 productos)

| Medida | Antes | Después |
|---|---|---|
| Local: vistas por encima de 200 ms | 5 (máx. 492 ms) | **0** (máx. 111 ms) |
| Red real: mediana por vista | 194 ms | 180 ms |
| Red real: vistas por encima de 200 ms | 80 | **5** |
| Red real: vista más lenta | 400 ms | 240 ms |
| Configuración (red real) | 400 ms | **109 ms** |
| Ficha de pedido, cliente o empresa, desde la segunda (red real) | ~190 ms | **~100 ms** |
| Ficha de producto con el puntero 150 ms sobre la fila antes del clic | ~200 ms | **110 ms** (20 ms si la pausa es de 300 ms) |
| Archivo principal del panel | 200 KB por petición, 7,8 ms de CPU | **158 KB**, 0,6 ms |

Las vistas de una sola llamada tardan un viaje de red (~100 ms con 80 ms de ida y vuelta), que es el mínimo posible sin datos en caché.

## Resultados: tienda con 50 000 productos

Las vistas lentas aparecieron solo con volumen. La tienda `escala` tenía 50 000 productos, 100 000 SKU, 1 millón de entradas de auditoría y 5,8 millones de eventos.

| Vista | Antes | Después |
|---|---|---|
| Reporte de inventario | 1 584 ms | **12–30 ms** (~130 ms la primera vez después de un cambio) |
| Operación › Estado | 507 ms | **8 ms** |
| Operación › Actividad (filtros) | 307 ms | **3 ms** |
| Catálogo (resumen) | 194 ms | **1 ms** (~60 ms la primera vez después de un cambio) |

## Qué se cambió y por qué

1. **Configuración tardaba 300 ms con una API de 3 ms.** Cada sección de Configuración se cargaba aparte dentro de la página. React revela un Suspense anidado en tandas de 300 ms, así que la sección esperaba aunque su código llegara en 5 ms. Ahora la página y la sección abierta se cargan juntas, y las demás secciones se precargan en segundo plano (`web/src/lib/lazy.ts`).

2. **Archivos comprimidos en cada petición.** El servidor comprimía el archivo principal (560 KB) en cada petición, a nivel rápido. Ahora cada archivo se comprime una sola vez al arrancar, al nivel máximo, y se sirve tal cual (`internal/webui/webui.go`). El middleware de compresión deja pasar lo que ya viene comprimido.

3. **Cascadas en las fichas.** Una ficha pedía su registro, lo dibujaba y solo entonces sus secciones pedían sus datos: dos viajes, uno tras otro.
   - **Producto:** sus secciones salen junto con el producto. Además, al apuntar 80 ms o enfocar un producto en la lista, sus datos empiezan a cargar (`web/src/pages/catalog/productQueries.ts`).
   - **Todas las fichas:** el panel aprende, por tipo de ficha, qué lecturas llevan el id del registro como segmento de la ruta. En la siguiente ficha del mismo tipo las lanza en el momento de la navegación (`web/src/routePrefetch.ts`). Solo se repiten lecturas (GET), y la respuesta se usa solo dentro de los 4 segundos posteriores a la navegación.

4. **Carga inicial.**
   - `/v1/me` se pide desde el HTML, en paralelo con el código, en vez de esperarlo.
   - La librería de validación (150 KB de fuente) solo se usaba para validar los errores de la API. Se reemplazó por una validación de 40 líneas.
   - El código principal bajó de 172 KB a 158 KB comprimido.

5. **Reporte de inventario.** Buscaba el precio de cada SKU uno por uno y ordenaba los 100 000 en disco para mostrar 500. Ahora se calcula desde la proyección en memoria:
   - existencias y precio se toman en una pasada;
   - las filas visibles salen de un montículo acotado;
   - títulos y ventas de 30 días se piden solo para esas filas;
   - el resultado se guarda por versión de la proyección.

   Una prueba compara el reporte de memoria contra el de SQL, fila por fila, con cada filtro (`TestStockCountsFromMemoryMatchDatabase`).

6. **Operación: estado y filtros de auditoría.** Contaban millones de filas en cada visita. Ahora hay un resumen incremental (migración 159, `internal/reports/activity.go`):
   - un trabajo con lease suma solo lo nuevo desde su marca;
   - la pantalla suma el resumen más las filas posteriores a la marca, así los números siguen siendo exactos;
   - sin el trabajo, el resultado es el mismo, solo más lento (`TestActivityRollupExact`).

   «Cambios registrados (24 h)» ahora cuenta por horas completas: desde el inicio de la hora de hace 23 horas.

7. **Resumen del catálogo.** Pedía existencias 100 000 veces, tomando el candado y armando una foto en cada una. Ahora usa una consulta sin foto, toma el candado una sola vez y guarda el resultado por versión.

## Lo que queda

- **Producto y promoción siguen en dos viajes en la primera visita sin apuntar.** Algunas secciones dependen del contenido del registro, como el SKU de cada variante. Con el puntero sobre la fila no se nota.
- **La primera carga del panel cuesta cuatro viajes de red:** HTML, código, sesión y datos de Inicio. Son ~450 ms con 80 ms de ida y vuelta. Es la que se siente al iniciar sesión. Después, cada vista cuesta un viaje.
- **Por HTTP/1.1 sin HTTPS** (el servidor de desarrollo), las vistas con más de 6 llamadas hacen cola. En producción, detrás de Caddy con HTTP/2, no.
