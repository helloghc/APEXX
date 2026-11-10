# Apexx — Stack integral y plan a producción

> Basado en las auditorías de `docs/audit/` (backend, frontend, builder, checkout y pagos).
> Criterio: cada capa se elige por velocidad para el comprador, seguridad de cada transacción,
> protección de datos personales y aislamiento entre clientes (tenants). Go y React son el punto
> de partida propuesto; abajo se comparan contra las alternativas y se indica dónde conviene otra pieza.

## 1. Stack recomendado

### 1.1 Backend

| Capa | Elección | Alternativas evaluadas | Por qué |
| --- | --- | --- | --- |
| Lenguaje núcleo | **Go** | Rust, Java/Kotlin, Node.js, Elixir | Latencia p99 estable, concurrencia simple, binario único, contratación viable en LatAm. Rust gana en CPU pura, pero el comercio electrónico es I/O; el costo de desarrollo no se justifica en el núcleo. |
| Arquitectura | **Monolito modular → servicios por celda** | Microservicios desde el día 1 | Un binario con módulos por dominio (hoy en el repo). Se separa un módulo cuando su carga lo exige. Menos red, menos fallas parciales. |
| API pública | **REST + OpenAPI 3.1** y **GraphQL de storefront con consultas persistidas** | Solo REST, solo GraphQL | REST para integradores y ERP. GraphQL solo para el storefront, con *dataloaders* para evitar el N+1 (VTEX hace 90 llamadas de reseñas en una categoría) e introspección apagada en producción. |
| API interna | **ConnectRPC** (gRPC + JSON) | gRPC puro, REST interno | Contratos tipados, funciona en navegador y servidor. |
| Base de datos | **PostgreSQL 17** con **Row Level Security** por tenant | CockroachDB, Spanner, MySQL, MongoDB | Transacciones fuertes para dinero y stock, JSONB para atributos, ecosistema maduro. CockroachDB/Spanner solo si un tenant necesita escrituras multi-región activas. |
| Escala de datos | **Celdas**: cada celda es un Postgres primario + réplicas en una región; tenants grandes en celda dedicada | Un único clúster gigante | Limita el radio de una falla a una celda; evita el vecino ruidoso; permite residencia de datos por país. |
| Caché | **Valkey** + caché en proceso | Memcached | Precios, stock y sesión; invalidación por eventos, nunca por tiempo. |
| Eventos | **NATS JetStream** + patrón *outbox* | Kafka, Redpanda | Más simple de operar que Kafka con la latencia que necesitamos. Outbox garantiza que ningún evento se pierda si la transacción se confirma. |
| Flujos de pedido y pago | **Temporal** (SDK de Go) | Máquinas de estado propias, colas | Reintentos, timeouts y compensaciones durables para pedido → pago → facturación → envío. |
| Búsqueda | **Typesense** por celda; **Vespa** cuando haya personalización a gran escala | OpenSearch, Meilisearch, Algolia | Typesense: facetas y vectores en milisegundos, operación sencilla. OpenSearch es más pesado de operar. Algolia es caro y externo. |
| Medios | **imgproxy** (libvips) detrás del CDN, origen en S3/R2 | Cloudinary | AVIF/WebP y `srcset` automáticos, sin costo por transformación. |
| Extensiones de terceros | **WebAssembly con wazero** + webhooks firmados | VTEX IO, scripts arbitrarios | Código de terceros aislado con límites de CPU, memoria y permisos explícitos. |

### 1.2 Frontend

| Superficie | Elección | Alternativas evaluadas | Por qué |
| --- | --- | --- | --- |
| Componentes de sección | **TSX con API de React**, renderizados con **Preact** | React completo, Vue, Svelte | El equipo y la comunidad escriben React. Preact pesa ~4 KB frente a ~45 KB de React DOM. |
| Storefront | **HTML estático generado al publicar + islas** (Astro como compilador de secciones) | Next.js, Remix/React Router, Hydrogen | Cero JavaScript por defecto. Next.js envía el runtime de React en cada página y empuja a hosting específico. Meta: < 100 KB JS en producto, frente a 861 KB en VTEX. |
| Renderizado en el borde | **Cloudflare** (CDN, Workers, R2, WAF, bot management, Turnstile) | Fastly, CloudFront, Vercel | HTML por revisión en 300+ ciudades; precio y stock se inyectan en el borde o con una isla pequeña. |
| Checkout | **App propia** con una sola isla Preact y **campos de pago alojados por el proveedor** (iframes) | Checkout VTEX (jQuery 1.8 + Bootstrap 2) | < 60 KB de JS, un solo stack del catálogo al pago, PCI SAQ A. |
| Admin y Builder | **React 19 + Vite + TanStack Router/Query + Radix UI + dnd-kit + Zod** | Next.js, Angular | Aplicación de trabajo intensiva; no necesita SEO. dnd-kit para arrastrar secciones; Zod comparte esquemas con el JSON Schema de las secciones. |
| Colaboración en el editor | **Yjs** (fase posterior) | Bloqueo pesimista | Varias personas editando la misma página sin pisarse. |
| Apps móviles | **React Native (Expo)** sobre la misma API | Nativas | Reutiliza tipos y lógica; fase posterior. |

### 1.3 Seguridad de transacciones y datos

| Riesgo | Control |
| --- | --- |
| Datos de tarjeta | Nunca tocan Apexx: tokenización y *hosted fields* del proveedor (Mercado Pago, Stripe, Adyen, Conekta, Openpay). Alcance PCI DSS **SAQ A**. 3-D Secure 2. |
| Cobros duplicados | **Claves de idempotencia** obligatorias en pedidos y pagos; flujos en Temporal con compensación. |
| Dinero de marketplace | **Libro mayor de doble entrada** inmutable para splits, comisiones y reembolsos; conciliación diaria automática. |
| Datos personales | **Cifrado por campo** (nombre, correo, teléfono, dirección, RFC) con **llave por tenant** (cifrado de sobre con KMS, librería Tink). Borrar un tenant destruye su llave (*crypto-shredding*). Logs sin PII. |
| Acceso de administradores | SSO (OIDC/SAML), **passkeys o MFA obligatorio**, sesiones cortas, permisos finos con **OpenFGA**, auditoría *append-only* encadenada por hash. |
| Acceso de compradores | Passkeys + código por correo; cookies `HttpOnly`, `Secure`, `SameSite`. |
| Ataques web | CSP estricta con *nonces*, Trusted Types, protección CSRF, escape automático en plantillas (probado en el repo), WAF y gestión de bots en el borde, límites de tasa por tenant e IP. |
| Cadena de suministro | `govulncheck` y escaneo de dependencias en CI, SBOM (Syft), imágenes firmadas (Cosign), procedencia SLSA nivel 3, contenedores *distroless* sin shell y sin root. |
| Secretos | Vault o KMS del proveedor de nube; nunca en variables de repositorio ni en código. |
| Red interna | mTLS entre servicios (Linkerd); bases de datos sin acceso público. |
| Cumplimiento | SOC 2 Tipo II, ISO 27001, PCI DSS SAQ A, LFPDPPP (México), LGPD (Brasil), GDPR; residencia de datos por tenant. |

### 1.4 Aislamiento de cada cliente (tenant)

1. **Datos:** `tenant_id` en cada fila + RLS de Postgres activada por transacción (`SET LOCAL app.tenant_id`). Un error de código no puede leer datos de otra tienda porque la base lo impide. Pruebas automáticas que intentan cruzar tenants en cada PR.
2. **Cómputo:** cuotas de CPU, memoria y peticiones por tenant; colas separadas por prioridad.
3. **Celdas:** los tenants se reparten en celdas; un tenant enterprise puede tener su propia celda, base y llaves.
4. **Llaves:** una llave de cifrado por tenant; rotación automática.
5. **Dominio:** cada tienda en su dominio, sin cookies compartidas entre tiendas.
6. **Extensiones:** WASM sin acceso a red ni a disco salvo permisos declarados.

### 1.5 Operación global

- **Kubernetes** (EKS o GKE) con **Argo CD** (GitOps) y **OpenTofu** para la infraestructura.
- Regiones iniciales: EE. UU. Este, México (AWS `mx-central-1`) y São Paulo. Cada tenant tiene una región "hogar" para su base de datos; el storefront se sirve desde el borde en todo el mundo.
- **OpenTelemetry** en todo; Grafana (Tempo, Loki, Mimir) o Datadog.
- **SLO:** storefront 99,95%, checkout 99,99%, p99 de API < 100 ms en la región.
- Despliegues *canary* con Argo Rollouts, pruebas de carga k6 en CI (Black Friday ×10), pruebas de caos trimestrales.
- **RUM**: Core Web Vitals reales de cada tienda visibles para el comerciante.

## 2. Plan paso a paso

Equipo de referencia: 10 a 12 personas (8 backend/frontend, 1 SRE, 1 seguridad, 1 diseño de producto, 1 PM). Cada fase tiene criterios de salida medibles; no se pasa a la siguiente sin cumplirlos.

| Fase | Semanas | Entregables | Criterio de salida |
| --- | --- | --- | --- |
| **0. Descubrimiento** | hecho | Auditorías de VTEX (backend, frontend, builder, checkout); núcleo Go: catálogo, inventario, precios, promociones explicadas, simulación de carrito, builder con publicación atómica | Pruebas en verde; benchmarks publicados |
| **1. Fundaciones** | 1–6 | Postgres + migraciones + RLS; auth (OIDC, passkeys); modelo multi-tenant; OpenAPI 3.1 + linter; outbox + NATS; OpenTelemetry; staging en Kubernetes con Argo CD | Prueba automática de fuga entre tenants en verde; despliegue en staging en < 10 min |
| **2. Comercio núcleo** | 6–14 | Catálogo completo (categorías, marcas, especificaciones, colecciones); precios por canal y escalonados; inventario multi-bodega con reservas; carrito y checkout; pedidos en Temporal; pagos (Mercado Pago y Stripe); correos transaccionales | Compra completa en staging; p99 simulación < 50 ms; cero cobros duplicados en pruebas de caos |
| **3. Builder v1** | 10–20 | Editor React (arrastrar y soltar, vista previa en vivo, deshacer); 25 secciones; temas con tokens; medios con imgproxy; publicación en el borde; dominios propios con TLS automático | Un no experto mueve un módulo y publica en < 30 s; LCP móvil < 1,5 s en producto |
| **4. Paridad comercial** | 16–28 | Búsqueda Typesense; motor de promociones completo; logística (políticas, retiro en tienda, feriados); **importador desde VTEX** usando las APIs auditadas; facturación electrónica MX vía PAC | Migración de `elpaps` completa a Apexx en staging; búsqueda p99 < 30 ms |
| **5. Piloto** | 24–32 | 1 a 3 tiendas reales en producción limitada; RUM; soporte 24/7 | 30 días sin incidentes severos; Core Web Vitals en verde |
| **6. Endurecimiento y GA** | 28–40 | Pentest externo; preparación SOC 2; prueba de carga Black Friday ×10; multi-región; marketplace y B2B | Pentest sin hallazgos críticos abiertos; SLO cumplidos 60 días |

## 3. Estado actual del código frente a producción

| Área | Estado | Detalle |
| --- | --- | --- |
| Modelo de dinero, errores RFC 9457, paginación por cursor, ETag, compresión | **Hecho** | Probado |
| Inventario con reservas atómicas y eventos | **Hecho (memoria)** | Falta persistencia en Postgres |
| Precios escalonados por canal | **Hecho (memoria)** | Falta persistencia y listas por cliente |
| Promociones con explicación | **Parcial** | 2 tipos de regla; faltan cupones, combos, límites |
| Simulación de carrito con totales explícitos | **Hecho (memoria)** | 14 µs por simulación |
| Builder: plantillas, esquemas, presupuestos, publicación y rollback | **Hecho (memoria)** | 0,69 ms por mover y publicar; falta editor visual React |
| Persistencia (Postgres + RLS) | Pendiente | Fase 1 |
| Autenticación y permisos | Pendiente | Fase 1; hoy un encabezado identifica al autor |
| Pagos, pedidos, facturación | Pendiente | Fase 2 |
| Editor visual y storefront React/Preact | Pendiente | Fase 3 |
| Docker distroless, CI con pruebas, race detector y govulncheck | **Hecho** | |
| Observabilidad, despliegue, multi-región | Pendiente | Fases 1 y 6 |

**Conclusión:** el núcleo de dominio es de calidad de producción (probado, rápido, con contratos limpios), pero **el sistema todavía no puede salir a producción**: falta persistencia, autenticación, pagos y el editor visual. El plan anterior es el camino para llegar ahí.
