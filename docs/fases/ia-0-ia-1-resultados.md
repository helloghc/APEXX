# IA-0 y IA-1: cimientos y búsqueda híbrida

Fecha: 12 de noviembre de 2026. Plan: `docs/fases/plan-ia-contextual.md`.

## Qué se construyó

### IA-0 Cimientos

| Pieza | Dónde | Detalle |
|---|---|---|
| Tablas con RLS forzada | `migrations/157_ai_foundations.sql` | `ai_chunks`, `kg_edges`, `ai_suggestions`, `ai_usage`. La auditoría de aislamiento (`TestEveryTenantTableIsIsolated`) las incluye sola. |
| Vectores en Postgres sin extensiones | `store/ai.go` | int8 normalizado en `bytea`, la misma forma que el índice en memoria. pgvector no está en todos los Postgres administrados y no hace falta: la búsqueda se hace en memoria. |
| Índice en memoria | `ai/vector.go` | Dos pasadas: 1 bit por dimensión comparado con popcount y luego int8 exacto sobre 400 candidatos. 50 000 vectores: **1,0 ms** (escaneo completo: 15,2 ms), mismos resultados (recall 1,000 frente al exacto en datos agrupados). |
| Flujo por el outbox | `api/semantic.go` | Producto guardado → todos los nodos lo marcan → el nodo con el turno `ai-embed` calcula los que cambiaron → `ai_chunks` + evento `ai.vectors` → los demás nodos lo cargan. El nodo que escribió no vuelve a leer lo suyo. |
| Borrar producto borra sus vectores | `store/catalogx.go` | En la misma transacción, también en el borrado masivo por conjuntos. |
| Autocorrección | `api/semantic.go` | Cada 10 minutos se compara todo el catálogo con los vectores: lo que falta o cambió se recalcula; lo huérfano o de otro modelo se borra. Un cambio de modelo se migra solo. |
| Proveedores | `ai/embed.go` | Local (palabras, raíces, pares y trigramas con hashing; sin red ni costo; palabras vacías con peso mínimo) y Voyage (semántico, por HTTPS, con llave del proxy). |
| Presupuesto y apagado por tienda | `api/learning.go` | `semanticOff` y `budgetUsd` (10 USD por omisión). Al tope se pausa y la búsqueda sigue léxica. |
| Panel | `web/src/pages/engage/AI.tsx` | Tarjeta «Búsqueda por significado»: encendido, cobertura, memoria, modelo, gasto del mes, presupuesto y último error. |
| Arnés de evaluación | `ai/evalset` | 93 productos y 55 búsquedas etiquetadas en español, de 6 tipos. |

### IA-1 Búsqueda híbrida y re-ranking

| Pieza | Dónde | Detalle |
|---|---|---|
| Fusión léxico + vector | `api/hybrid.go`, `catalog/browse.go` | Reciprocal Rank Fusion. Solo corre cuando la caché falla (una vez por versión del catálogo y consulta). |
| Pisos de similitud | `api/hybrid.go` | 0,20 absoluto y 30 % del mejor: lo que no se parece no entra, y las búsquedas sin sentido siguen sin resultados. |
| Re-ranking | `catalog/browse.go` (`fuse`) | Orden: fijados y clics aprendidos, luego relevancia fusionada, luego existencias. |
| Intención a filtros | `catalog/intent.go` | «playeras rojas baratas» se lee como texto «playeras», color Rojo y menor precio. Reglas, sin modelo, en microsegundos. Si los filtros dejan cero, responde la búsqueda normal. La tienda muestra «Entendimos tu búsqueda con: …». |
| Vector de la consulta | `api/hybrid.go` | Caché de consultas. Con un proveedor remoto, la primera vez responde léxico y calcula en segundo plano: el comprador nunca espera a la red. |
| Similares por vector | `api/learning.go` | Un producto nuevo sin ventas ni visitas recomienda los más parecidos, después de lo que los clientes eligieron juntos. |
| Sugerencias desde búsquedas sin resultado | `api/engage.go`, panel «Qué buscan» | Productos parecidos y un sinónimo propuesto; un clic lo agrega a las reglas. |
| Índice de palabras más rápido | `catalog/wordindex.go`, `catalog/search.go` | Los números se buscan por prefijo con búsqueda binaria y sin tolerancia a errores («2800» ya no encuentra «2900»). |
| Solo los candidatos | `catalog/catalog.go` (`ProductsIn`) | Browse recorre los productos que el índice dejó, no los 50 000. |

## Calidad de búsqueda (evaluación en español)

nDCG@10 (1 es perfecto) por tipo de búsqueda:

| | General | Sin resultado | Exacta | Con error | Plural | Parcial | Sinónimo | Intención |
|---|---|---|---|---|---|---|---|---|
| Léxica (antes) | 0,528 | **42 %** | 0,844 | 0,925 | 0,739 | 0,987 | 0,100 | 0,039 |
| Híbrida (ahora) | **0,814** | **9 %** | 0,969 | 0,992 | 0,991 | 1,000 | 0,600 | 0,562 |

Ningún tipo pierde. Las búsquedas sin sentido («zzqxv», «kkkjjj wwq») siguen sin resultados.

**Límite honesto:** los pisos se ajustaron sobre esta misma evaluación. El modelo local entiende plurales, errores y palabras parciales, pero no sinónimos de verdad: «cobertor» encuentra «cobija» solo si comparten letras o contexto. Con un proveedor semántico (Voyage) se espera que suban sinónimos e intención; se mide con el mismo arnés antes de activarlo.

## Escala con 50 000 productos

| Medida | Resultado |
|---|---|
| Vectores de 50 000 productos | listos **259 ms** después de que la tienda los muestra |
| Memoria del índice | **18,3 MB** (384 dimensiones int8) |
| Importación con vectores | **34 s (1 465/s)**, antes 39 s sin vectores |
| Búsqueda en caché | p95 3,7 ms |
| Búsqueda sin caché (consulta nueva), léxica | p50 0,34 ms · p95 12,6 ms (antes p50 11,3 ms) |
| Búsqueda sin caché, híbrida | p50 **1,7 ms** · p95 **11 ms** (meta: menos de 15 ms) |
| Borrado total | 50 000 en 20 s; **ninguna tabla distinta de la foto inicial**, `ai_chunks` incluida |

## Pruebas

- `TestSemanticIndexFollowsCatalog`: alta, cambio, sin cambio, borrado y borrado masivo.
- `TestSemanticIsolation`: una tienda no ve vectores de otra; el rol de la app sin tienda no ve filas.
- `TestSemanticVectorsReachEveryNode`: el nodo líder escribe y otro nodo carga por el outbox.
- `TestSemanticBudgetAndOutage`: proveedor caído (nada se pierde, se reintenta), tope de presupuesto, apagado.
- `TestHybridSearchInStore`: «algo para la lluvia» encuentra chamarra y botas, no el taladro; lo exacto va primero; apagado vuelve a léxico al instante; la sugerencia aparece.
- `TestIntentFiltersInStore`: «playeras rojas baratas» filtra por color y ordena por precio.
- `TestSimilarRecommendations`: producto sin historial recomienda los parecidos.
- `TestSearchEvaluation`: compuerta de calidad; falla si la híbrida pierde en algún tipo.
- `TestHybridSearchCost50k` (`APEXX_SCALE=1`): compuerta de 15 ms sin caché.
- `TestParseIntent`, `TestIndexPutDeleteSearch`, `TestSearchMatchesExact`, `TestMetrics`.
