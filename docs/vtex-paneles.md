# Mapa de paneles: VTEX → Apexx

Objetivo del dueño del proyecto: **cada panel de VTEX, mejorado, en Apexx.** Este documento es la lista de control. Se actualiza en cada integración; un panel solo pasa a "Hecho" cuando tiene backend, pantalla en el admin, pruebas automáticas, auditoría de uso humano y de reversión.

Estados: **Hecho** · **En construcción** (frente de trabajo activo) · **Pendiente** (fase futura).

Frentes activos (cada uno es un agente que trabaja en una copia aislada del repositorio):
F1 Catálogo y medios · F2 Promociones y precios · F3 Clientes, postventa y correos · F4 Envíos, impuestos y configuración · F5 Analítica y panel · F6 Búsqueda, reseñas y listas de deseos · F7 Marketplace y portal de vendedores · F8 Tarjetas de regalo, saldo, suscripciones y reglas de pago · F9 B2B y entidades personalizadas · F10 Cuenta, seguridad, idiomas, multi-tienda y apps · T Marca y temas.

## Pedidos

| Panel VTEX | Apexx | Estado | Mejora frente a VTEX |
| --- | --- | --- | --- |
| Todos los pedidos (OMS) | Pedidos | Hecho (base) → F3 profundidad | Búsqueda real, vistas guardadas, historial inmutable por evento |
| Detalle / workflow del pedido | Pedido | Hecho (base) → F3 | Máquina de estados explícita, reversión completa probada |
| Facturas | Facturas (Stripe Invoicing) | Hecho | Emisión automática con reintentos; CFDI vía conector (pendiente) |
| Transacciones | Transacciones | F8 | Ledger de doble entrada visible |
| Devoluciones (Return app) | Devoluciones | F3 | Reposición en la ubicación exacta, reembolso parcial idempotente |
| Cancelaciones | Cancelar y reembolsar | Hecho | Una sola acción revierte stock, dinero, contabilidad y factura |
| Edición de pedido | Editar pedido | F3 | Diferencia de precio calculada y explicada |
| Pedidos de atención (call center) | Pedidos borrador | F3 | |
| Suscripciones | Suscripciones | F8 | Reintentos de cobro con calendario y pausa automática |
| Configuración de OMS | Configuración → Checkout | F4 | |

## Catálogo

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Productos y SKUs | Productos | Hecho (base) → F1 | Matriz de variantes, edición masiva, cambios visibles en ~3 ms (sin indexador) |
| Categorías | Categorías (árbol) | F1 | Arrastrar para ordenar |
| Marcas | Marcas | F1 | |
| Especificaciones / grupos de campos | Atributos | F1 | Tipados y filtrables |
| Colecciones | Colecciones (manuales y por reglas) | F1 | |
| Importar / exportar | Importación CSV con vista previa | F1 | Errores por fila antes de aplicar |
| Imágenes | Biblioteca de medios | F1 | Variantes WebP y `srcset` automáticos |
| Reseñas y calificaciones | Reseñas | F6 | Compra verificada, moderación |
| Listas de deseos | Listas de deseos y alertas de stock | F6 | |
| SKUs recibidos (marketplace) | Ofertas por aprobar | F7 | Diferencias claras, aprobación masiva |

## Búsqueda (Intelligent Search)

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Sinónimos | Sinónimos | F6 | |
| Reglas de merchandising | Reglas (fijar, impulsar, ocultar) | F6 | Vista previa antes/después |
| Redirecciones de búsqueda | Redirecciones | F6 | |
| Facetas | Facetas desde atributos filtrables | F1 | |
| Reportes de búsqueda | Analítica de búsqueda | F6 | Sin datos personales |
| Autocompletar | Autocompletar | F6 | < 20 ms en memoria |

## Promociones y precios

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Promociones (Rates & Benefits) | Promociones | Hecho (2 tipos) → F2 todos los tipos | Cada promoción explica por qué aplica o no |
| Cupones | Cupones | F2 | Nunca se muestran como aplicados si no lo están (bug observado en VTEX) |
| Campañas / audiencias | Campañas | F2 | Presupuesto por campaña |
| Límites de promoción | Límites por uso y por cliente | F2 | Seguros ante concurrencia |
| Tablas de precios | Listas de precios | Hecho (escalonados) → F2 | Programadas, historial por SKU |
| Reglas de precio | Ajustes por lista, cambio masivo con vista previa | F2 | |
| Impuestos (calculadora) | Impuestos | F4 | IVA 16% y frontera 8% explicados |
| Tarjeta de regalo | Tarjetas de regalo | F8 | Pago combinado con tarjeta |

## Inventario y envíos

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Inventario | Inventario | Hecho (base) → F4 | Una sola fuente de verdad, reservas reales |
| Almacenes / docks | Ubicaciones | F4 | Transferencias auditadas |
| Políticas de envío | Envíos (zonas por CP, tarifas) | F4 | Aplicación inmediata (VTEX indexa la logística) |
| Puntos de retiro | Retiro en tienda | F4 | |
| Feriados | Calendario de feriados | F4 | |
| Ventanas de entrega | Estimaciones con hora de corte | F4 | |
| Reservas | Reservas visibles por SKU | Hecho (API) → F4 pantalla | El carrito sí reserva (VTEX no) |

## Clientes

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Master Data (CL, AD) | Clientes | F3 | Datos personales cifrados por tienda |
| Master Data (entidades propias) | Entidades personalizadas | F9 | Consultas reales sin los límites de VTEX |
| B2B (organizaciones, centros de costo) | Empresas | F9 | Aprobaciones y crédito |
| Cotizaciones | Cotizaciones | F9 | Versiones |
| Saldo a favor (Customer Credit) | Saldo a favor | F8 | Ledger completo |
| Segmentos / audiencias | Segmentos | F3 | |

## Tienda (CMS)

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Site Editor | Diseño de la tienda (builder) | Hecho (con temas) | Mover un módulo: segundos, publicar y deshacer en milisegundos |
| Páginas / plantillas | Plantillas y temas | Hecho: 4 temas, 14 secciones | 4 temas originales con estilos editables |
| Redirecciones | Redirecciones 301 | F4 | |
| Sitemap / robots | Sitemap con fechas reales | F4 | VTEX publica `lastmod` falsos |
| Mensajes / traducciones | Idiomas y traducciones | F10 | |
| Menús | Navegación | F4 | |
| Configuración de tienda | Configuración → Tienda | F4 | |
| Bindings / multi-tienda | Sitios | F10 | |

## Marketplace

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Gestión de vendedores | Vendedores | F7 | |
| Seller Portal | Portal de vendedores | F7 | |
| Comisiones | Reglas de comisión | F7 | Ledger y pagos con Stripe Connect |
| Afiliados / conexiones (Mercado Libre…) | Canales y conectores | F7 + F10 | Eventos con estado completo, sin depender de índices |

## Pagos

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Afiliaciones de gateway | Pagos (Stripe por tienda) | Hecho | Connect o llaves propias cifradas |
| Condiciones de pago / cuotas | Meses sin intereses | F8 | |
| Antifraude | Antifraude y cola de revisión | F8 | |
| Transacciones | Transacciones | F8 | |

## Cuenta y plataforma

| Panel VTEX | Apexx | Estado | Mejora |
| --- | --- | --- | --- |
| Usuarios (License Manager) | Equipo (puestos ilimitados) | Hecho | Permisos por módulo y nivel |
| Roles | Roles reutilizables | F10 | |
| Llaves de aplicación | API keys | Hecho → F10 alcances y vencimiento | |
| Autenticación (2FA, SSO) | Seguridad | F10 | TOTP, passkeys, OIDC |
| App Store | Apps | F10 | Feeds de Google y Meta reales |
| Message Center | Correos transaccionales | F3 | Vista previa y bitácora |
| Facturación de la cuenta | Plan y facturación (precio único) | F10 | |
| Insights / reportes | Inicio y reportes | F5 | Embudo sin terceros |
| Auditoría | Registro de actividad | Hecho (API) → F5 pantalla | Cadena con hash verificable |

## Fases pendientes (lo que todavía no tiene frente asignado)

0. **Detectado en temas:** el newsletter solo confirma en pantalla (falta conectarlo a clientes/correos de F3); encabezado y pie no se editan desde la lista de secciones del builder; carga de imágenes propias (llega con F1).

1. **CFDI 4.0 con Facturapi** (conector de factura legal mexicana).
2. **Conectores concretos de marketplaces externos** (Mercado Libre, Amazon, Liverpool) sobre el contrato de eventos.
3. **Secciones del builder en TSX/Preact** compiladas al publicar (hoy plantillas Go).
4. **Pruebas contra Stripe en modo test real** con llaves de una tienda.
5. **Alta disponibilidad en GCP real** (grupo de instancias, Cloud SQL) verificada en un proyecto.
6. **Pruebas de carga Black Friday ×10** y pentest externo.

## Auditorías que se repiten después de cada integración

- Flujos y reversión de todas las áreas (compra, cancelación, devolución, edición, promociones, tarjetas de regalo, suscripciones, marketplace, B2B).
- Uso humano como comprador (móvil y escritorio) y como cada rol de la plataforma, con tiempos y fricción.
- Resiliencia (caída de proceso, de base de datos, de proveedor de pagos, multi-nodo).
- Seguridad (aislamiento entre tiendas en cada tabla nueva, permisos por módulo, datos personales cifrados).
- Rendimiento (Core Web Vitals de cada tema, latencias de API).
