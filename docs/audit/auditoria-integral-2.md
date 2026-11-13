# Auditoría integral 2: panel, tienda, sandbox y superadmin, como humano

Fecha: 13 de noviembre de 2026. Navegador: Chromium, escritorio 1366×900 y emulación de iPhone 13.

Esta segunda auditoría integral volvió a recorrer todo el sistema como lo usaría una persona, después de construir el sandbox, los planes y el superadmin. El objetivo era encontrar discrepancias e inconsistencias, no solo errores:

- un número que no cuadra entre dos pantallas;
- un texto que dice una cosa y una pantalla que hace otra;
- una acción que no deja rastro;
- un paso de compra que falla la primera vez.

Cada defecto real se corrigió y lleva una prueba que falla sin la corrección.

## 1. Cómo se probó

| Tipo de auditoría | Cómo | Alcance |
|---|---|---|
| Accesibilidad (WCAG 2.1 A/AA) | axe-core 4.10.2 en cada vista | 151 vistas: panel, tienda, superadmin, escritorio y teléfono |
| Recorrido exhaustivo por rol | `tools/qa/exhaustivo.js` (cada ruta, botón, diálogo y formulario, datos vacíos y erróneos, teclado) | dueño (escritorio y teléfono), lector, catálogo, pedidos, diseñador, publicador, representante, vendedor, y el dueño **dentro de un sandbox** |
| Compra de punta a punta | Ficha → variante → carrito → cantidad → pago (vacío y luego válido) → gracias | Producto digital y físico, escritorio y teléfono, más un comprador "rápido" que paga sin esperar |
| Enlaces de la tienda | Rastreo de todas las ligas de la portada y el menú | 158 enlaces |
| Superadmin | axe, teclado (35 paradas), validación, desborde en teléfono | Entrada con TOTP y consola |
| Encabezados de seguridad | Revisión de cada respuesta | Panel, API, tienda |
| Integridad de datos | Consultas de referencias huérfanas en todas las tablas con referencias | Todas las tiendas de QA |
| Consistencia de cifras | El mismo pedido en la tienda (resumen, gracias), en la lista del panel y en su ficha | Pedidos digitales y físicos |
| Consistencia de textos | Tono (tú), nombres de acciones del registro, mensajes de error de la API | Panel, tienda y API |

## 2. Resultado por rol

| Rol | Rutas | Botones | Diálogos | Excepciones, errores de consola y 5xx | Defectos reales que quedan |
|---|---|---|---|---|---|
| Dueño (escritorio) | 115 | 531 | 55 | 0 | 0 |
| Lector (solo lectura) | 110 | 265 | 5 | 0 | 0 |
| Editor de catálogo | 40 | 186 | 20 | 0 | 0 |
| Atención a pedidos | 25 | 70 | 6 | 0 | 0 |
| Diseñador | 17 | 43 | 1 | 0 | 0 |
| Publicador | 17 | 43 | 1 | 0 | 0 |
| Representante de ventas | 30 | 77 | 7 | 0 | 0 |
| Vendedor (portal) | 2 | 2 | 1 | 0 | 0 |
| Dueño (teléfono) | 122 | 633 | 74 | 0 | 0 |
| Dueño dentro de un sandbox | 112 | 407 | 37 | 0 | 0 |

Superadmin: axe sin hallazgos, 35 paradas de teclado con anillo visible, validación de cada campo y 0 px de desborde en teléfono.

## 3. Defectos encontrados y corregidos

### Tienda (lo que ve el comprador)

| # | Qué pasaba | Impacto | Corrección |
|---|---|---|---|
| 1 | Las variantes (radios) llevaban `data-compare` con el precio de lista. El clic de "Comparar" escucha ese atributo y cancela el clic | **Crítico.** En cualquier producto en oferta el comprador no podía elegir otra talla o color: siempre se quedaba la primera | El precio de lista pasa a `data-compare-at` y el script de comparar solo escucha botones. Prueba `TestOnlyCompareButtonsCarryDataCompare` |
| 2 | El comprador que escribe su código postal al final y pulsa "Pagar" de inmediato recibía "El total de tu compra cambió" | Toda compra física fallaba al primer intento en ese caso | El envío se cotiza en cuanto el código postal está completo, mientras se escribe. Si al pagar el código no coincide con el cotizado, se recotiza primero. Si el total cambia, se muestra el nuevo total con un mensaje claro ("el total es $358.00 MXN; revísalo y pulsa Pagar"). Nunca se cobra un total que el comprador no vio |
| 3 | En un pedido solo digital el código postal seguía siendo obligatorio, aunque el servidor no lo pide | Paso de más y sin sentido para quien compra algo digital | Se oculta y deja de exigirse cuando no hay nada que enviar |
| 4 | El IVA incluido se truncaba en vez de redondear: $209.00 mostraba $28.82 de IVA (lo correcto es $28.83) | Un centavo de diferencia contra la factura, que es lo que nota un contador | `logistics.IncludedTax` redondea al centavo más cercano en el pago y en los pedidos manuales, como ya hacía el caso sin IVA incluido. Prueba `TestIncludedTaxRoundsToNearestCent` |
| 5 | La página de gracias decía "Envío $0.00" en un pedido digital, mientras el resumen decía "Entrega digital" | Dos textos distintos para lo mismo | Gracias usa las mismas palabras que el resumen: "Entrega digital", "Gratis" y "Recolección" |
| 6 | Sin código postal, el carrito mostraba precios de envío exactos y, debajo, en rojo, "Ingresa un código postal para calcular el envío" | Se contradecía. Además los precios eran de referencia: con 06600 el envío costaba $99, no $129 | El encabezado dice "precios de referencia, se confirman con tu código postal". La petición del código postal se muestra como nota, no como error en rojo |
| 7 | El menú podía apuntar a una categoría o un producto que ya no existe (`/c/tenis`) | Enlace roto en la portada | El menú omite los destinos que ya no existen y sube sus hijos. El editor de navegación avisa: "«tenis» ya no existe en tu tienda: este enlace no se muestra" |
| 7a | Las frases nuevas de la tienda no estaban traducidas. La batería lo detectó con `TestStorefrontScriptHasEveryPhrase` | Un comprador en inglés, francés, portugués, indonesio o japonés habría visto los mensajes nuevos en español | Traducidas a los cinco idiomas |
| 8 | Los recursos de la tienda (`shop.js`) tienen caché larga | Sin cambiar la versión, los compradores no recibirían las correcciones | Versión `61` y `AssetVersion` `22` |

### Panel

| # | Qué pasaba | Corrección |
|---|---|---|
| 9a | **Crítico.** La pantalla de entrada del panel, sin sesión, hacía unas **430 peticiones por segundo** a `/v1/me` (4,335 en 10 s). El 401 de `/v1/me` anunciaba "sesión terminada", el aviso volvía a preguntar `/v1/me`, y así sin fin. Cada visitante sin sesión golpeaba el servidor y vaciaba la batería del teléfono | El 401 de `/v1/me` ya no se anuncia, porque `/v1/me` es justo como el panel se entera de que no hay sesión. Cualquier otra llamada con 401 sí lo anuncia, así que una sesión que termina a media pantalla sigue llevando a la entrada. Ahora hay **1 petición** en 10 s. Si la sesión se cierra desde otro dispositivo y el usuario navega, llega a la entrada con 4 peticiones en total. El superadmin, la cuenta del comprador y los favoritos se midieron igual: de 0 a 2 peticiones en 6 s. Prueba `src/unauthorized.test.ts` |
| 9 | La lista de pedidos (`GET /v1/orders`) devolvía `itemsTotal`, `shipping`, `taxIncluded`, `paidTotal` y `balance` en 0. Un pedido pagado de $537 decía "pagado: $0" | La lista lee los mismos importes que la ficha, en la misma consulta (64 ms con 50 pedidos). La prueba compara cada pedido de la lista contra su ficha |
| 10 | Cerrar la sesión de un dispositivo o "cerrar las demás sesiones" no dejaba rastro en el registro de actividad | Se registra `session.revoke` o `session.revoke_others`, con el número de sesiones. La prueba exige la entrada |
| 11 | Actividad tenía nombres en español para 61 acciones, pero el sistema registra más de 170. El resto se leía como código ("Category reorder"), y dos nombres (`member.created` y `member.updated`) no coincidían con lo que se registra (`member.create` y `member.update`) | Nombres en español para todas las acciones. Las que se arman por partes (`settings.<sección>.set`, `subscription.<acción>`) también se leen en español. `TestEveryAuditActionHasAPanelName` recorre el código y falla si una acción nueva no tiene nombre |
| 12 | El vendedor del marketplace podía "Quitar" un producto sin confirmación | Se quita al momento con un aviso que ofrece «Deshacer» |
| 13 | Siete entradas de archivo ocultas recibían el foco sin mostrarlo (parada invisible) | El botón visible recibe el foco y la entrada queda fuera del orden de tabulación. Si la entrada vive dentro de una etiqueta, la etiqueta muestra el anillo de foco |
| 14 | Los colores de estado (verde, ámbar, rojo) no llegaban a 4.5:1 de contraste sobre sus fondos suaves en algunos temas | Tonos ajustados. Una prueba revisa el contraste de cada color de estado sobre cada superficie de cada tema |
| 15 | La precarga de rutas pedía datos de la tienda en la consola del superadmin (401 en la consola del navegador) | La consola del superadmin no precarga rutas de la tienda |

### Datos

| # | Qué pasaba | Corrección |
|---|---|---|
| 16 | 130 productos de las tiendas de QA apuntaban a categorías que no existen. La API aceptaba categorías desconocidas y el importador de VTEX guardaba nombres en lugar de identificadores | La API rechaza una categoría inexistente con un error claro. Importadores y semillas que mandan nombres crean la categoría. `apexx migrate` repara lo existente una sola vez, sin repetir, y deja registro (`category.repair`). Prueba `TestProductCategoryRefs` |

### Seguridad

| # | Qué pasaba | Corrección |
|---|---|---|
| 17 | Faltaban `Permissions-Policy` y `Cross-Origin-Opener-Policy` | Cámara, micrófono, ubicación, USB, puertos serie, HID y Bluetooth quedan bloqueados. COOP es `same-origin-allow-popups`, para que los pagos con ventana emergente sigan funcionando |
| 18 | Las respuestas autenticadas no decían cómo guardarse en caché | `Cache-Control: private, no-cache` por omisión en toda respuesta con sesión |
| 19 | El ETag de las páginas del sitio no cambiaba cuando cambiaba el menú | El ETag incluye la suma del HTML final |

## 4. Revisado y correcto (no son defectos)

- **"Quitar sin confirmar" en promociones y búsqueda.** Quitan un renglón del carrito de prueba del simulador o de un formulario sin guardar, que tiene "Guardar reglas" y "Descartar".
- **El lector ve el editor de promociones.** Lo ve desactivado, con el aviso "Tu acceso es de solo lectura". El servidor rechaza cualquier escritura.
- **"No se puede pulsar".** Son controles desactivados a propósito o un menú anterior abierto. Se verificó cada uno a mano.
- **401 de `/v1/me` al abrir el panel sin sesión.** Es la precarga intencional de `index.html`, que ahorra un viaje de red a quien sí tiene sesión.
- **Promoción "10% en compras desde $3,000: … (mínimo $3,000)".** El primer texto es el título que escribió la tienda. El mensaje del sistema debe valer por sí solo cuando la promoción no tiene título.
- **Textos con `<script>` guardados por el recorrido.** Se muestran escapados y nunca se ejecutan, ni en la lista de API keys ni en los nombres.
- **"Editar compradores" acepta guardar la lista vacía.** El comprador principal de la empresa no se puede quitar: el servidor siempre lo conserva. Guardar vacío deja la empresa como estaba, y el registro de actividad lo confirma (`members: 0` adicionales antes y después).
- **Una sesión de pruebas dejó de funcionar a la mitad.** El recorrido del dueño en teléfono pulsó "Cerrar las demás sesiones". La función hizo lo correcto, y gracias a eso se encontró el defecto 10.
- **Errores de la API.** Todos responden `application/problem+json` con código, título y detalle en español (401, 404, 400, 409). La única excepción es una ruta desconocida bajo `/s/{tienda}/api/`, que responde el 404 de texto de Go: lo único que hay en esa dirección es la API del navegador de la tienda, y nadie la enlaza.
- **URLs de la tienda en dos idiomas** (`/cart` y `/checkout` junto a `/gracias` y `/comparar`). Se dejan así a propósito: `/cart` y `/checkout` son las direcciones que esperan los temas, las campañas y los enlaces externos. Cambiarlas rompería enlaces ya publicados.

## 5. Compra completa verificada

| Caso | Escritorio | Teléfono |
|---|---|---|
| Digital: variante, +1, pago vacío con foco en el primer error, pago aprobado | Pedido #1009, IVA $57.66 | Pedido #1008 |
| Físico: tres opciones de entrega con fecha, pago aprobado | Pedido #1011: $438 + $99 = $537, IVA $74.07 | Pedido #1012 |
| Comprador que paga en cuanto termina de escribir el código postal | Ve el nuevo total, el foco va a "Pagar $358.00 MXN" y el segundo clic compra (#1013) | — |

Las cifras de la tienda, la página de gracias, la lista de pedidos del panel y la ficha del pedido coinciden al centavo.

En todos los casos: axe sin hallazgos, 0 px de desborde, 0 errores de JavaScript y 0 respuestas 5xx.
