# IA-4: grafo de conocimiento, fichas técnicas en PDF y cotización desde una solicitud

Fecha: 12 de noviembre de 2026. Plan: `docs/fases/plan-ia-contextual.md`.

## Qué se construyó

| Pieza | Dónde | Detalle |
|---|---|---|
| Grafo de productos | `api/graph.go`, `store/graph.go`, `kg_edges` | Tres relaciones con su motivo en palabras que el comerciante puede comprobar y una confianza. **Compatible con:** el texto del producto dice «compatible con / repuesto para / sirve para…» y el título del otro producto aparece en esa frase. **Accesorio de:** se compraron juntos en 2 o más pedidos, es de otra categoría y cuesta como máximo la mitad. **Alternativa de:** misma categoría, muy parecidos en significado (≥ 75 %) y precio a ±40 %. |
| Propuestas | trabajo `ai-graph` (un nodo, cada minuto; todo el catálogo cada 6 h) y botón «Buscar relaciones ahora» | Solo productos que cambiaron. Una relación rechazada no se vuelve a proponer; una aprobada sigue aprobada. |
| Decisión del comerciante | Búsqueda y opiniones › Recomendaciones e IA › «Relaciones entre productos» | Por revisar, aprobadas y rechazadas, con selección múltiple. Se ve la tasa de aprobación (la medida de calidad del plan). Cada decisión queda en la auditoría encadenada y se puede deshacer. |
| Uso | ficha, asistente, MCP | Solo lo aprobado. En la ficha: «Compatible con», «Combínalo con» (primero lo manual, luego el grafo) y «Alternativas», que encabeza la sección cuando el producto está agotado. El asistente responde «¿con qué es compatible?», «¿qué accesorios tiene?», «¿hay algo parecido?». `get_product` de MCP incluye `compatibleWith`, `accessories` y `alternatives`. |
| Lectura de PDF | `ai/pdf.go` | Extractor en Go puro. Las líneas se arman por altura del texto, porque muchos PDF no traen anchos de letra. Un archivo dañado da error, nunca una caída. Hasta 60 páginas y 10 MB. Un PDF escaneado sin texto se rechaza con un mensaje claro. |
| Fichas técnicas | `api/datasheet.go`, editor de producto | El comerciante sube el PDF del fabricante. Solo se guarda el texto. Aparece en la ficha («Ficha técnica») y responde preguntas citando «Ficha técnica de …». También entra a los fragmentos de la tienda. Borrar el producto borra su ficha y sus fragmentos. |
| Cotización desde una solicitud (B2B) | `api/rfq.go`, Pedidos › Cotizaciones | Texto pegado o PDF del cliente. Cada renglón con cantidad («20 pzas …», «… x 50», «… 100 piezas») o con un SKU existente se empareja: primero el SKU exacto, luego la búsqueda híbrida. Cada coincidencia trae precio y existencias del sistema. Los saludos, encabezados y direcciones no son renglones. El vendedor revisa cada renglón y crea un pedido borrador. Nada se pide solo. |

## Hallazgos de las pruebas

| Hallazgo | Corrección |
|---|---|
| La frase «compatible con Taladro percutor 650 W y otros taladros» no encontraba el taladro: se buscaba la frase completa. | Se mide cuánto del título del otro producto aparece en la frase, con al menos 2 palabras en común. |
| El PDF salía como una sola línea sin espacios. La prueba unitaria no lo veía porque solo revisaba que cada texto estuviera contenido. | Líneas por altura del texto. La prueba exige ahora exactamente una línea por renglón impreso, y la de cotización con PDF exige las mismas cantidades y SKU que con texto. |
| «¿A cuántas revoluciones gira?» no compartía palabras con «0 a 3000 rpm». | Tipo de respuesta «velocidad»; los tipos que nombran el tema suman un poco más (0,35) que los que solo miden (0,30). La evaluación de IA-2 e IA-3 sigue igual. |
| El editor pedía la ficha técnica y recibía un 404 cuando no había, y se veía como error en la consola. | Responde 200 con `datasheet: null`. |

## Resultados

| Medida | Resultado |
|---|---|
| Propuestas con motivo | 100 % (compatibilidad cita el texto; accesorio, el número de pedidos; alternativa, categoría, similitud y diferencia de precio) |
| Visible antes de aprobar | nunca (probado en ficha y asistente) |
| Rechazadas propuestas otra vez | 0 |
| Decisiones auditadas | 100 %, cadena íntegra |
| Borrar producto | borra sus relaciones, su ficha técnica y sus fragmentos |
| Aislamiento | otra tienda no ve relaciones ni fichas |
| Cotización desde texto o PDF | 3 de 3 renglones con cantidad y SKU correctos; encabezados, saludos y dirección descartados; precio y existencias del sistema |
| Navegador, teclado, escritorio y teléfono | tres pantallas nuevas sin desborde ni errores en consola |

**Límite honesto:** en la tienda de QA (120 productos de una sola categoría, sin textos de compatibilidad ni pedidos) no hay propuestas, y eso es lo correcto con esos datos. La tasa de aprobación real se medirá con catálogos reales. Con la llave de Claude se puede sumar la extracción de relaciones leyendo descripciones con el modelo, como propuesta igualmente aprobable.
