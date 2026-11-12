# Plan de fases pendientes (trabajo directo, sin agentes)

Regla: una fase a la vez; cada fase incluye backend, admin (shadcn/ui), tienda si aplica, pruebas, capturas y subida a git.
Avances parciales de agentes detenidos: `.claude/worktrees/agent-*` (local, no está en git). Mapa de paneles: `docs/vtex-paneles.md`.

| Fase | Contenido | Estado |
| --- | --- | --- |
| A | Catálogo completo: categorías, marcas, colecciones, variantes, atributos, medios, CSV, filtros en tienda | **Hecha**: backend, admin (productos, editor con variantes, categorías, colecciones, marcas, atributos, medios, importar/exportar), filtros y páginas de categoría/colección en tienda |
| B | Promociones, cupones, campañas, listas de precios, simulador | **Hecha**: motor con 9 tipos y explicación, campañas, cupones (uno y en lote), listas de precios, cambio masivo con vista previa, historial, simulador, cupón en el carrito |
| C | Clientes, cuentas de comprador, envíos con guía, reembolsos parciales, devoluciones, edición de pedidos | **Hecha**: clientes (lista, ficha, segmentos por reglas, etiquetas, notas), cuentas de comprador con código por correo, envíos parciales con guía, reembolsos parciales idempotentes, devoluciones (RMA), pedidos borrador, vistas guardadas y exportación |
| D | Correos transaccionales, bitácora, carritos abandonados, newsletter | **Hecha**: 9 plantillas editables con vista previa en vivo y correo de prueba, bitácora de enviados con reintento, remitente/marca/proveedor (prueba, SMTP, SendGrid), carritos abandonados con recordatorio, boletín |
| E | Almacenes, zonas y tarifas de envío, transportistas, retiro, impuestos | **Hecha**: ubicaciones con existencias, traspasos y alertas; zonas, perfiles, paqueterías, opciones de entrega con tarifas por peso/monto, calendario y simulador; retiro en tienda; regiones de impuestos, clase por producto y calculadora; el comprador elige la entrega en carrito y checkout |
| F | Configuración de tienda, políticas, menús, redirecciones, sitemap, dominios | **Hecha**: datos de la tienda y logo, reglas de checkout, prefijo de pedido, canales, políticas publicables, menús (principal y pie, 3 niveles), redirecciones con prueba e importación, dominios con verificación DNS, sitemap y robots |
| G | Panel de inicio, embudo, reportes, registro de actividad | **Hecha**: inicio con ventas, pedidos, ticket, conversión y alertas; reportes de ventas, productos, clientes, embudo, promociones e inventario con comparación de periodos y CSV; medición de visitas sin cookies; registro de actividad verificable y estado del sistema |
| H | Búsqueda (sinónimos, merchandising, autocompletar), reseñas, listas de deseos | **Hecha**: sinónimos, productos fijados, redirecciones y ocultos (aplican al instante); sugerencias al escribir; términos buscados y búsquedas sin resultados; opiniones con moderación, respuesta y compra verificada; favoritos en el navegador y en la cuenta |
| I | Tarjetas de regalo, saldo a favor, suscripciones, MSI, antifraude | **Hecha**: tarjetas de regalo y saldo a favor (pago parcial o total, reembolso al saldo, código solo como huella), reembolso como saldo a favor, meses sin intereses por plazo y mínimo, antifraude (bloqueos, velocidad, revisión por monto), suscripciones con descuento, cobro automático a tarjeta guardada, reintentos, pausa/salto/cancelación por enlace |
| J | 2FA, sesiones, roles, invitaciones, idiomas, multi-tienda, feeds | **Hecha en lo esencial**: verificación en dos pasos (TOTP, QR, códigos de respaldo), sesiones por dispositivo, invitaciones por correo, roles propios, catálogo para Google Merchant y Meta. **Pendiente**: tienda en varios idiomas (hoy la tienda del comprador está solo en español) y selector para administrar varias tiendas con una misma cuenta (hoy cada tienda tiene su propio acceso) |
| K | Marketplace y portal de vendedores | **Hecha**: vendedores con comisión, productos por vendedor, “vendido por” en la ficha, estado de cuenta que sigue pagos, reembolsos y cancelaciones, registro de pagos y ajustes, portal del vendedor (pedidos por enviar con guía, productos, estado de cuenta) con acceso aislado. **Fuera de alcance**: pago automático a vendedores por Stripe Connect (hoy se registra la transferencia hecha por fuera) y que el vendedor edite precios o existencias desde su portal |
| L | B2B y entidades personalizadas | **Hecha**: empresas con descuento propio, plazo de pago y límite de crédito; compradores autorizados que entran con su cuenta; compra a crédito como un pago más (reembolsos y pagos bajan la deuda), estado de cuenta y vencidos; datos personalizados con campos propios, registros cifrados, formulario público con filtro antibots y exportación. **Fuera de alcance**: flujos de aprobación de compras dentro de la empresa y listas de precios por empresa a nivel SKU (hoy es un descuento general; las cotizaciones se cubren con los pedidos borrador) |
| Cierre | Auditorías completas (flujos, reversión, uso humano comprador y plataforma, resiliencia, seguridad) | **Hecho**: auditorías de acceso (295 rutas), aislamiento (107 tablas), reversión, caídas, velocidad y uso humano por rol; 7 defectos encontrados y corregidos. Resultado y pendientes externos en [`docs/audit/cierre.md`](audit/cierre.md) |

Entorno local: Postgres en 127.0.0.1:5432 (arrancar con pg_ctl -D /var/lib/apexx-pg/data), variables en el scratchpad `dev.env`, tienda de QA con `apexx qa-setup`.

## Después de la fase L (noviembre 2026)

Hecho y probado:

- Panel con tres temas (rosa, lavanda, niebla) en lugar de claro y oscuro.
- Encabezado y pie editables como secciones en el diseñador; etiquetas para redes sociales.
- B2B: precio por producto por empresa y aprobaciones de compra.
- Varias tiendas con una misma cuenta (elegir tienda al entrar y cambiar desde el panel).
- Tienda en seis idiomas (es, en, fr, pt, id, ja) con una dirección por idioma y hreflang; `internal/i18n`.
- Panel en seis idiomas con diccionario de 792 frases por idioma; `web/src/i18n.ts`.
- Pruebas automáticas propias de clientes y devoluciones.

Pendiente de lo pedido: pago automático a vendedores (hoy se registra la transferencia hecha por fuera).

Siguiente etapa: ver `docs/audit/benchmark-plataformas.html`, que compara función por función
con Shopify, Magento y Medusa y ordena lo que falta en siete etapas.

Plan de las etapas 0 a 7 con metas de velocidad y auditorías: `docs/PLAN-SUPERIORIDAD.html`.

## Etapas del plan de superioridad

- Etapa 1 (mercados): hecha.
- Etapa 3 (B2B profundo): hecha; detalle y auditorías en [`docs/fases/etapa-3-b2b.md`](fases/etapa-3-b2b.md).
- Etapa 4 (logística y pagos conectados): hecha; detalle en [`docs/fases/etapa-4-logistica-pagos.md`](fases/etapa-4-logistica-pagos.md).
- Etapa 5 (tipos de producto): hecha; detalle en [`docs/fases/etapa-5-tipos-producto.md`](fases/etapa-5-tipos-producto.md).
