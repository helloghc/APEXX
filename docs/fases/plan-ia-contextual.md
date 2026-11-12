# Plan de IA contextual: recuperación precisa en cada panel

Estado: **propuesta**. Nada de esto está construido todavía. Cada fase se cierra con la misma batería de siempre: pruebas automáticas, aislamiento entre tiendas, reversión, recorrido por rol, velocidad y auditoría de uso humano.

## 1. De dónde partimos

Hoy Apexx no llama a ningún modelo de lenguaje. Su IA es determinista y vive en memoria:

- **Búsqueda léxica** con tolerancia a errores de dedo. Aprende de los clics y de lo que se agrega al carrito.
- **Recomendaciones** por coocurrencia de compras y vistas.
- **Servidor MCP** por tienda: un asistente externo busca, cotiza y arma un enlace de carrito, pero nunca crea pedidos.
- **Medición** (servidor de desarrollo, tienda de 1 000 productos):
  - ficha y búsqueda: 0,6 a 2 ms de mediana;
  - catálogo: 7 ms de mediana;
  - percentil 95: menos de 12 ms.

La regla que cuida la velocidad: **el comprador nunca espera a un índice ni a un modelo.** Todo se escribe en Postgres, un evento del outbox actualiza la memoria y la tienda lee de memoria. No hay indexador aparte que se atrase o se rompa, que es justo el problema de VTEX.

El plan conserva esa regla. Toda técnica nueva entra por el mismo camino:

1. Postgres es la fuente de verdad.
2. El outbox (ya sin huecos, desde la migración 154) avisa del cambio.
3. El trabajo pesado, como generar embeddings o extraer relaciones con un modelo, corre fuera de la petición del comprador.
4. La tienda lee el resultado en memoria.

## 2. Principios de diseño

1. **La IA nunca inventa datos de comercio.** Precio, existencias, envío, impuestos y estado del pedido salen siempre de la proyección o de la API, nunca de lo que "recuerde" un modelo. El modelo redacta, ordena, clasifica y propone. El sistema verifica.
2. **Ningún modelo en la ruta caliente de la tienda.** Catálogo, ficha y carrito siguen en milisegundos. Lo que necesita un modelo se calcula al escribir (al guardar un producto) o en paneles donde unos segundos son aceptables (asistentes).
3. **Una sola base.** Los vectores van en Postgres con `pgvector`, con `tenant_id` y RLS forzada como las demás 144 tablas. Para la tienda, una copia cuantizada va en la memoria de la proyección. Sin un servicio vectorial aparte, que sería otro indexador que mantener al día.
4. **Todo lo que la IA cambia se puede revertir.** Cada sugerencia aplicada queda en la auditoría con su origen (modelo, versión del prompt y fuentes), y se puede deshacer como cualquier cambio masivo.
5. **El comerciante decide.** Las escrituras propuestas por IA pasan por aprobación: vista previa, aplicar, deshacer. Cada función se enciende y se apaga por tienda en *Recomendaciones e IA*.
6. **El contenido recuperado es dato, no instrucción.** Descripciones, opiniones, PDF de clientes y correos pueden traer texto malicioso (inyección de instrucciones). Se pasan al modelo como documentos citados, nunca como órdenes. Las herramientas que escriben exigen confirmación.
7. **Privacidad.** No se generan vectores de datos personales (nombres, correos, direcciones). Lo que se indexa es catálogo, contenido y documentos de la tienda. Los resúmenes de clientes se calculan al vuelo y no se guardan.
8. **Costo con tope.** Presupuesto mensual por tienda y por función, caché de prompts y procesamiento por lotes para todo lo que no es interactivo.

## 3. Las técnicas, qué son para Apexx y dónde no usarlas

| Técnica | Para qué sirve aquí | Dónde no conviene |
|---|---|---|
| **Base de datos vectorial** | `pgvector` en Postgres (fuente de verdad, RLS) y un índice en memoria cuantizado a int8 para la tienda. Ejemplo: 50 000 productos × 512 dimensiones × 1 byte ≈ 25 MB por tienda | Un servicio vectorial externo: es otro indexador que se desincroniza |
| **Búsqueda híbrida** | Fusionar el resultado léxico actual con el vectorial (Reciprocal Rank Fusion). Encuentra "algo para correr en lluvia" aunque no comparta palabras con "tenis impermeable" | Reemplazar la búsqueda léxica: para SKU, marca y códigos exactos, la léxica es mejor y más rápida |
| **Re-ranking** | Reordenar los primeros 50 con un reordenador. Señales: coincidencia exacta, conversión aprendida, existencias, margen y fijados del comerciante. Opcionalmente, un modelo de reordenamiento (cross-encoder) medido contra una evaluación | Reordenar en la tienda con un modelo de lenguaje: cuesta cientos de milisegundos |
| **Contextual chunking** (recuperación contextual) | Antes de vectorizar cada fragmento de un documento largo, se le antepone una o dos frases que lo sitúan ("Del manual del Taladro X, sección garantía…"). Se generan con un modelo pequeño, por lotes y con el documento completo en caché | Textos cortos, como un título o una descripción breve: no hay fragmentos que pierdan sentido |
| **GraphRAG** | Un grafo de conocimiento de la tienda con productos, variantes, categorías, marcas, atributos, compatibilidades ("sirve para"), accesorios, sustitutos, colecciones, empresas B2B y sus catálogos. La mitad ya existe en tablas; un modelo propone aristas nuevas leyendo descripciones y fichas técnicas, con confianza y fuente | Preguntas que una consulta directa ya responde mejor (precio, existencias) |
| **CRAG con autocorrección** | Ciclo del asistente: recuperar, evaluar si lo recuperado responde la pregunta, y si no, reescribir la consulta o ir a la fuente estructurada (catálogo, pedido). Responde solo con citas verificadas, y si no encuentra, lo dice. En el flujo de datos: si un embedding falla o queda viejo, el outbox lo regenera, y una verificación periódica compara conteos como la auditoría de escala | Respuestas que no necesitan recuperación |
| **Prompt engineering avanzado** | Prompts de sistema por panel en caché. Salidas estructuradas con esquema JSON para toda acción. Herramientas con esquema estricto. Razonamiento adaptativo del modelo con nivel de esfuerzo por ruta: bajo para clasificar, alto para análisis | Mostrar la cadena de razonamiento al usuario: los modelos actuales no la devuelven en crudo, y lo que se muestra es la respuesta citada |
| **Fine-tuning** | Al final, y solo si una evaluación demuestra que la caché de prompts, los ejemplos y la recuperación no bastan. El candidato más probable es el modelo de embeddings o el reordenador, entrenados con búsquedas y clics reales en español, no el modelo de lenguaje | Fijar el tono de marca o enseñar el catálogo: eso lo resuelven el prompt y la recuperación, y se actualizan al instante sin reentrenar |

### Modelos por tarea (Claude, precios de noviembre de 2026)

| Tarea | Modelo | Por qué |
|---|---|---|
| Asistentes del panel y análisis (reportes, B2B, promociones) | `claude-opus-5-5` | Mejor razonamiento con herramientas. $4 / $20 por millón de tokens |
| Atención al comprador en la tienda, alto volumen | `claude-sonnet-5-5` | Más barato con buena calidad. Se decide midiendo contra la evaluación |
| Contexto de fragmentos, clasificación, extracción de atributos, traducción masiva | `claude-haiku-5-5` por lotes (Batch API, 50 % menos) | Barato y no interactivo |
| Embeddings y re-ranking | Modelo de embeddings dedicado (por ejemplo, Voyage AI o un modelo abierto local) | La API de Claude no es un servicio de embeddings. Se elige con la evaluación de búsqueda en español |

Detalles de la API que el plan respeta:

- **Caché de prompts:** el prompt de sistema y las herramientas van fijos al inicio.
- **Salidas estructuradas:** `output_config.format`.
- **Herramientas:** `strict: true`. Nada de `tool_choice` forzado, porque devuelve 400 en Opus 5.5 y Sonnet 5.5.
- **Razonamiento:** adaptativo; el nivel de esfuerzo se fija por ruta.
- **Rechazos:** respaldo del lado del servidor.
- **Aislamiento:** un prompt y una caché por tienda, para que ninguna caché mezcle datos de dos tiendas.

## 4. Dónde entra cada técnica, panel por panel

Columnas: **V** vectorial · **H** híbrida · **R** re-ranking · **CC** contextual chunking · **G** GraphRAG · **CRAG** · **P** prompt engineering y salidas estructuradas · **FT** fine-tuning.

| Panel o contexto | Función | V | H | R | CC | G | CRAG | P | FT |
|---|---|---|---|---|---|---|---|---|---|
| **Tienda: búsqueda** | Entiende intención ("tenis rojos para correr talla 27 baratos" se vuelve consulta y filtros), sinónimos y búsqueda semántica cuando la léxica da poco | ● | ● | ● | | | | ○ | ○ |
| **Tienda: ficha** | Preguntas sobre el producto, respondidas con descripción, ficha técnica (PDF), opiniones y políticas, con citas | ● | ● | ● | ● | ● | ● | ● | |
| **Tienda: recomendaciones** | Accesorios, compatibles y sustitutos del grafo, más similares por vector para productos nuevos sin ventas | ● | | ● | | ● | | | |
| **Tienda: asistente / MCP** | El MCP actual sigue mandando; las herramientas nuevas responden con grafo y con verificación de existencias y precio | ● | ● | ● | ● | ● | ● | ● | |
| **Catálogo** | Enriquecer: atributos extraídos de la descripción, categoría sugerida, títulos y SEO, texto alternativo de imágenes (visión), detección de duplicados por similitud. Todo como sugerencia aprobable | ● | | | | ● | | ● | |
| **Importar (CSV y VTEX)** | Mapear columnas y categorías externas al árbol de la tienda; detectar variantes mal agrupadas | ● | ● | | | ● | | ● | |
| **Búsqueda y opiniones** | Las búsquedas sin resultados sugieren sinónimos y redirecciones; resumen de opiniones por producto con citas; moderación | ● | ● | | | | ● | ● | |
| **Promociones** | "Arma un 3×2 en calcetas para el Buen Fin": salida estructurada, validada con el simulador existente antes de guardar | | | | | | ● | ● | |
| **Pedidos y devoluciones** | Resumen de la línea de tiempo del pedido, borrador de respuesta al cliente, clasificación del motivo de devolución, señales de riesgo explicadas | | | | | | ● | ● | |
| **Clientes y segmentos** | Segmento en lenguaje natural que se convierte en reglas JSON validadas (sin vectores de datos personales) | | | | | | | ● | |
| **Empresas B2B** | Cotización desde un PDF de solicitud (RFQ): fragmentos con contexto, cada renglón emparejado con su SKU por búsqueda híbrida y grafo, y una propuesta que el representante revisa. Responde con el catálogo y la lista de precios de la empresa | ● | ● | ● | ● | ● | ● | ● | |
| **Marketplace** | Alta de productos del vendedor: mapeo a categorías y atributos, y detección de duplicados del catálogo | ● | ● | | | ● | | ● | |
| **Correos y campañas** | Texto con la voz de la marca y en el idioma del comprador; asuntos alternativos | | | | | | | ● | |
| **Diseño de la tienda** | Textos de secciones, alternativas de banner y texto alternativo | | | | | | | ● | |
| **Reportes** | Preguntas en lenguaje natural sobre la capa de reportes personalizados (nunca SQL libre), con la cifra y el reporte que la produjo | | | | | ● | ● | ● | |
| **Configuración y políticas** | Borradores de políticas y respuestas frecuentes con fuente, marcados "revisar con tu abogado" | ● | | | ● | | ● | ● | |
| **Operación** | Explicar un incidente con el registro de actividad y el estado; sugerir la acción ya documentada | ● | | | ● | | ● | ● | |
| **Ayuda del panel** | Responder "cómo hago X" con la documentación de Apexx, con enlace a la pantalla | ● | ● | ● | ● | | ● | ● | |

● entra en esta fase del plan · ○ opcional, solo si la evaluación lo justifica.

## 5. Arquitectura

```
Escritura (panel, API, import)
   └─ Postgres: producto, documento, opinión… + evento en outbox (seq sin huecos)
        ├─ Proyección (memoria): catálogo, precios, existencias ──► tienda en ms
        └─ Trabajadores de IA (fuera de la ruta del comprador):
              · fragmentar + contexto (Haiku por lotes, caché del documento)
              · embeddings → tabla ai_chunks (pgvector, RLS) → índice int8 en memoria
              · extraer aristas → tabla kg_edges (fuente, confianza, aprobada)
Lectura
   · tienda: léxico + vector en memoria → fusión → re-ranking con señales (ms)
   · asistentes: herramientas del sistema (catálogo, pedidos, simulador, grafo, buscar_documentos)
                 → CRAG: evaluar, corregir y citar → respuesta estructurada (segundos)
```

**Tablas nuevas, todas con `tenant_id` y RLS forzada:**

| Tabla | Contenido |
|---|---|
| `ai_chunks` | fuente, fragmento, contexto, vector, versión del modelo, hash |
| `kg_edges` | origen, relación, destino, confianza, fuente, estado |
| `ai_suggestions` | lo propuesto, quién lo aprobó y cómo se deshace |
| `ai_usage` | tokens y costo por tienda y función |

**Recuperación de fallas (self-healing):**

- Cada fragmento guarda el hash de su fuente. Si la fuente cambia, el evento del outbox lo marca para regenerar.
- Una verificación periódica compara fuentes contra fragmentos. Es la misma idea de la auditoría de escala, que encontró productos borrados todavía visibles.
- Si el proveedor de embeddings falla, la tienda sigue con la búsqueda léxica (degradación elegante), y la cola reintenta sola.

## 6. Fases

| Fase | Contenido | Se cierra cuando |
|---|---|---|
| **IA-0 Cimientos** | `pgvector`; tablas con RLS; flujo de embeddings por el outbox; índice en memoria; presupuestos y apagado por tienda; arnés de evaluación (búsquedas reales etiquetadas en español) | Aislamiento de las tablas nuevas; borrar un producto borra sus vectores; 50 000 productos sin perder velocidad |
| **IA-1 Búsqueda híbrida y re-ranking** | Fusión léxico + vector; intención a filtros; reordenador; sugerencias desde búsquedas sin resultados | Mejora medida de nDCG@10 y de búsquedas con resultado; percentil 95 de búsqueda menor a 15 ms con 50 000 productos |
| **IA-2 Contextual chunking** | Fichas técnicas en PDF, políticas, blog y ayuda del panel; preguntas en la ficha con citas | Fidelidad de respuesta (solo afirma lo que está en las fuentes) ≥ 95 % en la evaluación |
| **IA-3 CRAG en asistentes** | Asistente del comprador y copiloto del panel con herramientas, verificación y citas | Cero precios o existencias inventados en la evaluación; responde "no sé" cuando no hay fuente |
| **IA-4 GraphRAG** | Grafo de compatibles, accesorios y sustitutos; extracción aprobable; recomendaciones y B2B (RFQ a cotización) | Aristas con fuente; la tasa de aprobación del comerciante mide la calidad |
| **IA-5 Copilotos por panel** | Catálogo, importación, promociones, pedidos, reportes, correos, diseño | Cada sugerencia se puede revertir y queda en la auditoría |
| **IA-6 Decisión de fine-tuning** | Solo si una evaluación lo justifica; primero embeddings y reordenador | Ganancia medida contra el costo de mantener el modelo |

## 7. Pruebas que se vuelven a correr en cada fase

- **Suite completa** de Go y del panel, más pruebas nuevas por función.
- **Aislamiento:** la auditoría de tablas incluye las tablas nuevas automáticamente. Una búsqueda vectorial nunca devuelve fragmentos de otra tienda.
- **Reversión:** aplicar y deshacer cada sugerencia de IA deja la base igual a la foto inicial (mismo arnés de la simulación de escala).
- **Escala:** 1 000, 10 000 y 50 000 productos con vectores. Medir memoria, latencia, propagación de un cambio hasta la búsqueda semántica, y borrado total sin restos.
- **Evaluación de calidad:**
  - búsqueda: nDCG@10, recall@50, porcentaje de búsquedas sin resultado;
  - respuestas: fidelidad a la fuente y citas correctas;
  - inyección de instrucciones en descripciones y opiniones: el asistente no obedece.
- **Recorrido por rol:** quien no tiene permiso no ve ni usa el copiloto de ese panel.
- **Costo por tarea completada**, con tope por tienda.

## 8. Lo que este plan no promete

- Que la IA vuelva a Apexx mejor por sí sola. Cada técnica entra solo si su evaluación mejora contra la versión sin IA.
- Tiempos de respuesta de milisegundos en los asistentes: ahí se miden segundos. La tienda sí se queda en milisegundos.
- Fine-tuning desde el inicio.
