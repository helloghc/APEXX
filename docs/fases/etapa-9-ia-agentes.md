# Etapa 9 — Inteligencia artificial y agentes (noviembre 2026)

Estado: **hecha**. Cada función tiene una prueba de integración. También se recorrió en el navegador (panel en escritorio, tienda en teléfono, un asistente que llama al servidor MCP) y se midió su velocidad.

| Función | Dónde | Prueba |
|---|---|---|
| Recomendaciones «Otros clientes también eligieron» en la ficha y el carrito, a partir de compras y vistas | Ficha de producto; carrito | `TestRecommendationsAndLearningSearch` |
| Por qué se recomienda cada producto (pedidos y visitas), productos excluidos, encender y apagar | Búsqueda y opiniones › Recomendaciones e IA | `TestRecommendationsAndLearningSearch` |
| Búsqueda que aprende de lo que los clientes abren y agregan al carrito, debajo de las reglas del comerciante | Búsqueda › Reglas › Aprender de tus clientes; vista previa con «Elegido N×» | `TestRecommendationsAndLearningSearch` |
| Servidor MCP por tienda: buscar, ver producto, cotizar con envío y crear un enlace de carrito | Búsqueda y opiniones › Recomendaciones e IA › Asistentes de IA | `TestMCPServer` |
| Enlace de carrito del asistente: la tienda lo carga y avisa que hay que revisarlo antes de pagar | Carrito | QA en navegador |
| Índice de recomendaciones y pesos de búsqueda | `internal/recs` | `TestBuildRanksBuysOverViews`, `TestBasketDedupesAndCaps` |

## Decisiones de diseño y seguridad

- **Sin datos personales.** El navegador manda solo pares de productos vistos en la misma visita (se guardan en la pestaña, no en una cookie) y el producto que se eligió tras una búsqueda. No se envía ningún identificador. Se guardan solo conteos por tienda, con RLS.
- **En memoria, sin servicio aparte.** Cada 20 s se guardan los conteos y se reconstruye el índice, solo para las tiendas que tuvieron pedidos, vistas o cambios. Una compra se refleja en menos de un minuto. Un pedido cancelado o devuelto deja de contar en la siguiente ronda. La ficha consulta el índice en memoria, sin tocar la base de datos.
- **Explicable.** Un pedido con ambos productos pesa como cinco visitas con ambos. Una compra grande cuenta solo sus primeros 20 productos, para que un pedido al mayoreo no domine.
- **La búsqueda aprende con tope.** El refuerzo crece con el logaritmo de los clics más tres veces lo que se agrega al carrito, y se detiene en 40. Puede reordenar productos que coinciden con la búsqueda, pero nunca gana a uno fijado (1000) ni muestra uno oculto. Se apaga con una casilla.
- **MCP con el cliente al mando.** Está apagado hasta que la tienda lo enciende. Usa el transporte HTTP del protocolo con respuestas JSON y versiones 2025-06-18, 2025-03-26 y 2024-11-05. Ninguna herramienta crea pedidos ni cobra: la última solo da un enlace al carrito y el cliente paga en el pago de siempre, con sus límites y antifraude. Los argumentos se validan de forma estricta y los errores regresan como errores de herramienta que el modelo puede leer y corregir. Se rechazan peticiones con `Origin` de otro sitio, para que una página ajena no lo use desde el navegador de un cliente. Hay límite por IP y un tope de 64 KB por petición. Se respetan los productos ocultos y fijados de la tienda.

## Defectos que encontraron las auditorías (todos corregidos)

- Los cambios en las reglas de búsqueda llegan a la proyección de forma asíncrona: la prueba ahora espera a que se apliquen antes de revisar el orden.
- Dos nombres de funciones auxiliares ya existían en el paquete y se renombraron.
- El panel decía «1 términos»; ahora muestra «búsquedas aprendidas: 1».

## Velocidad medida (servidor de desarrollo)

- 9 rutas de la etapa (ficha y carrito con recomendaciones, búsqueda, panel): mediana de medianas **1.6 ms**, peor p95 **3.7 ms**, sin errores.
- Herramientas MCP: buscar 2.8 ms (p95 4.3), ver producto 1.4 ms, cotizar con envío 1.4 ms, enlace de carrito 1.2 ms.

## Lo que queda fuera de este entorno

- Conectar asistentes comerciales reales (Claude, ChatGPT) a una tienda publicada en internet. Aquí se probó con un cliente MCP que sigue la especificación.
