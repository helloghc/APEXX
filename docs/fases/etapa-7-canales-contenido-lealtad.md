# Etapa 7 — Canales, contenido y lealtad (noviembre 2026)

Estado: **hecha**. Cada función tiene prueba de integración, recorrido en el navegador (panel en escritorio, tienda en teléfono) y medición de velocidad.

| Función | Dónde | Prueba |
|---|---|---|
| Puntos de lealtad con niveles; canje por saldo a favor; asientos en el libro contable | Configuración › Puntos de lealtad; Clientes; Mi cuenta › Mis puntos | `TestLoyaltyPoints` |
| Blog con portada, etiquetas, publicación programada y mapa del sitio | Configuración › Blog; `/blog` | `TestBlog` |
| Diseño programado (Buen Fin, Navidad) con vista previa firmada | Configuración › Diseño programado | `TestScheduledDesign` |
| Campañas de correo por grupo, con baja y atribución de ventas | Configuración › Campañas de correo | `TestEmailCampaigns` |
| Conversiones de anuncios desde el servidor (Meta y GA4), solo con permiso | Configuración › Conversiones de anuncios | `TestServerSideConversions` |
| Contenido por segmento de clientes | Diseñador › cada sección › Quién la ve | `TestContentPerSegment` |
| Punto de venta: búsqueda por código, efectivo con cambio o terminal, ticket | Caja (tienda física) | `TestPointOfSale` |
| Regalos: envoltura con cargo, mensaje, ticket sin precios; hoja de empaque | Configuración › Regalos; pago; pedido | `TestGiftsAndRegistries` |
| Mesa de regalos con envío privado a los festejados | Mi cuenta › Mesa de regalos; `/mesa`; ficha de producto | `TestGiftsAndRegistries` |
| Reportes personalizados: agrupaciones y cifras a elegir, gráfica, guardar y CSV | Reportes › Personalizados | `TestCustomReports` |
| Mercado Libre y Amazon: publicar con precio y existencias al día, importar pedidos | Configuración › Mercado Libre y Amazon | `TestSalesChannels` |
| Varias tiendas con su propio diseño; dominio por mercado (moneda e idioma) | Configuración › Tiendas y mercados; Dominios | `TestStorefrontsAndMarketDomains` |
| Editor de imágenes reversible (girar, voltear, recortar, luz y color) o como copia | Catálogo › Medios › Editar imagen | `TestImageEditor`, `TestEdits` |

## Decisiones de diseño y seguridad

- **Mesa de regalos.** La dirección de los festejados se guarda cifrada y nunca llega al invitado: el servidor la pone en el pedido. La página de la mesa no se indexa, la búsqueda solo muestra mesas marcadas como visibles, y lo «ya regalado» se calcula de los pedidos pagados, así que una cancelación o devolución devuelve el regalo a la lista.
- **Reportes personalizados.** Cada agrupación, cifra y estado se elige de una lista cerrada; la consulta se arma solo con fragmentos conocidos y los valores viajan como parámetros. Se calculan al momento sobre los pedidos (sin tablas que sincronizar), con tope de 8 s por consulta y RLS por tienda.
- **Mercado Libre y Amazon.** La tienda usa las credenciales de su propia app; se guardan cifradas y no se vuelven a mostrar. Un rechazo del canal (falta un atributo, categoría equivocada) se muestra con su motivo y no se reintenta a ciegas; los errores de red sí se reintentan con espera creciente. Solo se envía lo que cambió. Los pedidos se importan una sola vez (llave única por pedido del canal), comparten inventario con la tienda, y una cancelación en el canal cancela aquí.
- **Varias tiendas.** Comparten catálogo, inventario, clientes y pedidos; cada una tiene su diseño y puede abrir en un mercado. El mercado se decide así: el de la dirección (`?mk=`), si no el del dominio, si no el de la tienda. Una tienda no se borra: se archiva, porque su historial de publicaciones es inmutable.
- **Editor de imágenes.** Los cambios son una receta sobre el original, que nunca se toca: se pueden cambiar o deshacer cuando sea. Cada versión usa archivos nuevos, así que navegadores y CDN nunca muestran la anterior. El original solo se entrega al panel con sesión, porque puede llevar la ubicación de la cámara.
- **Punto de venta.** La venta descuenta primero las piezas de la tienda física elegida; si falla el cobro, el borrador se cancela y nada queda apartado.

## Defectos que encontraron las auditorías (todos corregidos)

- Ticket de caja impreso en blanco: la regla de impresión que ocultaba todo ganaba en especificidad a la que mostraba el ticket.
- Mesa vacía: la respuesta omitía la lista de productos y la vista de la cuenta fallaba.
- Al enviar a la mesa, los campos de dirección no se ocultaban: el estilo de los campos ganaba al atributo `hidden`.
- La hoja de empaque repetía «Para:» con el comprador y los festejados.
- Reportes: la zona horaria quedaba sin tipo cuando la consulta no agrupaba por tiempo; la vista desbordaba el ancho del teléfono.
- Borrar una tienda chocaba con el historial inmutable de publicaciones: ahora se archiva.
- Editor: las imágenes importadas sin original no se podían abrir (ahora usa su tamaño mayor) y la política de seguridad bloqueaba la vista previa (ahora se dibuja sin URL `blob:`).

## Velocidad medida (servidor de desarrollo)

- 22 rutas de la etapa: mediana de medianas **1.7 ms**, peor p95 **4.7 ms**, sin errores.
- Caja: búsqueda por código 3 ms, por palabra 2 ms. Reportes personalizados: 2 a 10 ms. Mesa de regalos: 2.4 ms. Blog: 1.9 ms.

## Lo que queda fuera de este entorno

- Probar Mercado Libre y Amazon con cuentas reales de vendedor (aquí se probaron contra dobles que siguen su API pública).
- Terminales de pago físicas integradas: la caja registra la autorización que da la terminal.
