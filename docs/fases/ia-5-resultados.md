# IA-5: copilotos por panel

Fecha: 12 de noviembre de 2026. Plan: `docs/fases/plan-ia-contextual.md`.

Regla de todos los copilotos: proponen en la estructura del propio sistema, validada por el sistema, con el porqué. El comerciante decide. Lo que se aplica queda en la auditoría y se puede deshacer.

| Panel | Copiloto | Cómo se aplica y se deshace |
|---|---|---|
| **Catálogo** (editor de producto y «Sugerencias para tu catálogo») | Atributos que el texto ya dice («Playera roja» da Color: Rojo, con la palabra que lo dice), categoría para productos sin una (la que comparten los más parecidos, con el conteo), título y descripción para buscadores, y posibles duplicados (≥ 97 % de parecido y mismas palabras). Botón «Revisar el catálogo» para todo el catálogo. | `ai_suggestions` guarda la propuesta, el valor anterior y el motivo. «Aplicar» escribe solo ese campo, protegido por la versión del producto. «Deshacer» restaura el valor anterior solo si nadie lo cambió después; si alguien lo cambió, responde 409 en vez de borrar esa edición. Lo rechazado no se vuelve a proponer. Los duplicados solo informan. |
| **Imágenes** (en el mismo copiloto del catálogo) | El texto alternativo es obligatorio en Apexx, así que las importaciones lo llenan con lo que traen: «IMG_4021.JPG», «foto», el mismo texto en todas. Se detecta y se propone uno a partir del producto («Lámpara de escritorio LED, vista 2»). | Se escribe en la biblioteca de medios (auditado) con el texto anterior guardado; deshacer lo restaura si nadie lo cambió. |
| **Importar** | Lee las columnas con los nombres que usa la gente y otras plataformas («Nombre», «Precio», «Existencias», «Código», «Variant Price», «Body (HTML)»…), lee como atributo una columna con el nombre de un atributo («Material»), y sin columna handle lo toma del nombre. | La vista previa dice cada mapeo («La columna "Nombre" se leyó como "title"»). Nada se escribe hasta «Aplicar», como siempre. Los archivos con columnas canónicas se leen igual que antes. |
| **Promociones** | «3x2 en calcetas», «20% en tenis hasta el 30 de noviembre», «envío gratis desde $1,500», «15% en toda la tienda con el cupón BUENFIN» se vuelven una promoción. El motor de promociones la valida y la resume con sus propias palabras. | «Crear como borrador» la crea **inactiva** por la ruta normal (auditada). Se activa o se borra desde su pantalla. |
| **Clientes y segmentos** | «compraron más de 3 veces y gastaron más de $2,000», «no compran desde hace 3 meses y aceptan correos» se vuelven reglas del segmento, con cuántos clientes entran hoy. | Abre el diálogo de segmento ya lleno: el comerciante revisa, nombra y guarda (auditado). |
| **Pedidos y devoluciones** | Resumen del pedido, respuesta al cliente según su estado (con su nombre, sin datos inventados) y motivo de devolución leído de las palabras del cliente (talla, dañado, otro producto, no como se describía, tarde, cambio de opinión). | Solo se lee y se copia; no cambia el pedido. |
| **Reportes** | El copiloto de IA-3: ventas, más vendidos, agotados y búsquedas sin resultado, según permisos. | Solo lectura. |
| **Correos y diseño** | Asuntos y textos en la voz de la tienda, con Claude. | **Necesita la llave de Claude.** Sin ella lo dice y no ofrece plantillas disfrazadas de escritura. Con ella, las cifras que el modelo agregue y que el comerciante no dio se marcan para revisar. El uso se registra y respeta el presupuesto. |

## Hallazgos

| Hallazgo | Corrección |
|---|---|
| «compraron más de 3 veces» se leía como un gasto de $3. | El gasto exige hablar de gasto o de un monto con $. |
| La normalización de texto quitaba «$» y «%», así que «20%» nunca se habría leído. La primera prueba pasó porque el patrón aceptaba un $ opcional. | Normalizador propio de los copilotos que conserva $ y % y quita separadores de miles. |
| «hasta el 30 de noviembre» cerraba a las 17:59: se usaba 23:59 UTC. | Fin del día en la zona de la tienda (centro de México); la prueba exige 23 h en esa zona. |
| La sugerencia de atributo citaba la opción («Rojo») en vez de la palabra del texto («roja»). | El motivo cita la palabra que el texto realmente dice. |

## Pruebas

- `TestCatalogCopilot`: aplicar solo cambia el atributo; deshacer lo regresa; SEO aplicado y luego editado a mano no se deshace (409); 3 pasos auditados con la cadena íntegra; una llave pública no ve sugerencias; otra tienda no puede aplicarlas.
- `TestCatalogCopilotRejectAndDuplicates`: duplicado detectado, no aplicable, rechazado y no vuelto a proponer; una segunda revisión del catálogo no agrega nada.
- `TestCatalogCopilotCategory`: la categoría de los 4 más parecidos, aplicada.
- `TestCatalogCopilotAltText`, `TestUselessAlt`: textos alternativos sin significado detectados, aplicados en la biblioteca de medios y deshechos.
- `TestImportMapsColumnNames`: hoja con nombres comunes, atributo por nombre y sin handle; archivo canónico sin cambios.
- `TestParseSegment`, `TestParsePromotion`, `TestClassifyReturn`: lenguaje a estructura.
- `TestPanelCopilots`: segmento con conteo y aceptado por la ruta normal; promoción validada, creada como borrador; resumen y respuesta de un pedido real; permisos y aislamiento.
- `TestWriteCopilot`: sin modelo lo dice; con modelo (servidor de prueba) da opciones, marca la cifra inventada y registra el uso.
- Navegador, escritorio y teléfono, solo con teclado: editor de producto, sugerencias del catálogo, segmento, promoción y pedido, sin desborde ni errores en consola.
