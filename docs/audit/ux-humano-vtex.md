# Auditoría de uso real — VTEX como comprador y como operador (cuenta `elpaps`)

> Fecha: 2026-11-10 · Alcance: navegación como una persona real, **sin guardar nada** en el admin y **sin crear pedidos**. Solo se usaron datos de prueba (`test@example.com`, "Prueba Auditoria").
> Tienda demo protegida con login de administrador. Capturas y datos crudos fuera del repositorio.

## 1. Método

| Recorrido | Dispositivo | Red | Corridas |
| --- | --- | --- | --- |
| Comprador | Playwright "Pixel 5", locale `es-MX`, pausas humanas (escritura a 120 ms por tecla) | Slow 4G emulada (150 ms RTT, 1,6 Mbps) + CPU 4× | 1 recorrido completo + 2 exploraciones del DOM |
| Operador | Chromium escritorio 1440×900 | Sin limitación | 2 recorridos por el menú real del admin |

Cada paso mide el tiempo hasta que la persona puede hacer lo siguiente, los toques, las peticiones de red y los errores de consola. Se describe lo que vería una persona.

**Limitaciones:** un recorrido de comprador es una sola muestra, no una mediana. El reCAPTCHA del checkout detectó la automatización ("La verificación ha expirado"), así que **no se llegó a los pasos de envío y pago**; esos pasos están medidos en `checkout-payments.md`. Los terceros (analítica, chat) quedaron bloqueados por la red de la auditoría.

## 2. Recorrido del comprador (móvil)

| # | Paso | Tiempo | Toques | Peticiones | Errores de consola | Qué vería una persona |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| 1 | Abrir la home | 2,8 s (8,0 s en frío) | — | 68 | 4 | La página no tiene `<title>`. El encabezado tiene 2 íconos (menú y carrito) **sin nombre accesible** |
| 2 | Buscar "convrse" (con error) | — | 1 | — | — | **No hay buscador en móvil**: ni en el encabezado ni dentro del menú. El único campo de texto de la home es el del newsletter |
| 2b | Resultados de "convrse" (entrando por URL) | 13,6 s | — | 173 | 12 | 42 productos, los mismos que "converse": la tolerancia a errores funciona. El autocompletar devolvió 0 sugerencias para "sh" (auditoría de backend) |
| 3 | Filtrar | 9,5 s | **5** | 23 | 3 | Abrir "FILTRAR", expandir el grupo, marcar la opción y "APLICAR". De 42 a 26 productos. La URL queda como `?_q=converse&fuzzy=0&initialMap=ft&initialQuery=converse&map=category-1,ft&operator=and` |
| 4 | Abrir la ficha | 5,1 s | 1 | 132 | 8 | **2 `h1`**: el nombre del producto y "Te recomendamos" |
| 5 | Elegir variante | — | — | — | — | El producto tiene un solo SKU y no hay selector. Correcto |
| 6 | Agregar al carrito | 3,6 s | 1 | 35 | 1 | Se abre el minicarrito con un selector 0–10 ("0 - Retirar"). **El nombre cambia**: la ficha dice "…All Star **Mjr**" y el minicarrito "…All Star **Jr**" |
| 7 | Abrir el carrito | 3,2 s | 1 | 65 | 12 | **Imagen del producto rota** y un spinner que no termina |
| 8 | Subir la cantidad a 2 | API 253 ms | 1 | 26 | **21** | La línea muestra el precio unitario, no el subtotal de la línea |
| 9 | Cupón falso "TEST10" | 10 s | 2 | 4 | 3 | Mensaje "No hay descuentos disponibles. Consulta las condiciones.", pero el código **queda mostrado como aplicado** (TEST10 con ✕). No dice que el cupón no existe |
| 10 | Código postal 53129 | — | — | — | — | **No hay calculadora de envío en el carrito.** El "Total" ($3,368.76) no incluye envío y no lo dice |
| 11 | "Proceder al pago" → Identificación | 30 s+ | 4 | — | — | Un solo formulario con correo, nombre, apellidos y teléfono. El botón dice "**Ir para la Entrega**" (calco del portugués "Ir para a Entrega") |
| 12 | Continuar a entrega | — | — | — | — | Bloqueado por reCAPTCHA en la automatización: "La verificación ha expirado. Verifique nuevamente el campo." Una persona podría ver el mismo mensaje si tarda en llenar el formulario |

**Totales hasta la identificación:** unos 150 s, unos 18 toques, más de 560 peticiones y **más de 80 errores de consola**.

### Accesibilidad de la ficha (escaneo automático)

| Métrica | Valor |
| --- | ---: |
| Imágenes sin `alt` | 0 de 17 |
| Controles sin nombre accesible | 5 de 43 |
| Áreas táctiles menores de 24 px | **19** |
| Campos sin etiqueta | 1 de 2 |
| `lang` | `es-MX` |

### Ciclo de vida del carrito y reversión

| Momento | Stock total | Reservado |
| --- | ---: | ---: |
| Antes de agregar | 20 | 6 |
| Con 2 unidades en el carrito | 20 | **6** (sin cambio) |

**El carrito no reserva stock.** Solo se reserva al crear el pedido. Entre "agregar" y "pagar", otro comprador puede llevarse las unidades, y el primero lo descubre en el pago. Las 6 unidades reservadas corresponden a pedidos `ready-for-handling` que siguen reteniendo stock.

### Página inexistente

`/producto-que-no-existe-xyz/p` devuelve un **404 correcto**, pero la página queda **en blanco**: sin mensaje, sin buscador y sin productos sugeridos. TTFB 0,6 a 3,0 s.

## 3. Recorrido del operador (admin, escritorio)

| Pantalla | Carga | Red inactiva | Peticiones | Peticiones fallidas | Observación |
| --- | ---: | ---: | ---: | ---: | --- |
| Inicio del admin | 8,3 s | 12,1 s | 76 | 6 | |
| Productos (URL antigua `/admin/Site/Produto.aspx`) | 8,7 s | 9,3 s | 75 | 7 | **Error 404 del admin servido con HTTP 200**. Los enlaces y la documentación antiguos ya no funcionan |
| Edición de producto antigua (`ProdutoForm.aspx`) | 4,6 s | 6,4 s | 75 | 8 | Igual: 404 dentro del admin |
| Productos y SKUs (menú actual) | — | **50 s** | — | — | Menú en inglés en esta sesión ("Products and SKUs"); en otra sesión, en español |
| Lista de precios | — | **52 s** | — | — | **Pantalla en blanco** tras 52 s. La configuración de pricing tiene `migrationStatus: "Error"`; posible relación, no confirmada |
| Gestión del stock | — | — | — | — | No hay enlace directo desde el menú principal; está dentro de Catálogo → Stock |
| Todos los pedidos | 4,0 s | 9,1 s | 104 | **16** | El filtro por defecto "Creados: hoy" muestra "No se encontraron pedidos" aunque la cuenta tiene 2 316 |
| Site Editor | 4,1 s | 17,6 s | **238** | 13 | **"Estás editando la tienda en producción. Los cambios en el contenido tendrán un efecto inmediato."** |
| Historial de indexación | 3,5 s | 52 s | — | — | Primer dato visible a los 5,7 s; promedio de indexación de 6 min 37 s |
| Promociones | 2,0 s | 4,2 s | 40 | 7 | |

Cada pantalla vuelve a cargar el shell completo del admin (unas 75 peticiones). La interfaz pide traducciones `es-AR` y `en-US` aunque la tienda es `es-MX`. Hay 6 a 16 peticiones fallidas por pantalla.

### Tareas comunes: pantallas y clics (estimado a partir del menú observado)

| Tarea | Ruta en VTEX | Pantallas | Clics aprox. | Riesgo |
| --- | --- | ---: | ---: | --- |
| Cambiar un precio | Catálogo → Lista de precios → buscar SKU → editar → guardar | 3 | 6–8 | La pantalla no cargó en esta cuenta |
| Cambiar stock | Catálogo → Gestión del stock → buscar SKU → editar cantidad por bodega → guardar | 2 | 5–7 | Se ve en la tienda cuando el indexador procesa el producto (6 min 37 s en promedio) |
| Mover un banner de la home | Storefront → Site Editor → página → buscar el bloque entre nombres genéricos ("Fila", "Texto RTF", "Lista de productos") → editar | 2 | 6–10 | Solo 6 de 601 bloques de la home son editables ahí (auditoría del builder). El resto requiere cambiar el tema y redesplegar (1 a 3 horas de un desarrollador). **Los cambios van directo a producción** |

## 4. Hallazgos ordenados por fricción

| # | Hallazgo | Evidencia | Decisión de Apexx |
| --- | --- | --- | --- |
| H1 | **Sin buscador en móvil** | 0 campos de búsqueda en la home ni en el menú | La sección de encabezado incluye búsqueda siempre visible en móvil; el builder no permite publicar un encabezado sin búsqueda |
| H2 | **Site Editor edita producción en vivo** | Aviso de "efecto inmediato"; sin borrador | Borrador, vista previa, publicación atómica en 0,7 ms y rollback en 1 clic (ya en el repositorio) |
| H3 | **El carrito no reserva stock** | Reservado 6 → 6 con 2 unidades en el carrito | Reserva blanda con vencimiento al pasar al checkout; reserva firme al pagar; liberación automática (ya en `inventory`) |
| H4 | **El total del carrito no incluye envío ni hay calculadora** | Total $3,368.76 sin envío | Calculadora de envío por código postal en el carrito; `totals.grand` siempre incluye envío (ya en `cart`) |
| H5 | **Cupón inválido ambiguo** | "No hay descuentos disponibles" + código mostrado como aplicado | Error específico ("El cupón TEST10 no existe"), sin aplicarlo, con la explicación del motor de promociones |
| H6 | **Cambios de stock y precio tardan minutos en verse** | Promedio de 6 min 37 s; hasta 21 min | Proyecciones sin indexador, < 2 s (`indexing.md`) |
| H7 | **Pantallas del admin lentas, en blanco o 404** | Precios en blanco a los 52 s; URLs antiguas → 404 con HTTP 200; 6 a 16 peticiones fallidas por pantalla | Admin SPA con rutas estables y redirecciones permanentes; presupuesto de 2 s por pantalla en CI |
| H8 | **Nombres inconsistentes** | "Mjr" en la ficha y "Jr" en el carrito | Un solo campo de nombre para mostrar por variante, usado en todas las superficies |
| H9 | **Imagen rota y spinner infinito en el carrito** | Captura del paso 7 | Imágenes del mismo pipeline (imgproxy) en todas las superficies, con *placeholder* y tamaño fijo |
| H10 | **Accesibilidad** | 2 íconos del encabezado sin nombre; 19 áreas táctiles < 24 px; 2 `h1`; home sin `<title>` | El builder valida nombre accesible, 44 px mínimos, un solo `h1` y título obligatorio antes de publicar |
| H11 | **Idioma mezclado** | "Ir para la Entrega"; admin en es/en según la sesión; errores de logística en portugués | Textos del sistema por locale de la tienda, con prueba en CI que detecta cadenas de otro idioma |
| H12 | **404 en blanco** | Página sin mensaje | 404 con buscador y productos populares, generado por el builder |
| H13 | **Filtros con 5 toques y URLs ilegibles** | URL con 6 parámetros técnicos | Filtros que se aplican al tocar, URLs legibles (`/c/tenis?marca=converse`) |
| H14 | **Más de 80 errores de consola en un recorrido** | Contados por paso | Presupuesto de 0 errores de consola en las pruebas E2E del storefront |
| H15 | **Pedidos: el filtro por defecto oculta todo** | "Creados: hoy" → "No se encontraron pedidos" | Vista por defecto con los pedidos que requieren acción, sin importar la fecha |

## 5. Qué hace bien VTEX y Apexx debe igualar

- La búsqueda tolera errores de escritura ("convrse" encuentra lo mismo que "converse").
- El checkout lee el inventario real (372 de 372 SKUs coinciden).
- El 404 HTTP es correcto y todas las imágenes de la ficha tienen `alt`.
- El historial de indexación existe, aunque el comerciante no ve ahí la frescura de cada superficie.
