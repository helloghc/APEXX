# Plan de IA de Apexx: resumen de resultados

Fecha: 12 de noviembre de 2026. Plan: `plan-ia-contextual.md`. Página con el avance: `plan-ia-apexx.html`.

| Fase | Estado | Resultado clave | Detalle |
|---|---|---|---|
| PRE Escala base | Cerrada | 50 000 productos: tienda y panel en 1–4 ms; despliegue sin corte (154 689 peticiones, 0 fallidas) | `../audit/escala-50k.md`, `../deploy.md` §7.1 |
| IA-0 Cimientos | Cerrada | Vectores por el outbox con RLS; 50 000 vectores listos 0,3–1,3 s después de verse, 18 MB; búsqueda vectorial 1,0 ms | `ia-0-ia-1-resultados.md` |
| IA-1 Búsqueda híbrida | Cerrada | nDCG@10 0,528 → 0,814; búsquedas sin resultado 42 % → 9 %; sin caché p95 11 ms con 50 000 | `ia-0-ia-1-resultados.md` |
| IA-2 Fragmentos con contexto | Cerrada | «Pregúntale a la tienda»: 27/27, fidelidad 100 %, «no sé» sin fuente | `ia-2-resultados.md` |
| IA-3 Asistentes con CRAG | Cerrada | 0 precios o existencias inventados; «no sé» cuando la tienda no cubre el tema; copiloto del panel con permisos | `ia-3-resultados.md` |
| IA-4 Grafo de conocimiento | Cerrada | Compatibles, accesorios y alternativas con motivo, aprobables y auditados; fichas técnicas y cotización B2B desde PDF | `ia-4-resultados.md` |
| IA-5 Copilotos por panel | Cerrada, salvo una medición | Catálogo (atributos, categoría, SEO, texto alternativo, duplicados), importación, promociones, segmentos y pedidos: aplicables y reversibles. Correos y diseño, con Claude: falta medir con la llave en el servidor | `ia-5-resultados.md` |
| IA-6 Decisión de fine-tuning | Cerrada | No entrenar ahora; cada tienda tiene su medición con sus búsquedas reales y umbrales para decidir | `ia-6-decision.md` |

## Medición final con 50 000 productos y toda la IA activa

Vectores, fragmentos, grafo y sugerencias corrieron mientras se importaba, se medía y se borraba.

| Medida | Resultado |
|---|---|
| Importación | 31 s (1 606 productos/s) |
| Vectores | listos 1,3 s después de que la tienda muestra los productos; 18,3 MB |
| Catálogo página 1 | p50 3,0 ms · p95 5,5 ms · 16 a la vez p95 28,6 ms |
| Búsqueda con error de dedo | p50 2,2 ms · p95 3,2 ms |
| Ficha de producto | p50 3,4 ms · p95 4,9 ms |
| Cambio de precio visible en la ficha | p50 16 ms |
| Reversión de precios | exacta (100 000 líneas) |
| Tras el borrado total | **ninguna tabla distinta de la foto inicial**: `ai_chunks`, `kg_edges`, `ai_suggestions` y `tenant_docs` incluidas |

**Hallazgo y corrección:** en una corrida anterior, el grafo revisó los 50 000 productos recién importados de una sola vez y el p95 del catálogo subió a 10,7 ms (con 16 a la vez, 38 ms). Ahora el recorrido completo y los productos nuevos se procesan en tramos de 2 000 por minuto. El p95 volvió a 5,5 ms (con 16 a la vez, 28,6 ms).

## Principios que se cumplieron

1. El comprador nunca espera a un modelo. Todo lo de la tienda es determinista y en milisegundos. Con un proveedor remoto, la primera búsqueda responde solo con palabras y calcula el significado en segundo plano.
2. La IA no inventa datos de comercio. Precio, existencias y variantes salen del sistema; los montos de las respuestas se comparan contra él en las pruebas.
3. Una sola base, sin indexador aparte. Vectores, fragmentos, grafo y sugerencias viven en Postgres con RLS y se mantienen por el outbox.
4. Todo se puede deshacer y queda en la auditoría encadenada.
5. El contenido recuperado es dato. Las instrucciones dentro de opiniones o PDF no se obedecen ni se usan como fuente.

## Lo que depende de configuración

- `APEXX_ANTHROPIC_API_KEY` en el servidor activa el contexto de fragmentos (Haiku), las respuestas escritas (Sonnet) y los textos de correos y diseño. Ya está guardada en el entorno de la sesión; se mide en una sesión nueva.
- `APEXX_EMBEDDINGS=voyage` y `VOYAGE_API_KEY` activan vectores semánticos. Se evalúan con el mismo arnés antes de usarse.
