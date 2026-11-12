# Etiquetas y píxeles

Fecha: 12 de noviembre de 2026. Pantalla: *Configuración › Etiquetas y píxeles*. Código: `internal/api/tags.go`, `internal/api/shop/tags-runtime.js` y los eventos en `internal/api/shop/shop.js`.

## El problema

Cada tienda quiere medir y anunciarse a su manera:
- unas usan Google Tag Manager y ahí ponen todo;
- otras solo tienen Google Analytics;
- otras necesitan el píxel de Meta, TikTok o Pinterest, o una conversión de Google Ads, en el carrito o en el pedido confirmado.

En otras plataformas esto suele requerir un desarrollo por cliente, código pegado a mano en plantillas y un despliegue por cada cambio. Además, los píxeles a menudo quedan mal instalados: sin monto, sin número de pedido o con compras contadas dos veces.

## La solución: la configuración es un dato

1. **Publicar es inmediato.** Al pulsar «Publicar» se guarda una versión nueva y se emite un evento por el outbox. Cada nodo descarta su copia y la reconstruye, así que la siguiente página que abra cualquier comprador apunta a `/s/{tienda}/api/tags.js?v=N`. No hay despliegue, no se recompila nada y no se vuelve a publicar el sitio.
   - Medido: publicar tarda 34 ms y la página siguiente ya trae la versión nueva.
   - Probado con dos nodos: un segundo servidor cambia de versión al aplicar el evento (`TestStoreTags`).

2. **Eventos de comercio estándar.** La tienda emite sola los eventos con los nombres y campos de GA4:

   | Evento | Dónde | Datos |
   |---|---|---|
   | `view_item` | ficha de producto | SKU, nombre, precio, moneda |
   | `add_to_cart` | al agregar | SKU, cantidad, precio, monto |
   | `view_cart` | carrito | productos, cantidades, monto calculado por la tienda |
   | `begin_checkout` | al ir a pagar | lo mismo que el carrito |
   | `purchase` | pedido confirmado | número de pedido, monto, envío, impuestos y productos, una sola vez por pedido |

   Van al `dataLayer`, que lee Tag Manager. `tags.js` los traduce para cada píxel. Un pedido apartado con pago pendiente (OXXO, SPEI) no cuenta como compra en el navegador; las conversiones desde el servidor lo reportan al pagarse.

3. **Cada proveedor con solo su ID:**

   | Tipo | Qué se pega | Qué recibe |
   |---|---|---|
   | Google Tag Manager | `GTM-…` | el `dataLayer` completo; lo demás se configura en GTM |
   | Google Analytics 4 | `G-…` | vistas y eventos de comercio |
   | Google Ads | `AW-…` y la etiqueta de conversión | la conversión donde se elija (por omisión, el pedido confirmado), con monto y número de pedido |
   | Meta | ID del píxel | PageView, ViewContent, AddToCart, InitiateCheckout, Purchase |
   | TikTok | ID del píxel | page, ViewContent, AddToCart, InitiateCheckout, CompletePayment |
   | Pinterest | ID de la etiqueta | page, pagevisit, addtocart, checkout |
   | Script por URL | `https://…` | se carga en los lugares elegidos |
   | Código propio | JavaScript | corre como función con el evento en `apexx`, en los lugares elegidos |

   En Meta, TikTok y Pinterest la compra usa el número de pedido como identificador del evento. Así la plataforma descarta el duplicado de las conversiones desde el servidor (`conversions.go`).

4. **Dónde se dispara.** Google Ads, los scripts y el código propio se disparan en los lugares que se marquen: todas las páginas, ficha, agregar al carrito, carrito, inicio del pago o pedido confirmado. Además, cualquier etiqueta puede limitarse a direcciones que contengan un texto.

5. **Modo prueba.** Con `?apexx_tags=prueba` (botón «Probar en la tienda»), un recuadro lista cada etiqueta que cargó o por qué no, y cada evento que recibió con su monto. Sirve para verificar antes de anunciar.

6. **Versiones.** Se guardan las últimas 20. «Restaurar» publica una versión anterior como la más reciente.

## Seguridad y privacidad

- **Consentimiento.** Las etiquetas de analítica y publicidad cargan solo con el permiso del visitante. Al aceptar, reciben los eventos de esa misma página. Con Global Privacy Control quedan apagadas hasta que el visitante las encienda.
- **Páginas excluidas.** Nunca hay etiquetas en el pago, la aprobación de compras, las cotizaciones ni la cuenta del cliente: la página no incluye `tags.js` y su política lo prohíbe. La página de gracias sí las tiene, porque ahí van las conversiones.
- **Política de seguridad (CSP).** En las demás páginas la política permite exactamente los dominios de las etiquetas activas, nada más. Se calcula al servir la página, así que un cambio rige desde la siguiente.
  - Con Google Tag Manager, la política permite scripts https y en línea, porque las etiquetas de GTM se deciden en su propia consola. El panel lo explica; el pago y la cuenta siguen cerrados.
- **Código propio.**
  - Solo lo publica un administrador del diseño, y queda en la auditoría.
  - Cada código vive en su propio archivo (`/s/{tienda}/api/tags/{id}.js`): un error suyo no detiene las demás etiquetas.
  - No se aceptan etiquetas `<script>` dentro del código.
  - Otra tienda no puede servirlo.
- **Las tarjetas nunca pasan por Apexx.** El campo de la tarjeta vive en el iframe de Stripe.

## Velocidad

- `tags.js` pesa 5,6 KB comprimido.
- Se sirve inmutable por versión, así que el navegador lo baja una vez.
- Se carga con `defer` después del contenido.
- La ficha de producto sigue en ~2 ms del lado del servidor.
- Una tienda sin etiquetas no carga nada.

## Pruebas

- `TestStoreTags` cubre:
  - la validación de cada formato y el permiso de administrador;
  - la política por página y la exclusión de checkout y cuenta;
  - el aislamiento entre tiendas;
  - el caché inmutable y el código en su propio archivo;
  - el cambio inmediato en un segundo nodo;
  - la política de Tag Manager;
  - el historial y la restauración.
- Recorrido en navegador real (Chromium):
  - el aviso aparece y antes del permiso no se pide ningún proveedor;
  - al aceptar llegan `page_view` y `view_item` con su monto;
  - después llegan `add_to_cart`, `view_cart` y `begin_checkout`;
  - `purchase` llega con el número de pedido y no se repite al recargar;
  - en el checkout no carga nada;
  - el modo prueba lista cada etiqueta y evento;
  - 0 errores de política de seguridad.
- Casos para personas: `docs/qa.md` §9.
