# Fases de Apexx: índice

Cada documento dice qué se construyó, cómo se probó y qué quedó pendiente, con cifras medidas.

## Construcción del sistema

| Documento | Contenido |
|---|---|
| [fase-1-fundaciones.md](fase-1-fundaciones.md) | Datos, aislamiento por tienda (RLS), identidad y resiliencia |
| [fase-2-pedidos-pagos.md](fase-2-pedidos-pagos.md) | Pedidos, pagos con Stripe y reversión |
| [fase-3-builder.md](fase-3-builder.md) | Builder visual y storefront |
| [fase-4-importador.md](fase-4-importador.md) | Importador VTEX → Apexx |
| [fase-5-conectores.md](fase-5-conectores.md) | Eventos firmados, reintentos y repetición |
| [fase-6-despliegue.md](fase-6-despliegue.md) | Despliegue en Google Cloud y autorecuperación |
| [fase-7-paneles-A-L.md](fase-7-paneles-A-L.md) | Paneles completos (sub-fases A a L) |
| [etapa-0-cierre.md](etapa-0-cierre.md) | Cierre de pendientes |
| [etapa-3-b2b.md](etapa-3-b2b.md) | B2B profundo |
| [etapa-4-logistica-pagos.md](etapa-4-logistica-pagos.md) | Logística y pagos conectados |
| [etapa-5-tipos-producto.md](etapa-5-tipos-producto.md) | Tipos de producto |
| [etapa-6-extensibilidad.md](etapa-6-extensibilidad.md) | Extensibilidad |
| [etapa-7-canales-contenido-lealtad.md](etapa-7-canales-contenido-lealtad.md) | Canales, contenido y lealtad |
| [etapa-8-b2b-avanzado.md](etapa-8-b2b-avanzado.md) | B2B avanzado |
| [etapa-9-ia-agentes.md](etapa-9-ia-agentes.md) | Primeros agentes de IA (MCP, recomendaciones) |

## Escala y despliegue sin corte

| Documento | Contenido |
|---|---|
| [../audit/escala-50k.md](../audit/escala-50k.md) | Simulación con 1 000, 10 000 y 50 000 productos, con reversión total |
| [../audit/carga-paneles.md](../audit/carga-paneles.md) | Carga de cada vista del panel, en local y con red real, con 120 y 50 000 productos |
| [../deploy.md](../deploy.md) §7–7.2 | Varios nodos, despliegue sin corte y variables de IA |

## Plan de IA

| Documento | Contenido |
|---|---|
| [resumen-ia.md](resumen-ia.md) | **Empieza aquí:** estado y cifras de cada fase, y la medición final con 50 000 productos |
| [plan-ia-contextual.md](plan-ia-contextual.md) | El plan: técnicas, dónde entra cada una, arquitectura y pruebas |
| [plan-ia-apexx.html](plan-ia-apexx.html) | Página con el avance por fase (lo cerrado y lo que falta) |
| [ia-0-ia-1-resultados.md](ia-0-ia-1-resultados.md) | Vectores por el outbox y búsqueda híbrida |
| [ia-2-resultados.md](ia-2-resultados.md) | Fragmentos con contexto y «Pregúntale a la tienda» |
| [ia-3-resultados.md](ia-3-resultados.md) | Asistente del comprador, copiloto del panel y resumen de opiniones |
| [ia-4-resultados.md](ia-4-resultados.md) | Grafo de productos, fichas técnicas en PDF y cotización desde una solicitud |
| [ia-5-resultados.md](ia-5-resultados.md) | Copilotos por panel con sugerencias aplicables y reversibles |
| [ia-6-decision.md](ia-6-decision.md) | Decisión de fine-tuning y medición por tienda |

## Etiquetas y siguiente fase

| Documento | Contenido |
|---|---|
| [../etiquetas.md](../etiquetas.md) | Etiquetas y píxeles: catálogo de proveedores, verificación de dominio, WhatsApp, historial y publicación al instante |
| [plan-sandbox.md](plan-sandbox.md) | **Siguiente fase:** plan del sandbox por tienda (copia idéntica, URL propia, credenciales aisladas, pasar a producción y revertir) |

## Pruebas

- Guía de QA para personas: [../qa.md](../qa.md). La sección 8 cubre la IA.
- Herramientas automáticas (recorrido del panel, escala, relevo de nodos): [../../tools/qa/README.md](../../tools/qa/README.md).
- Evaluaciones de calidad en el código: `TestSearchEvaluation` (búsqueda), `TestAskEvaluation` (respuestas) y `TestHybridSearchCost50k` (`APEXX_SCALE=1`, la compuerta de 15 ms).
