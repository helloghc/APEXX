# Apexx

Plataforma de comercio electrónico multi-tienda con builder visual, escrita en Go (backend) y React (editor).
Diseñada a partir de auditorías profundas de VTEX (`docs/audit/`) para resolver sus problemas de fondo:
indexación lenta, stock desalineado, APIs inconsistentes, checkout antiguo y módulos difíciles de mover.

- Plan, stack y estado: [`docs/plan.md`](docs/plan.md)
- Fases documentadas con evidencia: [`docs/fases/`](docs/fases/) (índice en [`docs/fases/README.md`](docs/fases/README.md))
- IA (búsqueda por significado, «Pregúntale a la tienda», asistentes, grafo, copilotos): [`docs/fases/resumen-ia.md`](docs/fases/resumen-ia.md)
- Escala con 50 000 productos y reversión total: [`docs/audit/escala-50k.md`](docs/audit/escala-50k.md)
- Despliegue en Google Cloud y despliegue sin corte: [`docs/deploy.md`](docs/deploy.md) (§7.1)
- Guía de QA para personas (comprador, panel, roles, IA): [`docs/qa.md`](docs/qa.md); herramientas automáticas en [`tools/qa/`](tools/qa/)

## Arquitectura en una línea

PostgreSQL (fuente de verdad, RLS por tienda) → outbox transaccional → proyector en memoria por tienda
(sin indexador, cambios visibles en ~3 ms) → API Go + storefront estático + editor React, con Stripe por tienda.

## Comandos

```bash
apexx migrate                 # migraciones y roles (ADMIN_DATABASE_URL, APP_DB_PASSWORD, SYSTEM_DB_PASSWORD)
apexx tenant -id demo -name "Demo" -owner-email dueno@tienda.mx -owner-password '...'
apexx seed -tenant demo -n 2000
apexx import -tenant demo -vtex-account micuenta -limit 500   # VTEX_APP_KEY / VTEX_APP_TOKEN, solo lectura
apexx serve                   # API, storefront, editor (/admin/) y procesos de fondo supervisados
apexx qa-setup -tenant qa -password '...' -n 300              # tienda de QA con productos, roles y clientes
deploy/rolling.sh             # despliegue sin corte: reemplaza un nodo a la vez (docker compose)
```

Variables de `serve`: `DATABASE_URL` (rol `apexx_app`), `SYSTEM_DATABASE_URL` (rol `apexx_system`),
`APEXX_MASTER_KEY` (64 hex), `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `APEXX_PLATFORM_FEE_BPS`,
`APEXX_PAYMENTS=stripe|sandbox`, `APEXX_SECURE_COOKIES`, `APEXX_ADDR`, `APEXX_TRUST_PROXY` (detrás de Caddy),
`APEXX_DRAIN` (segundos en que un nodo que se apaga deja de recibir tráfico antes de cerrar).
IA, opcionales: `APEXX_ANTHROPIC_API_KEY` (Claude: contexto de fragmentos, respuestas escritas y textos),
`APEXX_AI_MODEL`, `APEXX_AI_MODEL_FAST`, `APEXX_EMBEDDINGS=local|voyage` y `VOYAGE_API_KEY`.
Sin ellas, toda la IA funciona en modo determinista. Ver `.env.example` y `docs/deploy.md` §7.2.

## Desarrollo

```bash
make test                     # pruebas unitarias
APEXX_TEST_ADMIN_URL=postgres://postgres@127.0.0.1:5432/postgres go test -race ./...   # + integración
cd web && npm ci && npm run build   # editor; se embebe en el binario
docker compose up -d          # pila completa (ver docs/deploy.md)
```

## API v1 (resumen)

Autenticación: `Authorization: Bearer <apx_… API key | aps_… sesión>` o cookie de sesión.
Errores: siempre `application/problem+json` (RFC 9457) con `code` estable.

| Área | Rutas | Permiso mínimo |
| --- | --- | --- |
| Sesión | `POST /v1/auth/login`, `POST /v1/auth/logout`, `GET /v1/me` | — |
| Equipo (puestos ilimitados) | `GET/POST /v1/team/members`, `PATCH /v1/team/members/{id}`, `POST /v1/team/api-keys` | equipo |
| Catálogo | `GET /v1/products`, `GET/PUT /v1/products/{id}` (`If-Match`) | catálogo |
| Inventario | `GET /v1/inventory/{sku}`, `PUT /v1/inventory/{sku}/{ubicación}` | inventario |
| Precios | `PUT /v1/prices/{canal}/{sku}` | precios |
| Canales y promociones | `GET/PUT /v1/channels/{id}`, `PUT /v1/promotions/{id}` | configuración / promociones |
| Carrito | `POST /v1/carts/simulate` | catálogo: lector o llave storefront |
| Compra | `POST /v1/checkout` (`Idempotency-Key`) | llave storefront o pedidos: editor |
| Pedidos | `GET /v1/orders`, `GET /v1/orders/{id}`, `POST …/fulfill`, `POST …/cancel` | pedidos |
| Pagos | `GET/PUT /v1/payments/account`, `POST /v1/stripe/webhook/{tienda}` | pagos: admin |
| Builder | `GET /v1/builder/sections`, `GET/PATCH /v1/sites/{sitio}/templates/{página}/draft`, `…/preview`, `POST /v1/sites/{sitio}/publish`, `…/rollback`, `GET …/releases` | builder |
| Conectores | `GET /v1/events`, `GET/POST /v1/webhooks`, `DELETE /v1/webhooks/{id}`, `GET …/deliveries`, `POST …/replay` | configuración |
| Operación | `GET /healthz`, `GET /readyz`, `GET /metrics`, `GET /v1/system/freshness`, `GET /v1/audit/verify` | — / configuración |
| Storefront | `GET /s/{tienda}/{sitio}/{página}` | público |

## Estructura

```
cmd/apexx            binario único: migrate, tenant, seed, import, serve
internal/store       PostgreSQL, migraciones, RLS, outbox, auditoría
internal/projection  proyector: vistas en memoria por tienda (reemplazo del indexador)
internal/auth        miembros, sesiones, API keys, permisos por módulo
internal/orders      checkout, pagos, reconciliación, cancelación, facturas
internal/payments    Stripe (Connect o llaves propias) y sandbox
internal/webhooks    eventos CloudEvents firmados, reintentos, repetición, feed
internal/builder     plantillas, secciones, JSON Patch, presupuestos, revisiones
internal/importer    importador desde VTEX (solo lectura)
internal/resilience  recuperación, límites por tienda, cortacircuitos, supervisor
internal/crypto      cifrado de sobre por tienda
web/                 editor visual React
deploy/              Google Cloud (VM única y alta disponibilidad)
```
