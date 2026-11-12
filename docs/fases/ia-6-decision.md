# IA-6: decisión de fine-tuning

Fecha: 12 de noviembre de 2026. Plan: `docs/fases/plan-ia-contextual.md`.

## Decisión

**No entrenar un modelo propio por ahora.** El plan pedía entrenar solo si una evaluación demostraba que la recuperación y los prompts no bastan. Las evaluaciones de IA-1 a IA-5 dicen que bastan:

| Medida | Sin modelo propio | Meta |
|---|---|---|
| Búsqueda (evaluación en español) | nDCG@10 0,814 (léxica 0,528); sin resultado 9 % (antes 42 %) | mejorar contra léxica ✓ |
| Búsqueda con 50 000 productos, sin caché | p95 11 ms | < 15 ms ✓ |
| Preguntas con fuente | 27/27; «no sé» 5/5 y 3/3; fidelidad 100 % | ≥ 95 % ✓ |
| Precios o existencias inventados | 0 | 0 ✓ |

Entrenar hoy costaría mantener un modelo (datos, reentrenos, versiones, evaluación) sin una ganancia medida que lo pague.

## Lo que queda listo para decidir con datos reales

La decisión ya no se toma una vez para todos: cada tienda tiene su medición en *Recomendaciones e IA › Búsqueda por significado › Calidad con tus búsquedas reales* (`GET /v1/ai/eval`).

- **Etiquetas:** las búsquedas en las que los clientes hicieron clic (relevante) o agregaron al carrito (muy relevante), de la tabla `search_clicks`, con RLS. Hasta las 1 000 búsquedas más frecuentes con al menos 2 elecciones.
- **Comparación:** las mismas búsquedas con solo palabras y con significado (híbrida); nDCG@10 y recall@50.
- **Sin trampa:** se apagan los impulsos aprendidos al medir, porque salen de esos mismos clics.

**Reglas de decisión** (en `internal/api/store_eval.go`):

| Situación | Decisión |
|---|---|
| Menos de 200 búsquedas etiquetadas | Juntar datos: todavía no se puede decidir |
| Híbrida con nDCG@10 ≥ 0,75 | No hace falta entrenar |
| Híbrida abajo de 0,75 con vectores locales | Probar antes un proveedor semántico (Voyage) y volver a medir |
| Híbrida abajo de 0,75 con proveedor semántico | Considerar entrenar el reordenador o los vectores con estas búsquedas, y aceptar el modelo nuevo solo si mejora esta misma medición |

## Si algún día se entrena

- **Qué:** el modelo de vectores o un reordenador pequeño. **Nunca** el modelo de lenguaje: el tono y el catálogo se resuelven con prompts y recuperación, que se actualizan al instante.
- **Con qué:**
  - parejas búsqueda → producto elegido (`search_clicks`);
  - sugerencias aplicadas o rechazadas (`ai_suggestions`);
  - relaciones aprobadas o rechazadas (`kg_edges`).
- **Cómo se acepta:** con el mismo arnés.
  - `TestSearchEvaluation` (55 búsquedas en español, ningún tipo puede empeorar).
  - La medición de la tienda, con un conjunto apartado.
  - La compuerta de 15 ms.
- **Límite conocido de las etiquetas:** los clics tienen sesgo de posición, porque se elige más lo que ya aparecía arriba. Antes de entrenar hay que corregirlo; por ejemplo, ponderando por la posición en que se mostró.

## Prueba

`TestStoreSearchEval`: con las elecciones de clientes de la tienda, la híbrida supera a la léxica (0,942 contra 0,667 en la prueba). La decisión sigue los umbrales, sin datos dice «juntar datos» y otra tienda no ve las etiquetas.
