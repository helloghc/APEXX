# Auditoría exhaustiva del panel, los portales y el uso solo con teclado

Fecha: 12 de noviembre de 2026. Navegador: Chromium (escritorio 1366×900 y emulación de iPhone 13).

Esta auditoría cubre lo que las anteriores no habían cubierto:

- cada botón, menú, diálogo y campo del panel;
- datos vacíos y erróneos en cada formulario;
- pantallas vacías y estados de error;
- todos los roles, incluidos los que viven fuera del equipo (vendedor, representante y comprador B2B);
- tareas completas solo con teclado;
- una revisión de UX de punta a punta como comerciante nuevo.

## 1. Cómo se probó

### Recorrido automático (`exhaustivo.js`)

Para cada rol, el recorrido visita todas las rutas del menú y de sus pestañas. En cada pantalla hace esto:

1. **Carga.** Registra excepciones, errores de consola, respuestas 5xx, avisos de error, pantallas en blanco, desborde horizontal, falta de `<h1>` y "Cargando…" que no termina.
2. **Teclado.** Hasta 60 Tab. Marca cada parada de foco invisible, sin nombre accesible o sin anillo de foco.
3. **Cada botón** (hasta 45 por pantalla).
   - Si abre un diálogo, comprueba que el foco entre al diálogo y quede atrapado en él, y que Esc lo cierre.
   - Envía el diálogo vacío y luego con datos erróneos:
     - correo inválido;
     - número negativo enorme;
     - texto en un campo numérico;
     - URL `javascript:`;
     - `<script>` e `<img onerror>` seguidos de 300 caracteres.
   - Marca las acciones destructivas que no piden confirmación.
4. **Formularios de la pantalla.** Los guarda vacíos y luego con datos erróneos. Recarga la pantalla para verificar que lo guardado se muestre escapado y nunca se ejecute.

### Pruebas dirigidas

- Portal del comprador B2B: cada pestaña, sus formularios con datos vacíos y erróneos, teclado, y una compra a crédito completa.
- Portal del vendedor: aislamiento y envío de pedido.
- Representante de ventas: compra en nombre de la empresa, más la prueba negativa de los demás roles.
- Tareas solo con teclado:
  - alta de producto en el panel;
  - compra en la tienda.
- Revisión de UX pantalla por pantalla en la tienda vacía, como comerciante nuevo.

## 2. Resultado por rol (pasada final)

| Rol | Rutas | Botones | Diálogos | Defectos reales que quedan |
|---|---|---|---|---|
| Dueño, tienda vacía (escritorio) | 93 | 200 | 26 | 0 |
| Dueño, tienda con 120 productos (escritorio) | 106 | 438 | 28 | 0 |
| Lector (solo lectura) | 98 | 255 | 5 | 0 |
| Editor de catálogo | 39 | 162 | 17 | 0 |
| Atención a pedidos | 25 | 57 | 6 | 0 |
| Diseñador | 17 | 33 | 1 | 0 |
| Publicador | 17 | 33 | 1 | 0 |
| Dueño (teléfono) | ver § 5 | | | |

Los avisos que el recorrido sigue mostrando se revisaron uno por uno. Ninguno es un defecto:

- **"No se puede pulsar".** Son controles desactivados a propósito para quien solo puede ver, o un menú anterior que quedó abierto. Se verificó a mano que cada uno funciona.
- **"Quitar sin confirmar".** Quitan un renglón del carrito de prueba del simulador o de un borrador que todavía no se guarda. El editor muestra "Cambios sin guardar", así que no destruyen nada.
- **"Parada de foco sin tamaño".** Es la entrada de archivo oculta. Al recibir el foco, el recuadro visible que la contiene muestra el anillo.
- **Vista previa del correo.** Es un `iframe` de solo lectura.

## 3. Defectos encontrados y corregidos

### Errores

| # | Dónde | Qué pasaba | Corrección |
|---|---|---|---|
| 1 | Todo el panel | Una excepción en cualquier pantalla dejaba el panel entero en blanco hasta recargar | `ErrorBoundary`: el error queda en esa pantalla, el menú sigue funcionando y se limpia al navegar |
| 2 | Ficha de producto | "Personalización" y "Datos aduanales" compartían la clave de caché `["product", id, "customs"]` con datos distintos. Una pisaba a la otra y el editor fallaba | Claves separadas. El servidor responde `[]` (nunca `null`) al quitar todas las opciones |
| 3 | Configuración › Tiendas | Fallaba si antes se abría Dominios: misma clave `["storefronts"]` con formas distintas | Claves separadas. Se revisaron todas las claves compartidas del panel |
| 4 | Caja (punto de venta) | Un cajero con permiso de pedidos no podía usarla, porque pedía las sucursales a Configuración | `GET /v1/pos/locations` con permiso de pedidos. El menú solo la muestra a quien puede vender |
| 5 | Tienda | `/s/tienda`, `/s/tienda/sitio` y la misma dirección con barra final respondían un 404 en JSON | Llevan a la portada |
| 6 | Tienda | Una página inexistente respondía JSON | Muestra el 404 de la tienda en HTML, con un camino de vuelta |
| 7 | Formularios | Los botones sin tipo enviaban el formulario por accidente | `type="button"` por omisión |
| 8 | Unos 12 formularios | Un número inválido se guardaba como 0 en silencio | Aviso con el nombre exacto del campo |

### Seguridad

| # | Dónde | Qué pasaba | Corrección |
|---|---|---|---|
| 9 | Correos › Enviados | Con un proveedor real, el registro guardaba legibles el código de la tarjeta de regalo y los enlaces que actúan como su dueño: invitación al equipo, aprobación de compra, cotización y suscripción. Alguien con solo lectura de Configuración podía aceptar una invitación ajena y obtener sus permisos | Se redactan al guardar el registro. Solo el buzón de desarrollo los conserva |

### Uso solo con teclado y lector de pantalla

| # | Dónde | Qué pasaba | Corrección |
|---|---|---|---|
| 10 | Todos los formularios | El navegador mostraba su globo de validación en su propio idioma, distinto en cada navegador | Mensaje en línea en el idioma del panel, con `aria-invalid` y `aria-describedby`; el foco va al primer error |
| 11 | Editor de producto | Al guardar con errores, el foco se quedaba en el botón y el error estaba fuera de la vista. Las celdas de variantes solo se marcaban en rojo | El foco va al primer error, el aviso dice cuál es y las celdas llevan `aria-invalid` |
| 12 | Promociones | Varios campos sin nombre accesible | La etiqueta del campo nombra al control |
| 13 | Entradas de archivo, filtro oculto de Operación, buscador de la tienda | Recibían el foco sin mostrarlo | Anillo de foco visible |
| 14 | Diseñador, detalle de campaña | Sin `<h1>` | Título de página |

### Experiencia de uso (revisión de experto)

| # | Dónde | Qué pasaba | Corrección |
|---|---|---|---|
| 15 | Inicio, tienda nueva | Métricas en cero y "Publicar tu tienda" sin productos | Guía de primeros pasos con el estado real de cada paso (`GET /v1/setup`, una consulta): producto, pagos, envíos, diseño, dominio y equipo |
| 16 | Pagos | Conectar Stripe estaba al final. La URL del webhook decía "no se volverá a mostrar" | Stripe primero. El texto falso se quitó |
| 17 | Diseñador | Los campos salían en orden alfabético de su clave interna ("Texto alternativo" primero). Los enlaces mostraban "c" | Orden de lectura (`x-apexx-order`): contenido, botones, imagen y apariencia. Los enlaces ofrecen los destinos de la tienda con su nombre |
| 18 | Pedidos vacío | Sin siguiente paso. Exportar activo sin pedidos | Ofrece crear un pedido borrador o quitar filtros. Exportar se desactiva |
| 19 | Diálogos de empresa, llave de API y tarjeta de regalo | Al enviarlos vacíos no decían nada | Mensaje del campo |
| 20 | Compra a crédito B2B | La confirmación decía "confirmado y pagado" | Dice que se cargó al crédito de la empresa. Total a crédito |
| 21 | Mensajes de permiso | "Tu acceso a settings no permite esta acción (necesitas read)" | "Necesitas permiso para ver en «Configuración». Pídelo a un administrador de la tienda" |
| 22 | Envío del vendedor | Botón desactivado sin explicar y error duplicado | Mensaje en línea, una sola vez |
| 23 | `qa-setup` | No creaba vendedor, representante ni comprador B2B | Los crea, de forma idempotente |

## 4. Roles fuera del equipo

| Prueba | Resultado |
|---|---|
| Comprador B2B entra con código | Correcto. Ve Mi empresa: crédito disponible, facturas y aprobaciones |
| Formularios del comprador con datos vacíos y erróneos | La validación detiene los datos inválidos. Lo guardado se muestra escapado |
| Compra a crédito con orden de compra | Pedido creado con la orden de compra. El crédito disponible baja. El vendedor ve su parte |
| Representante abre la tienda como el comprador | Correcto, con un aviso fijo: "Estás comprando como … Todo queda registrado con tu nombre" |
| Lector, Pedidos y Vendedor intentan lo mismo | 403 los tres |
| Vendedor entra al panel | Solo ve su portal. Las demás secciones dicen "Sin acceso" |
| Vendedor registra un envío vacío o con datos erróneos | Mensaje en línea. El servidor rechaza la guía inválida |

## 5. Tareas completas solo con teclado

| Tarea | Resultado |
|---|---|
| Panel: entrar, ir de la guía a "Agregar producto", escribir nombre, descripción, precio y existencias, y guardar | Se completa con Tab, Enter y flechas. Hallazgo corregido: al guardar con errores, el foco no iba al error (#11) |
| Tienda: saltar al contenido, buscar, abrir producto, elegir talla, agregar, pagar con tarjeta de prueba | Ver la pasada final más abajo |

## 6. Auditorías permanentes (misma batería que el cierre)

| Auditoría | Resultado |
|---|---|
| Acceso | 460 rutas `/v1` verificadas, 410 de gestión: ninguna responde sin credenciales y la llave pública de tienda no entra a ninguna de gestión |
| Aislamiento | 144 tablas con datos de tienda, todas con seguridad por fila forzada |
| Reversión | El pedido completo se cancela y todo vuelve a su punto: dinero, saldo, comisión, cupón y existencias. La contabilidad queda en cero |
| Compra interrumpida | Se recupera sin cobro doble |
| Suite completa | Go (22 paquetes) y panel (51 pruebas) en verde |

Las pruebas de Go corren ahora en un clúster de Postgres propio (puerto 5433). Así ya no cambian la contraseña del rol de la aplicación en la base de desarrollo, que era lo que causaba los 503 intermitentes del servidor de desarrollo.

## 7. Lo que no cubre esta auditoría

- Safari y Firefox reales: todo se probó en Chromium.
- Lectores de pantalla reales (NVDA, VoiceOver). Se revisaron nombres, roles y `aria-*`, pero no se escuchó.
- El formato de los campos de fecha sigue el idioma del navegador.
- Las pruebas son de una sola persona a la vez; la carga concurrente está en el plan de despliegue.
