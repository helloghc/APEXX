# Etapa 0 — Cerrar lo que quedó abierto (noviembre 2026)

Estado: **hecha**.

| Función | Dónde | Prueba |
|---|---|---|
| Pago automático a vendedores con Stripe, calendario y retención por devoluciones | Marketplace › Vendedores | `TestSellerAutomaticPayouts` |
| Panel completo en seis idiomas: 3,404 frases, incluidas las de datos variables (`#` cifras, `{}` textos, listas) | Todo el panel (Idioma · Language) | `i18n.test.ts`; recorrido de 160 rutas en japonés e indonesio |
| Correos al comprador en su idioma: las 18 plantillas en cinco idiomas más; plantillas reescritas con las traducciones propias de la tienda; vista previa por idioma | Correos | `TestBuyerEmailsInTheirLanguage` |
| Mensajes de entrega y promociones en los seis idiomas (fechas, feriados, zona, tarifas, avance de promociones) | Carrito y pago | `TestPhrasesWithVariableParts` |
| Toda frase visible del guion de la tienda tiene traducción | Tienda | `TestStorefrontScriptHasEveryPhrase` |
| Diseñador usable en teléfono: pestañas Secciones / Vista previa / Editar, barra con iconos | Diseño de la tienda | Recorrido en iPhone 13 |
| Revisión de los cuatro temas (balsamo, lienzo, mercadito, voltio) en teléfono y escritorio | Tienda | Recorrido de 40 páginas: desbordes, imágenes, contraste, tamaño de toque, consola |

## Decisiones de diseño

- **Idioma del comprador.** El pedido guarda el idioma en que se compró y el cliente el de su último pedido. Cada correo de ese pedido sale en ese idioma, también los que se mandan días después (envío, entrega, reembolso). El código de acceso sigue el idioma de la página que lo pidió. Un idioma que la tienda no activó se manda en español.
- **Plantillas propias.** Si la tienda reescribió una plantilla, se envía con sus palabras, frase por frase con sus traducciones de Configuración › Idiomas. Si no la tocó, se usa el texto de Apexx en el idioma del comprador.
- **Frases con partes variables.** Solo se aplican a mensajes que arma el servidor (API y correos). El texto que la tienda escribe en sus páginas nunca se traduce a medias por un patrón. Los nombres que la tienda pone (zonas, opciones de envío, promociones) se quedan como están, salvo que la tienda los traduzca.
- **Extractor de frases del panel** (`web/scripts/i18n-pendientes.mjs`). Lista lo que falte traducir; hoy solo quedan cifras y estilos.

## Defectos que encontró la revisión de temas (todos corregidos)

- En teléfono, el carrito (tabla de cuatro columnas) y el pago (los `fieldset` y la rejilla no se encogían) se salían de la pantalla. Ahora el carrito es una tarjeta por línea.
- Los botones fantasma que son `<button>` tomaban el color primario de fondo con el color de texto del tema (contraste 2.8:1, por ejemplo «Calcular» en Voltio). Ahora son transparentes en los cuatro temas.
- En el héroe a todo lo ancho, el texto blanco quedaba sobre la parte clara del fondo (Lienzo). Ahora el fondo se oscurece desde arriba.
- Estrellas sin elegir, migas, enlaces del pie, orden del catálogo y redes sociales: se subió el contraste y el tamaño de toque (mínimo 24 a 32 px). La marca en las tarjetas de Lienzo pasó de 10.9 a 11.5 px.
- El guion pedía datos de cuenta (tarjetas, compra exprés, mesas) aunque no hubiera sesión, y dejaba errores 401 en la consola. Ahora solo los pide con sesión, gracias a una marca sin datos.
- El diseñador mostraba valores internos («center») en vez de nombres («Centro»).

## Lo que queda fuera de este entorno

- La revisión de las traducciones por hablantes nativos de francés, portugués, indonesio y japonés.
