# Ayuda del panel de Apexx
Cada sección: título, pantalla (ruta del panel) y permiso que hace falta para usarla.

## Agregar un producto
ruta: catalogo/productos/nuevo
permiso: catalog edit
Ve a Catálogo y pulsa «Agregar producto». Escribe el nombre, la descripción y al menos una variante con su SKU. El precio y las existencias se capturan en la misma pantalla. Guarda: el producto aparece en la tienda en milisegundos, sin reindexar.

## Importar productos desde un archivo CSV
ruta: catalogo/importar
permiso: catalog edit
En Catálogo, Importar, sube un CSV de hasta 8 000 productos. Revisa la vista previa: muestra qué se crea, qué se actualiza y los errores por renglón. Pulsa «Aplicar». Puedes descargar la plantilla de ejemplo en la misma pantalla.

## Borrar varios productos a la vez
ruta: catalogo
permiso: catalog admin
En la lista de productos marca las casillas y elige «Eliminar…». Se borran hasta 500 por vez, con sus precios, existencias y vectores, y queda registro en la auditoría.

## Cambiar precios de muchos productos
ruta: promociones/masivo
permiso: pricing edit
En Promociones, Cambio masivo, sube un archivo o elige productos y aplica un porcentaje o un precio fijo. El cambio se ve en la tienda en milisegundos y queda en el historial de precios.

## Crear una promoción o un cupón
ruta: promociones
permiso: promotions edit
En Promociones elige el tipo: descuento automático, cupón, 3×2 o envío gratis. Define a qué productos aplica, la vigencia y el límite de usos. Prueba la promoción en el Simulador antes de activarla.

## Probar una promoción antes de activarla
ruta: promociones/simulador
permiso: promotions read
En Promociones, Simulador, arma un carrito de prueba y mira qué descuentos aplican y por qué.

## Atender un pedido
ruta: pedidos
permiso: orders edit
En Pedidos abre el pedido. Desde ahí confirmas el pago, compras la guía, marcas como enviado y registras devoluciones o reembolsos. Todo queda en el historial del pedido.

## Hacer una devolución o un reembolso
ruta: pedidos/devoluciones
permiso: orders edit
En Pedidos, Devoluciones, registra los productos que regresan y el motivo. El reembolso se hace por Stripe al mismo medio de pago y el inventario se ajusta solo.

## Crear un pedido por teléfono o WhatsApp
ruta: pedidos/borradores
permiso: orders edit
En Pedidos, Pedidos borrador, captura los productos y el cliente y envía el enlace de pago. El pedido se confirma cuando el cliente paga.

## Ver las ventas y reportes
ruta: reportes
permiso: reports read
En Reportes ves ventas, productos más vendidos, clientes, embudo de conversión y promociones, por periodo y canal. Puedes exportar cada reporte.

## Ver el inventario y lo que está por agotarse
ruta: reportes/inventario
permiso: reports read
En Reportes, Inventario, filtra por agotados o con pocas existencias. El inicio del panel también avisa cuando algo se agota.

## Conectar Stripe para cobrar
ruta: pagos
permiso: payments admin
En Pagos pulsa «Conectar Stripe» y sigue los pasos de Stripe. Los datos de tarjeta nunca pasan por Apexx. Ahí también activas meses sin intereses.

## Configurar envíos
ruta: configuracion/envios
permiso: settings edit
En Configuración, Envíos, crea zonas por código postal o estado, tarifas por peso o por monto, envío gratis desde cierto monto y recoger en tienda.

## Conectar tu propio dominio
ruta: configuracion/dominios
permiso: settings admin
En Configuración, Dominios, escribe tu dominio y agrega en tu proveedor el registro DNS que te mostramos. El certificado HTTPS se emite solo.

## Editar el diseño de la tienda
ruta: builder
permiso: builder edit
En el Diseñador elige un tema, cambia colores, tipografía y secciones de cada página. Los cambios se guardan como borrador hasta que pulsas «Publicar». Puedes volver a una versión anterior.

## Escribir las políticas de la tienda
ruta: configuracion/politicas
permiso: settings edit
En Configuración, Políticas, edita envíos, devoluciones, privacidad y términos. Lo que escribas ahí también responde las preguntas de tus clientes en «Pregúntale a la tienda».

## Publicar un artículo en el blog
ruta: configuracion/blog
permiso: builder edit
En Configuración, Blog, crea un artículo, agrega portada y etiquetas y elige publicarlo ahora o en una fecha. Los artículos publicados también sirven para responder preguntas de clientes.

## Invitar a alguien del equipo
ruta: equipo
permiso: team admin
En Equipo pulsa «Invitar», escribe el correo y elige los permisos por área: catálogo, pedidos, reportes y demás. Cada persona entra con su propia cuenta.

## Mejorar lo que encuentra el buscador
ruta: busqueda
permiso: catalog edit
En Búsqueda y opiniones agrega sinónimos, redirecciones y productos fijados por término. En «Qué buscan» ves las búsquedas sin resultados con un sinónimo sugerido que agregas con un clic.

## Activar la búsqueda por significado y su presupuesto
ruta: busqueda/ia
permiso: catalog admin
En Búsqueda y opiniones, Recomendaciones e IA, enciende la búsqueda por significado y define el presupuesto mensual de IA. Al llegar al tope todo sigue funcionando sin costo.

## Moderar opiniones de clientes
ruta: busqueda/opiniones
permiso: catalog edit
En Búsqueda y opiniones, Opiniones, publica o rechaza cada opinión y responde como tienda. Solo las publicadas aparecen en la ficha y en el resumen de opiniones.

## Ver clientes y segmentos
ruta: clientes
permiso: customers read
En Clientes ves el historial de cada persona. En Segmentos agrupas clientes por reglas para campañas y precios.

## Enviar una campaña por correo
ruta: correos
permiso: customers edit
En Correos crea una campaña, elige el segmento y la fecha de envío. Las plantillas de correos de pedido también se editan ahí.

## Ventas a empresas (B2B)
ruta: empresas
permiso: customers edit
En Empresas registra cada empresa con sus compradores, crédito, lista de precios y aprobaciones. Las cotizaciones se gestionan en Pedidos, Cotizaciones.

## Revisar la auditoría
ruta: operacion/actividad
permiso: settings admin
En Operación, Actividad, ves quién hizo qué y cuándo. El registro está encadenado: nadie lo puede alterar sin que se note.

## Instalar Analytics, un píxel, un mapa de calor, un chat o WhatsApp
ruta: configuracion/scripts
permiso: builder admin
En Configuración, Etiquetas y píxeles, elige el proveedor (Google Tag Manager, Google Analytics 4, Adobe Analytics, Google Ads, Meta, TikTok, Pinterest, Snapchat, X, LinkedIn, Microsoft Advertising, Reddit, Hotjar, Clarity, Crazy Egg, chats como Crisp, Tawk.to, Tidio, Intercom, Zendesk, Trengo, JivoChat o HubSpot, el botón de WhatsApp, un script por URL o código propio) y llena sus datos. Las etiquetas de verificación de Search Console, Bing, Meta o Pinterest van en «Verificación de dominio». Pulsa «Publicar»: la tienda usa la versión nueva desde la siguiente página que se abra, sin despliegues. Los eventos de comercio (ficha, agregar al carrito, carrito, inicio del pago y pedido confirmado) llegan solos con monto, moneda y número de pedido. Verifica con «Probar en la tienda». En «Versiones» ves quién publicó, cuándo y qué cambió, y puedes regresar a cualquier versión anterior.

## Probar cambios en el sandbox antes de publicarlos
ruta: configuracion/sandbox
permiso: settings admin
En Configuración, Sandbox, pulsa «Crear sandbox»: en segundos tienes una copia completa de tu tienda (productos, precios, promociones, diseño, configuración y etiquetas), sin los datos personales de tus clientes ni tus credenciales. «Entrar al sandbox» te lleva a esa copia; una franja naranja te recuerda dónde estás. Cambia lo que quieras y, cuando esté listo, en «Publicar a producción» elige qué áreas pasan a tu tienda real. Cada publicación se puede revertir. Para ver la tienda del sandbox usa «Ver la tienda del sandbox» o comparte un enlace de vista previa; también puedes usar tu propio subdominio desde Configuración, Dominios, estando dentro del sandbox. En el sandbox nada sale a servicios reales: los pagos son de prueba, los correos se capturan y no se pueden conectar webhooks, canales de venta ni conversiones del servidor.

Tu plan dice cuántos sandboxes puedes tener y de qué tamaño; lo ves arriba de la pantalla. Si tienes varios, ponles nombre (por ejemplo «Black Friday») y pasa de uno a otro sin volver a producción. Puedes ponerle contraseña a la tienda de un sandbox para mostrarla a alguien de fuera, y en «Publicar a producción» elegir «En una fecha y hora» para que se publique solo: a esa hora se publica lo que tenga el sandbox, y puedes cancelarlo antes. En Operación, Estado ves cuánto se usa cada sandbox.

