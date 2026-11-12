# IA-2: fragmentos con contexto y «Pregúntale a la tienda»

Fecha: 12 de noviembre de 2026. Plan: `docs/fases/plan-ia-contextual.md`.

## Qué se construyó

| Pieza | Dónde | Detalle |
|---|---|---|
| Fuentes | `api/knowledge.go` | Políticas publicadas, artículos del blog publicados, descripciones largas de producto (600+ caracteres). Al preguntar por un producto también entran su descripción, sus especificaciones y sus opiniones aprobadas con la respuesta de la tienda. |
| Fragmentos con contexto | `ai/chunk.go` | Corte por encabezado y párrafo (~700 caracteres, nunca a través de un encabezado). Cada fragmento lleva «Título › Sección» y se vectoriza junto con su contexto. Con un modelo configurado, el contexto suma una oración escrita por Claude Haiku con el documento completo en caché. |
| Almacenamiento | `migrations/158`, `store/ai.go` | `ai_chunks` con título y dirección de la fuente para citarla. Mismo índice en memoria por tienda que los productos. |
| Mantenimiento | trabajo `ai-docs` (turno de un nodo, cada 30 s) | Compara el hash de cada documento. Si cambió, se vuelve a cortar; si se despublicó o se borró, se quitan sus fragmentos. Los demás nodos recargan por el evento `ai.docs`. |
| Respuestas | `api/ask.go`, `POST /s/{tienda}/api/ask` | 1) Precio, existencias y variantes salen del sistema, nunca de un texto ni de un modelo. 2) Oraciones candidatas puntuadas por significado, contexto, palabras compartidas (raíz ligera del español, verbos irregulares) y tipo de respuesta esperado (duración, peso, material, origen, precio, capacidad). 3) CRAG: si la mejor es débil, reescribe la pregunta con los sinónimos de la tienda y vuelve a juzgar; si sigue débil, responde «no encontré esa información». 4) Cita la oración exacta con su fuente. Con modelo: respuesta corta escrita solo con las fuentes, aceptada solo si cita y si cada número que dice está en ellas. |
| Tienda | `shop/product.tmpl`, `shop/shop.js` | Bloque «Pregúntale a la tienda» en la ficha: funciona con teclado, anuncia la respuesta (`role=status`) y enlaza la fuente. Traducido a los 6 idiomas de la tienda. |
| Claude | `ai/llm.go` | SDK oficial de Go. Llave y dirección explícitas (`APEXX_ANTHROPIC_API_KEY`); nada se hereda del entorno por accidente. Modelos: `claude-sonnet-5-5` para respuestas y `claude-haiku-5-5` para contexto masivo. El uso y el costo se registran por función en `ai_usage` y respetan el presupuesto mensual de la tienda. |

## Evaluación (`TestAskEvaluation`)

Políticas de envíos, devoluciones, garantía y pagos; un artículo del blog; tres productos y una opinión.

| | Resultado |
|---|---|
| Preguntas con respuesta en la tienda | **27/27 (100 %)** |
| «No sé» cuando la tienda no lo dice | **5/5 (100 %)** |
| Fidelidad (la respuesta es la oración citada o un dato del sistema) | **100 %** (meta: 95 %) |
| Conjunto apartado, escrito después de ajustar y nunca usado para ajustar | **10/12 (83 %)** |

**Límite honesto:** los umbrales se ajustaron sobre las 32 primeras preguntas. El conjunto apartado mide cómo generaliza. Sus dos fallas son paráfrasis («a mi tarjeta» frente a «al mismo medio de pago») y un dato que solo está en el título del producto. Responde «no sé» en vez de inventar; un proveedor semántico debería cerrar esa brecha y se medirá con este mismo arnés.

Sin llave de Claude todo funciona en modo extractivo, que es el que se midió arriba. El camino con modelo se probó contra un servidor de prueba: acepta una respuesta citada y fiel, y rechaza una con un número que no está en las fuentes o sin cita. Falta medir la calidad con el modelo real cuando el servidor tenga su llave.

## Pruebas

- `TestKnowledgeFollowsDocuments`: alta, sin cambios, edición, despublicación y aislamiento entre tiendas.
- `TestAskIgnoresInstructionsInReviews`: una opinión con instrucciones maliciosas se cita como opinión y nunca se obedece; el precio no sale de ella.
- `TestAskWithLanguageModel`: contexto escrito por el modelo; respuesta escrita aceptada; número inventado rechazado; sin cita rechazada; uso registrado por función.
- `TestClaudeAgainstFakeServer`: el cliente manda la llave, usa la ruta correcta y calcula el costo.
- Navegador (Playwright), escritorio y teléfono, solo con teclado: responde «Tienes 30 días naturales…» con su fuente, sin desborde horizontal ni errores en consola.
