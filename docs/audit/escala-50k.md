# Simulación de escala: 1 000, 10 000 y 50 000 productos

Fecha: 12 de noviembre de 2026. Entorno: servidor de desarrollo (un proceso Go y Postgres 16 en la misma máquina). Tienda `escala` creada con `qa-setup`. Arnés: `node run.js N`, en la carpeta de trabajo de la sesión.

Cada producto tiene 2 variantes, precio, precio anterior, existencias, marca, categoría y etiquetas. Los títulos combinan tipo, marca, material y color, para que la búsqueda tenga trabajo real.

## Qué hace cada corrida

1. **Foto inicial.** Cuenta las filas de la tienda en cada tabla que tiene `tenant_id`: 144 tablas.
2. **Carga por la vía del comerciante.** CSV de hasta 8 000 productos por archivo, vista previa, aplicar, y esperar a que termine.
3. **Visible en la tienda.** Cuánto tarda la tienda en mostrar los N productos después de terminar la importación. No hay indexador aparte.
4. **Latencias.** p50, p95 y p99 de 15 rutas de la tienda, la API y el panel, con un cliente y con 16 a la vez.
5. **Propagación.** Un cambio de precio por operación masiva, hasta verse en la ficha. Se mide 20 veces.
6. **Reversión de precios.** Foto de todos los precios. Sube 10 % a 1 000 productos. Restaura los precios exactos por la operación masiva de precios. Compara línea por línea.
7. **Borrado total y comparación con la foto inicial.** Solo pueden crecer las tablas de historial: auditoría, outbox, historial de precios y registros de trabajos.

## Defectos que encontró la simulación (corregidos)

| # | Hallazgo | Causa | Corrección |
|---|---|---|---|
| 1 | Tras borrar 1 000 productos con 8 peticiones a la vez, la tienda seguía mostrando 31 en catálogo, búsqueda y sugerencias | Proyección, webhooks y feed seguían el outbox con `id > cursor`. Una transacción que confirmaba tarde con un id menor quedaba atrás del cursor y su evento se perdía | Migración 154: posición `seq` asignada por un secuenciador único a eventos ya confirmados. `TestOutboxLateCommitIsNotLost` falla con el código anterior y pasa con el nuevo |
| 2 | Catálogo página 1: 340 ms; con 16 a la vez, 6,3 s | Cada visita calculaba precio, existencias, orden y facetas de los 50 000 productos | Caché compartida por versión de la proyección: un cálculo por cambio; quien llega mientras tanto espera ese mismo cálculo |
| 3 | Arranque: 44 s | Unas 250 000 consultas, una por producto, SKU y precio | Una consulta por tipo (`EachProduct`, `AllStock`, `AllPrices`) |
| 4 | Panel, abrir producto: 31 ms | Listar ubicaciones recorría las 100 000 filas de existencias | Índice (migración 155) y recorrido por saltos: 0,18 ms |
| 5 | Panel, inicio: 102 ms; con 16 a la vez, 660 ms | Sumaba en SQL todas las existencias en cada visita | Conteo en memoria, una vez por versión, igual al SQL |
| 6 | Panel, buscar: 46 ms | Comparación con tolerancia a errores sobre cada palabra de cada producto | Índice de palabras (palabra → productos, palabras agrupadas por largo) |
| 7 | Un cambio de precio masivo tardaba 2 s en verse | La cola de operaciones masivas se revisaba cada 2 s | Crear la operación despierta la cola |
| 8 | Sugerencias mostraron productos recién borrados una vez | Se servía el resultado anterior mientras se recalculaba | Cada producto del resultado se confirma contra el catálogo vivo antes de responder |
| 9 | No había borrado masivo; borrar 50 000 de uno en uno tomaba 5 min | — | "Eliminar…" en el panel, hasta 500 por vez, y borrado por conjuntos: 1 940 productos/s |
| 10 | Con la primera versión del borrado masivo, 11 bloques de 500 superaron el tiempo límite (503) | Cada producto se borraba con su ronda completa de consultas | Borrado por conjuntos. Cada bloque es atómico: el bloque que falló no dejó nada a medias |

## Resultados con 50 000 productos (100 000 variantes)

Medición de referencia. El servidor confía en `X-Forwarded-For`, como detrás de Caddy, y cada petición llega con una IP distinta, como compradores distintos. Cualquier respuesta que no sea 2xx se cuenta como error y no se cronometra. Errores: 0.

En mediciones anteriores el limitador por IP (300 por minuto) respondía 429 a parte de las peticiones del arnés, y esas respuestas instantáneas abarataban algunas cifras. Las de esta tabla no tienen ese problema.

| Ruta | Antes | Después | 16 a la vez, p95 |
|---|---|---|---|
| Tienda: portada | 1,3 ms | 1,6 ms | 17 ms |
| Tienda: catálogo, página 1 | 340 ms | 3,3 ms | 34 ms |
| Tienda: catálogo, página 40 | 375 ms | 3,1 ms | 25 ms |
| Tienda: categoría | 77 ms | 2,9 ms | 23 ms |
| Tienda: filtro marca y precio | — | 3,1 ms | 54 ms |
| Tienda: búsqueda con error de dedo | — | 1,8 ms | 18 ms |
| Tienda: sugerencias | 97 ms | 0,9 ms | 13 ms |
| Tienda: ficha | 2,0 ms | 1,9 ms | 34 ms |
| API: simular carrito | 1,5 ms | 1,8 ms | 17 ms |
| Panel: lista de productos | 7 ms | 3,1 ms | 33 ms |
| Panel: buscar producto | 46 ms | 1,2 ms | 13 ms |
| Panel: abrir producto | 31 ms | 2,4 ms | 24 ms |
| Panel: inicio | 102 ms | 3,9 ms | 30 ms |
| Cambio de precio masivo → visible en la ficha | 2 000 ms | 16 ms | — |
| Importar 50 000 productos (CSV) | 270 s (185/s) | 39 s (1 279/s) | — |
| Borrar 50 000 productos | 280–550 s, con fallos | 22 s (2 258/s) | — |
| Arranque del servidor | 44 s | 0,45 a 1,9 s | — |

## Reversión

| Prueba | 1 000 | 10 000 | 50 000 |
|---|---|---|---|
| Precios restaurados exactos | sí (2 000 líneas) | sí (20 000) | sí (100 000) |
| Tras el borrado: tablas iguales a la foto inicial | sí | sí | sí* |
| Tras el borrado: catálogo, búsqueda y sugerencias | 0 | 0 | 0 |

\* En la corrida de 50 000 con el borrado anterior quedaron 5 500 productos completos (defecto #10). Se borraron con el borrado por conjuntos y entonces todas las tablas volvieron a la foto inicial. La única diferencia fue la sesión de inicio del script de limpieza.

Solo crece el historial, a propósito: auditoría, outbox, historial de precios, registros de importación y de operaciones masivas, y la analítica de búsquedas (`search_terms_daily`).

## Lo que sigue

- **Importación: hecha la carga por conjuntos.** Los productos nuevos entran por `INSERT … SELECT FROM unnest`, en bloques de 2 000. No se usa `COPY` porque no respeta la seguridad por fila. Resultado: 185 → 1 279 productos/s, con resultado idéntico al camino de uno en uno (`TestImportSetPathEqualsOneByOne`). Lo que queda en el perfil:
  - leer el CSV (normalizar acentos): 19 %;
  - aplicar los eventos en la proyección: 16 %.
  Siguientes pasos: un evento por lote, leer el CSV en varios núcleos y procesar el archivo mientras sube, sin el límite de 5 MB.
- **Arranque sin corte: hecho.** Dos nodos detrás de Caddy, drenado con `/readyz` al apagarse, turnos de trabajos de fondo en la base y límite de login compartido. Prueba de relevo: 154 689 peticiones durante 4 reemplazos de nodo, 0 fallidas, la más lenta 55 ms. Detalle en `docs/deploy.md` §7.1.
- **Memoria:** 523 MB con 50 000 productos. El índice de palabras suma unos 60 MB; hay que revisar estructuras más compactas.
- **Outbox:** un ciclo completo de carga y borrado de 50 000 productos generó unos 600 000 eventos. Con un evento por lote bajaría unas 1 000 veces.
