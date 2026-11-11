# Fase 7 — Paneles completos (sub-fases A a L)

Fecha: 2026-11-11. Plan y estado: [`docs/PLAN-FASES.md`](../PLAN-FASES.md).

Esta fase lleva Apexx de "núcleo que funciona" a "todos los paneles que un
comercio usa a diario". Se construyó por sub-fases; cada una se subió con sus
pruebas automáticas y un recorrido en navegador como persona (dueño, miembro
con permisos limitados, vendedor y comprador).

Convenciones que aplican a todas:

- Cada tabla nueva tiene aislamiento por tienda (Row Level Security forzado).
- Los cambios de configuración se leen de memoria y aplican al instante: no
  hay reindexación ni "publicar para que se vea".
- Dinero en centavos enteros; cada movimiento de dinero cuadra en el libro
  contable (la suma de cada transacción es cero) y hay prueba que lo verifica.
- Datos personales cifrados con la llave de la tienda; lo que se busca por
  valor exacto se guarda como huella con llave, nunca en claro.
- Errores en español con el campo que falla; nada de pantallas en blanco.

## A. Catálogo

Productos con variantes, categorías en árbol, marcas, colecciones manuales y
por reglas, atributos filtrables, biblioteca de imágenes con recortes y WebP,
importar y exportar CSV, publicación programada. En la tienda: filtros con
conteo, orden, páginas de categoría y colección.

- Código: `internal/catalog`, `internal/media`, `internal/api/routes_catalog.go`, `web/src/pages/catalog/`
- Migración: `010`. Pruebas: `internal/api/catalog_integration_test.go`.

## B. Promociones y precios

Motor con 9 tipos de promoción y explicación de por qué aplica o no, campañas
con presupuesto, cupones individuales y en lote con límites que no se pueden
rebasar con dos compras simultáneas, listas de precios, cambio masivo con
vista previa e historial, simulador.

- Código: `internal/promo`, `internal/promostore`, `web/src/pages/promotions/`
- Migraciones: `020`, `021`. Pruebas: `promotions_integration_test.go`.

## C. Clientes y postventa

Clientes con ficha, etiquetas, notas y segmentos por reglas; cuenta del
comprador con código por correo; envíos parciales con guía y rastreo;
reembolsos parciales idempotentes con cotización previa; devoluciones (RMA)
con recepción y reingreso a inventario; pedidos borrador con enlace de pago;
vistas guardadas y exportación.

- Código: `internal/customers`, `internal/orders/{depth,money,fulfill}.go`, `internal/api/routes_customers.go`, `web/src/pages/{orders,customers}/`
- Migraciones: `030`, `031`, `033`. Verificación: `qa/aftersale-smoke.py` y recorrido en navegador.
- Pendiente: pruebas Go propias de clientes y devoluciones (hoy cubiertas por el guion de humo y por las pruebas de dinero de la sub-fase I).

## D. Correos

Plantillas editables con vista previa en vivo y correo de prueba, bitácora de
enviados con reintento, remitente y marca, proveedor (modo de prueba, SMTP o
SendGrid), recordatorio de carrito abandonado, boletín.

- Código: `internal/notify`, `web/src/pages/notifications/`. Migración: `032`.
- Plantillas: pedido confirmado, enviado, entregado, reembolso, devolución,
  código de acceso, factura, carrito abandonado, enlace de pago, tarjeta de
  regalo, suscripción activa, suscripción con pago pendiente, invitación.

## E. Envíos, ubicaciones e impuestos

Ubicaciones con existencias, traspasos y alertas de bajo inventario; zonas por
estado o código postal, perfiles de envío, paqueterías, opciones con tarifa
fija, por peso o por monto, corte del día, días hábiles y festivos; retiro en
tienda; simulador. Impuestos por región y por clase de producto con desglose.
El comprador elige la entrega en carrito y checkout con fecha estimada.

- Código: `internal/logistics`, `internal/api/settings_{locations,shipping}.go`, `web/src/pages/settings/`
- Migraciones: `040`, `041`, `043`.

## F. Configuración de la tienda

Datos fiscales y logo, reglas de checkout (teléfono obligatorio, nota, casilla
de términos), prefijo del número de pedido, canales de venta, políticas
publicables, menús de la tienda (3 niveles), redirecciones con prueba y carga
masiva, dominios propios con verificación DNS, sitemap y robots.

- Código: `internal/sitecfg`, `internal/api/settings_{store,public}.go`. Migración: `042`.

## G. Inicio, reportes y operación

Inicio con ventas, pedidos, ticket y conversión comparados con el periodo
anterior; reportes de ventas, productos, clientes, embudo, promociones e
inventario con exportación; medición de visitas sin cookies que respeta "no
rastrear"; registro de actividad con cadena verificable; estado del sistema.

- Código: `internal/reports`, `internal/api/reports.go`, `web/src/pages/reports/`. Migración: `050`.
- Pruebas: `internal/reports/reports_test.go`, `internal/api/reports_test.go`.

## H. Búsqueda, opiniones y favoritos

Sinónimos, productos fijados por término, redirecciones de búsqueda y
productos ocultos; sugerencias al escribir; términos más buscados y búsquedas
sin resultado; opiniones con moderación, respuesta pública y marca de compra
verificada (el correo del autor no se guarda); favoritos en el navegador y
sincronizados con la cuenta.

- Código: `internal/engage`, `internal/api/engage.go`, `internal/store/engage.go`, `web/src/pages/engage/`. Migración: `060`.
- Pruebas: `engage_integration_test.go`, `internal/engage/search_test.go`, `internal/catalog/browse_synonyms_test.go`.

## I. Medios de pago avanzados y suscripciones

- **Tarjetas de regalo y saldo a favor**: entran al pedido como un pago más.
  Se gastan de forma atómica, vuelven si el pago con tarjeta falla y al
  reembolsar regresan a la misma tarjeta. Del código solo se guarda una huella.
- **Reembolso como saldo a favor**: el dinero se queda en la tienda y el
  cliente recibe un código por correo.
- **Meses sin intereses**: plazos y compra mínima por tienda (Stripe installments).
- **Antifraude**: listas de bloqueo, límite de pedidos por correo y por
  conexión, revisión por monto; el pedido marcado muestra el motivo y se aprueba.
- **Suscripciones**: descuento por suscripción, tarjeta guardada en Stripe,
  cobro automático con llave idempotente por ciclo e intento, tres reintentos,
  aviso por correo, y el cliente pausa, salta o cancela desde un enlace.

- Código: `internal/orders/{wallet,risk,subscriptions}.go`, `internal/subs`, `internal/api/{wallet,subscriptions}.go`, `web/src/pages/{wallet,subscriptions}/`
- Migraciones: `070`, `071`. Pruebas: `wallet_integration_test.go`, `subscriptions_integration_test.go`.
- Límites: una suscripción no se paga con tarjeta de regalo ni a meses; el
  cobro recurrente real requiere una cuenta de Stripe (en modo de prueba se simula).

## J. Seguridad de cuentas, equipo y catálogos para anuncios

Verificación en dos pasos (TOTP con QR, códigos de respaldo de un solo uso,
rechazo de códigos repetidos), sesiones por dispositivo con cierre remoto,
invitaciones por correo donde la persona crea su contraseña, roles propios,
restablecer dos pasos por un administrador. Catálogo para Google Merchant
(XML) y Meta (CSV) con clave rotativa.

- Código: `internal/auth/security.go`, `internal/api/{account,feeds}.go`, `web/src/pages/account/`, `web/src/pages/settings/Feeds.tsx`. Migración: `080`.
- Pruebas: `account_integration_test.go`, `feeds_integration_test.go`.
- **No incluido**: tienda del comprador en varios idiomas y selector para
  administrar varias tiendas con una sola cuenta.

## K. Marketplace

Vendedores con comisión, productos por vendedor, "vendido y enviado por" en la
ficha, estado de cuenta que sigue cada evento del pedido con la comisión del
momento de la venta, pagos y ajustes con referencia, y portal del vendedor:
sus pedidos por enviar (dirección sin el correo del cliente), registro de
guía, productos y estado de cuenta. La persona del vendedor no puede abrir
nada más de la tienda.

- Código: `internal/orders/sellers.go`, `internal/api/marketplace.go`, `web/src/pages/marketplace/`. Migración: `090`.
- Pruebas: `marketplace_integration_test.go`.
- **No incluido**: pago automático a vendedores (Stripe Connect transfers) y
  edición de precios o existencias desde el portal.

## L. Empresas (B2B) y datos personalizados

- **Empresas**: descuento sobre precio de lista, plazo (contado, 15, 30 o 60
  días) y límite de crédito. Solo los compradores autorizados, con su cuenta
  iniciada en la tienda, ven el precio de empresa y la opción de pagar a
  crédito. El crédito entra al pedido como un pago: no se puede rebasar el
  límite con dos compras simultáneas, un reembolso baja la deuda y el estado
  de cuenta muestra vencidos. Una llave pública de tienda no puede gastar el
  crédito de una empresa.
- **Datos y formularios**: la tienda define sus propias entidades y campos;
  los registros se guardan cifrados; el formulario público valida cada campo,
  limita envíos por conexión, descarta bots con un campo oculto y se exporta a CSV.

- Código: `internal/orders/company.go`, `internal/api/{b2b,entities}.go`, `web/src/pages/b2b/`. Migración: `100`.
- Pruebas: `b2b_integration_test.go`, `entities_integration_test.go`.
- **No incluido**: aprobaciones de compra dentro de la empresa y precios por SKU por empresa.

## Defectos encontrados al usarlo como persona (y corregidos)

| Dónde | Qué pasaba |
| --- | --- |
| Clientes | La lista fallaba porque el servidor devolvía las etiquetas en otra forma |
| Navegación | No existían los menús iniciales (principal y pie) y las categorías salían con su identificador |
| Búsqueda | Un producto fijado no aparecía si su nombre no coincidía con el término |
| Suscripciones | El selector de frecuencia salía vacío en la ficha del producto |
| Carritos abandonados | Un carrito con suscripción no se guardaba |
| Invitaciones | Pegar el enlace con el panel ya abierto dejaba la pantalla de inicio de sesión |
| Mi cuenta | Un miembro sin permisos amplios veía "Sin acceso" en su propia cuenta |
| Catálogo para anuncios | Una llave pública de tienda podía leer la clave del catálogo |

## Cómo repetir las verificaciones

```sh
# Pruebas automáticas (necesitan un PostgreSQL 16 local)
APEXX_TEST_ADMIN_URL=postgres://postgres@127.0.0.1:5432/postgres go test ./...
cd web && npm test && npx tsc -b
```

Los recorridos en navegador usan la tienda de QA descrita en [`docs/qa.md`](../qa.md).
