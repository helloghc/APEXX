# Apexx

Plataforma de comercio electrónico multi-tenant con builder visual, escrita en Go.

- Stack y plan a producción: [`docs/plan.md`](docs/plan.md)
- Auditorías de VTEX que guían el diseño: [`docs/audit/`](docs/audit/)

## Ejecutar

```bash
make run            # API en :8080 con 18.500 productos de demo
make test           # pruebas con detector de condiciones de carrera
make bench          # benchmarks
make docker         # imagen distroless sin root
```

## Probar el builder

```bash
# Página publicada
curl localhost:8080/s/demo/index

# Mover la sección 4 a la posición 2 (un solo parche JSON)
curl -X PATCH localhost:8080/v1/sites/demo/templates/index/draft \
  -d '{"ops":[{"op":"move","from":"/regions/main/3","path":"/regions/main/1"}]}'

# Publicar y deshacer
curl -X POST localhost:8080/v1/sites/demo/publish
curl -X POST localhost:8080/v1/sites/demo/rollback
```

## API v1

| Método | Ruta | Descripción |
| --- | --- | --- |
| GET | `/v1/products?limit=&cursor=&fields=` | Catálogo con paginación por cursor y stock en vivo |
| GET | `/v1/products/{id}` | Producto |
| GET | `/v1/inventory/{sku}` | Stock por ubicación |
| GET | `/v1/channels/{id}` | Reglas visibles del canal (moneda, impuestos, máximos, envío) |
| POST | `/v1/carts/simulate` | Precios, promociones explicadas, envío, impuestos y total final |
| GET | `/v1/builder/sections` | Catálogo de secciones con esquema y presupuesto |
| GET/PATCH | `/v1/sites/{site}/templates/{handle}/draft` | Borrador y edición con JSON Patch |
| GET | `/v1/sites/{site}/templates/{handle}/preview` | Vista previa |
| POST | `/v1/sites/{site}/publish` · `/rollback` | Publicación atómica y deshacer |
| GET | `/s/{site}/{handle}` | Storefront publicado (HTML estático) |

Todos los errores usan `application/problem+json` (RFC 9457) con un `code` estable.

## Estructura

```
cmd/apexx          binario único (monolito modular)
internal/money     montos en unidades menores + moneda
internal/catalog   productos y variantes
internal/inventory stock con reservas atómicas y eventos
internal/pricing   precios por canal y por cantidad
internal/promo     promociones que explican por qué aplican o no
internal/cart      simulación de carrito
internal/builder   plantillas, secciones, JSON Patch, presupuestos, revisiones
internal/api       HTTP
```
