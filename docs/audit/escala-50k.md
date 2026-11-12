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

| Ruta | Antes | Después | 16 a la vez, p95 |
|---|---|---|---|
| Tienda: portada | 1,3 ms | 1,7 ms | 20 ms |
| Tienda: catálogo, página 1 | 340 ms | 3 ms | 29 ms |
| Tienda: categoría | 77 ms | 0,7 ms | 10 ms |
| Tienda: búsqueda con error de dedo | 0,7 ms | 0,6 ms | 12 ms |
| Tienda: sugerencias | 97 ms | 0,9 ms | 6 ms |
| Tienda: ficha | 2,0 ms | 1,7 ms | 13 ms |
| API: simular carrito | 1,5 ms | 1,6 ms | 19 ms |
| Panel: lista de productos | 7 ms | 2,7 ms | 27 ms |
| Panel: buscar producto | 46 ms | 0,9 ms | 14 ms |
| Panel: abrir producto | 31 ms | 1,5 ms | 14 ms |
| Panel: inicio | 102 ms | 3,3 ms | 20 ms |
| Cambio de precio masivo → visible | 2 000 ms | 15 ms | — |
| Arranque del servidor | 44 s | 0,45 a 1,9 s | — |

## Reversión

| Prueba | 1 000 | 10 000 | 50 000 |
|---|---|---|---|
| Precios restaurados exactos | sí (2 000 líneas) | sí (20 000) | sí (100 000) |
| Tras el borrado: tablas iguales a la foto inicial | sí | sí | sí* |
| Tras el borrado: catálogo, búsqueda y sugerencias | 0 | 0 | 0 |

\* En la corrida de 50 000 con el borrado anterior quedaron 5 500 productos completos (defecto #10). Se borraron con el borrado por conjuntos y entonces todas las tablas volvieron a la foto inicial. La única diferencia fue la sesión de inicio del script de limpieza.

Solo crece el historial, a propósito: auditoría, outbox, historial de precios y registros de importación y de operaciones masivas.

## Lo que sigue

- **Importación: 185 productos/s, unos 4,5 min para 50 000.** El costo son las idas y vueltas a la base, no el cálculo. Plan:
  - carga por conjuntos con `COPY` a tablas temporales y fusión con `INSERT … SELECT … ON CONFLICT`;
  - un evento por lote;
  - auditoría por lote con huella de cada producto;
  - archivo procesado mientras sube, sin el límite de 5 MB.
- **Arranque sin corte:**
  - foto de la proyección en disco con su posición del outbox; se atiende de inmediato y se pone al día en segundo plano;
  - relevo de versiones con `/readyz`, para que un despliegue nunca deje la tienda sin servicio.
- **Memoria:** 523 MB con 50 000 productos. El índice de palabras suma unos 60 MB; hay que revisar estructuras más compactas.
- **Outbox:** un ciclo completo de carga y borrado de 50 000 productos generó unos 600 000 eventos. Con un evento por lote bajaría unas 1 000 veces.
