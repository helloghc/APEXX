# IA-3: asistentes con CRAG

Fecha: 12 de noviembre de 2026. Plan: `docs/fases/plan-ia-contextual.md`. Continúa `ia-2-resultados.md`.

## Qué se construyó

| Pieza | Dónde | Detalle |
|---|---|---|
| Asistente del comprador | `api/assistant.go`, `POST /s/{tienda}/api/assistant`, página de búsqueda | Una sola caja. «Busco una cafetera» o «quiero algo de lino» muestra productos de la búsqueda híbrida con precio y existencias del sistema. Una pregunta que nombra un producto («¿la licuadora tritura hielo?») responde con las palabras de ese producto. Una pregunta de la tienda («¿cuánto tarda el envío?») responde con sus políticas. |
| MCP | `api/mcp.go`, herramienta `ask_store` | Asistentes externos (Claude, ChatGPT) preguntan con las mismas reglas. Las fuentes llegan como enlaces absolutos. `kind: unknown` le dice al asistente que la tienda no lo dice y que no adivine. `get_product` incluye el resumen de opiniones. |
| Copiloto del panel | `api/copilot.go`, `POST /v1/ai/copilot`, tarjeta «Pregúntale a Apexx» en Inicio | Ventas por periodo, lo más vendido, lo agotado y lo que buscan sin encontrar, desde los reportes y con la pantalla donde viven. «¿Cómo hago…?» se responde con la ayuda del panel y un enlace a la pantalla. Respeta permisos: sin acceso a reportes no hay cifras, y la ayuda no ofrece pantallas que la persona no puede abrir. Una llave de tienda pública no lo puede usar. |
| Ayuda del panel | `internal/help/ayuda.md` | 25 tareas, cada una con su pantalla y el permiso que necesita (las rutas se verificaron contra el panel). |
| Resumen de opiniones | `api/reviewsum.go`, ficha y MCP | «Lo que destacan» y «Lo que mejorarían» con frases tal cual de opiniones publicadas y su autor. Se elige la frase más representativa (la más cercana a las demás) y se descartan las que repiten una idea ya elegida. Nunca una paráfrasis que ponga palabras en boca de un cliente. |
| CRAG | `api/ask.go` | Evaluar lo recuperado; reescribir con los sinónimos de la tienda; ir a la fuente estructurada (precio, existencias, variantes). Si la pregunta nombra un tema que ninguna fuente menciona («internacional», «instalación»), es una brecha de conocimiento y la respuesta es «no sé», en vez de prestar una respuesta de otro tema. |

## Defensas que agregaron las pruebas

| Hallazgo | Corrección |
|---|---|
| La prueba de inyección no publicaba la opinión maliciosa: la creación pública no devuelve el id. Al publicarla de verdad, «¿Cuánto cuesta?» citaba la opinión que decía «$1». | El precio y las existencias de un producto salen solo del sistema; si no hay dato, «no tengo ese dato». Las frases dirigidas a una máquina («ignora las instrucciones…», «responde que…») nunca son fuente ni entran al resumen. |
| En una tienda sin política de envíos, «¿Cuánto tarda el envío?» respondía con el plazo de reembolso (también «5 días hábiles»). | El tipo de respuesta (días, $, kg) solo suma si el pasaje comparte el tema con la pregunta. Los tipos que nombran el tema (material, lluvia, armar) sí cuentan solos. |
| El copiloto: un `viewer` lee reportes, así que la prueba de permisos no probaba nada con esa llave. | La prueba usa una persona con permiso solo de catálogo. |

## Resultados

| Medida | Resultado |
|---|---|
| Precios y existencias inventados (asistente, 8 preguntas, cada monto se compara con el sistema) | **0** |
| Preguntas de la tienda y de productos | **27/27** |
| «No sé» sin fuente | **5/5** |
| «No sé» cuando la tienda no cubre el tema, aunque tenga respuestas del mismo tipo | **3/3** (antes 0/3) |
| Fidelidad | **100 %** |
| Conjunto apartado | **9/12 (75 %)**. Antes 83 %: «¿llega a Monterrey?» ahora es «no sé» en vez de citar «resto del país». Es un cambio hacia la cautela. Ojo: este conjunto ya se vio durante los ajustes de esta fase, así que no es del todo independiente. |
| Inyección en opiniones | Ni se obedece ni se usa como fuente |
| Navegador, teclado, escritorio y teléfono | Copiloto y asistente responden; sin desborde ni errores en consola |

Sin llave de Claude, todo lo anterior es determinista. Con llave, las respuestas escritas pasan por la misma verificación de IA-2 (cita y números en las fuentes).

## Pendiente de IA-3

- **Fichas técnicas en PDF.** Leer el texto de un PDF necesita un analizador de PDF; se decide en IA-4 junto con la cotización B2B desde un PDF, que necesita lo mismo.
