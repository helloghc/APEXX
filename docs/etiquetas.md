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

3. **Catálogo de proveedores.** Cada proveedor está definido en el sistema (`tagKindList` en `internal/api/tags.go`) con:
   - sus campos y el formato de cada uno;
   - su grupo y el permiso que pide por omisión;
   - qué recibe;
   - el enlace a su guía oficial;
   - los dominios que usa, que la política de seguridad permite solo mientras la etiqueta esté activa.

   El panel arma el formulario con esa definición. Agregar un proveedor es agregar una entrada y su función en `tags-runtime.js`.

   | Grupo | Proveedor | Qué se pega | Qué recibe |
   |---|---|---|---|
   | Analítica | Google Tag Manager | `GTM-…` | el `dataLayer` completo; lo demás se configura en GTM |
   | Analítica | Google Analytics 4 | `G-…` | vistas y eventos de comercio de GA4 |
   | Analítica | Adobe Analytics (Experience Platform) | el código de inserción de la propiedad de Tags (`assets.adobedtm.com/…`) | Adobe Client Data Layer (`window.adobeDataLayer`) con el esquema XDM de comercio: `commerce.productViews`, `productListAdds`, `productListViews`, `checkouts`, `purchases`, con `productListItems` y `order` (`purchaseID`, `priceTotal`, `currencyCode`) |
   | Publicidad | Google Ads | `AW-…` y la etiqueta de conversión | la conversión en los lugares elegidos, con monto y número de pedido |
   | Publicidad | Meta | ID del píxel | PageView, ViewContent, AddToCart, InitiateCheckout, Purchase (`eventID` = pedido) |
   | Publicidad | TikTok | ID del píxel | page, ViewContent, AddToCart, InitiateCheckout, CompletePayment (`event_id` = pedido) |
   | Publicidad | Pinterest | ID de la etiqueta | page, pagevisit, addtocart, checkout (`order_id`, `event_id`) |
   | Publicidad | Snapchat | ID del píxel | PAGE_VIEW, VIEW_CONTENT, ADD_CART, START_CHECKOUT, PURCHASE (`transaction_id`, `client_dedup_id`) |
   | Publicidad | X (Twitter) | ID del píxel y, opcional, el ID de cada evento creado en X | `config` y los eventos con `value`, `currency`, `contents`, `conversion_id` |
   | Publicidad | LinkedIn | ID de socio y, opcional, el ID de cada conversión | Insight Tag y `lintrk('track', {conversion_id, conversion_value, currency, event_id})` |
   | Publicidad | Microsoft Advertising | ID de la etiqueta UET | `pageLoad` y eventos con `ecomm_prodid`, `ecomm_pagetype`, `revenue_value`, `currency` |
   | Publicidad | Reddit | ID del píxel (`a2_…`) | PageVisit, ViewContent, AddToCart, Purchase (`conversionId`) |
   | Mapas de calor | Hotjar | ID del sitio | grabaciones y mapas; los eventos de comercio como eventos de Hotjar |
   | Mapas de calor | Microsoft Clarity | ID del proyecto | grabaciones y mapas; los eventos de comercio como eventos de Clarity |
   | Mapas de calor | Crazy Egg | número de cuenta | mapas de calor y de desplazamiento |
   | Chat | Botón de WhatsApp | número con lada, mensaje, texto y posición | botón flotante a `wa.me`; en la ficha, el mensaje agrega el nombre del producto. Sin servicios externos ni cookies |
   | Chat | Tawk.to, Crisp, Tidio, Intercom, Zendesk, Trengo, JivoChat | el ID o la clave del widget | el chat, fuera del pago y la cuenta |
   | Chat | HubSpot | Hub ID | código de HubSpot (chat, formularios, analítica) |
   | Avanzado | Script por URL | `https://…` | se carga en los lugares elegidos |
   | Avanzado | Código propio | JavaScript | corre como función con el evento en `apexx` |

   En Meta, TikTok, Pinterest, Snapchat, X, LinkedIn y Reddit, la compra usa el número de pedido como identificador. Así la plataforma descarta el duplicado de las conversiones desde el servidor (`conversions.go`).

   Una prueba exige que el ejemplo de cada campo, el que el panel muestra como pista, pase su propia validación (`TestTagKindsExamplesValidate`).

   **Sobre «Adobe Commerce».** Adobe Commerce es la plataforma de tienda (antes Magento). Lo que una tienda en Apexx necesita de Adobe es su analítica: Adobe Analytics o Customer Journey Analytics, a través de Adobe Experience Platform. Apexx carga la propiedad de Tags del cliente y le entrega los eventos con el mismo esquema XDM de comercio que usan los conectores de Adobe Commerce. Las reglas de Tags del cliente los envían con el Web SDK.

4. **Dónde se dispara.** Google Ads, los scripts y el código propio se disparan en los lugares que se marquen: todas las páginas, ficha, agregar al carrito, carrito, inicio del pago o pedido confirmado. Además, cualquier etiqueta puede limitarse a direcciones que contengan un texto.

5. **Modo prueba.** Con `?apexx_tags=prueba` (botón «Probar en la tienda»), un recuadro lista cada etiqueta que cargó o por qué no, y cada evento que recibió con su monto. Sirve para verificar antes de anunciar.

6. **Verificación de dominio.** Se pueden agregar las etiquetas meta que piden Google Search Console (`google-site-verification`), Bing (`msvalidate.01`), Meta (`facebook-domain-verification`), Pinterest (`p:domain_verify`), Yandex o cualquier otro servicio (nombre y contenido).
   - Se puede pegar la etiqueta completa: el sistema toma el nombre y el código.
   - Van en el `<head>` de todas las páginas, incluso las del builder y el pago, porque no son scripts.
   - No se aceptan nombres como `robots` o `viewport`, que cambiarían cómo se indexa o se ve la tienda.

7. **Historial y observabilidad.** Cada publicación guarda:
   - la versión, la fecha y quién publicó;
   - qué cambió, en palabras («Agregó Meta 1234…», «Cambió Google Analytics 4: ID: «G-OLD» → «G-NEW»», «Quitó Hotjar…», «Restauró la versión 3»).

   El panel lo muestra en «Versiones». La auditoría encadenada (`store_tags.set`) guarda además la configuración completa de cada versión. Se conservan las últimas 20 versiones, y «Restaurar» publica cualquiera de ellas como la más reciente, al instante.

## Seguridad y privacidad

- **Permiso de cookies, configurable por tienda** (`consentMode`):

  | Modo | Qué hace | Para quién |
  |---|---|---|
  | Solo aviso (por omisión) | Todo carga desde la primera página. Un aviso con «Entendido» informa, y el visitante puede apagar la analítica o la publicidad en «Preferencias de cookies» | México y casi toda Latinoamérica, donde basta con avisar de forma transparente |
  | Analítica sin permiso, publicidad con permiso | Analytics y mapas de calor desde la primera página; los píxeles de publicidad esperan a que el visitante acepte | Un punto medio |
  | Todo con permiso | Nada opcional carga hasta aceptar | Obligatorio para tiendas que venden en Europa o Reino Unido (GDPR) |

  - **En los tres modos**, Google recibe las señales del modo de consentimiento v2 (`analytics_storage`, `ad_storage`, `ad_user_data`, `ad_personalization`): primero el valor por omisión y luego cada cambio.
  - **Con Global Privacy Control** la publicidad queda apagada salvo que el visitante la encienda, porque leyes de Estados Unidos (como la de California) exigen respetarlo. La analítica sigue midiendo en «Solo aviso».
  - **Quién decide:** el modo lo elige cada tienda según dónde vende. El panel explica cuándo usar cada uno.
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

## Fragmentos de cada proveedor: qué falta confirmar con cuentas reales

El arranque y los nombres de eventos de cada proveedor siguen sus guías públicas de instalación, enlazadas en el panel. Desde el entorno de desarrollo no se pudo abrir la documentación de X, Snapchat, Reddit, Microsoft, Meta, TikTok ni Hotjar, porque la red solo permite ciertos dominios.

Antes de anunciarlo, la sección 9 de `docs/qa.md` pide verificar con una cuenta real de cada proveedor que los eventos llegan con monto y número de pedido. Un ajuste de formato, si hiciera falta, cambia solo `tags-runtime.js` y se publica con el siguiente despliegue. Las etiquetas de los clientes no se tocan.

## Pruebas

- `TestTagKindsExamplesValidate`: cada proveedor declara grupo, permiso y qué recibe, y su ejemplo pasa su validación.
- `TestTagsChangesInWords`: agregar, quitar y cambiar se describen en palabras.
- `TestStoreTags` cubre:
  - la validación de cada formato y el permiso de administrador;
  - la política por página y la exclusión de checkout y cuenta;
  - el aislamiento entre tiendas;
  - el caché inmutable y el código en su propio archivo;
  - el cambio inmediato en un segundo nodo;
  - la política de Tag Manager;
  - el historial (quién, qué cambió) y la restauración;
  - WhatsApp, Crisp, Hotjar y X con sus eventos;
  - las metas de verificación en el `<head>`, el builder y el pago;
  - la política de estilos y fuentes de los chats;
  - las entradas de auditoría;
  - el modo de permiso por omisión, el rechazo de un modo desconocido y su cambio en palabras.
- Recorrido en navegador real (Chromium):
  - el aviso aparece y antes del permiso no se pide ningún proveedor;
  - al aceptar llegan `page_view` y `view_item` con su monto;
  - después llegan `add_to_cart`, `view_cart` y `begin_checkout`;
  - `purchase` llega con el número de pedido y no se repite al recargar;
  - en el checkout no carga nada;
  - el modo prueba lista cada etiqueta y evento;
  - con nueve proveedores, cada uno arrancó y recibió la compra en su formato. Los servidores de los proveedores fueron simulados, porque la red del entorno no llega a ellos;
  - el botón de WhatsApp mide 52 px en teléfono y no tapa la página;
  - los tres modos de permiso. «Solo aviso» carga Analytics y Meta sin clic y muestra «Entendido»; con GPC, Meta no carga y Google recibe `ad_storage: denied`. «Analítica sin permiso» carga solo Analytics. «Todo con permiso» no carga nada antes del clic;
  - 0 errores de política de seguridad.
- Casos para personas: `docs/qa.md` §9.
