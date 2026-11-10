# Fase 1 — Fundaciones: datos, aislamiento, identidad y resiliencia

Estado: **completa y verificada** (2026-11-10).

## Qué se construyó

| Pieza | Dónde | Qué resuelve |
| --- | --- | --- |
| PostgreSQL con migraciones embebidas | `internal/store/migrations/*.sql`, `internal/store/db.go` | Fuente única de verdad, transacciones fuertes para dinero y stock |
| Row Level Security en cada tabla de tienda | migraciones 001–005 | Una tienda no puede leer datos de otra aunque haya un error de código |
| Dos roles de base de datos | `store.Migrate` → `apexx_app` (sin BYPASSRLS) y `apexx_system` | La aplicación solo trabaja con RLS; el rol de sistema solo lo usan el proyector, la verificación de llaves y procesos de plataforma |
| Outbox transaccional + `LISTEN/NOTIFY` | tabla `outbox`, `store.Emit` | Ningún cambio confirmado se pierde para la tienda, la búsqueda o los conectores |
| **Proyector (reemplazo del indexador de VTEX/Magento)** | `internal/projection` | Vistas en memoria por tienda, actualizadas por evento y entidad; sin reindexación completa ni caché con TTL |
| Auditoría inmutable encadenada por hash | tabla `audit_log`, `store.Audit`, `GET /v1/audit/verify` | Toda acción queda registrada; un trigger impide `UPDATE`/`DELETE` y la cadena detecta manipulación |
| Cifrado de sobre por tienda | `internal/crypto` | Datos personales y llaves de Stripe cifrados con una llave por tienda; borrar la llave destruye los datos |
| Equipos con puestos ilimitados | `internal/auth`, migración 004 | Permisos por módulo (catálogo, precios, inventario, promociones, pedidos, pagos, builder, equipo, configuración) con niveles sin acceso / lector / editor / admin |
| Sesiones y API keys | `internal/auth` | Contraseñas argon2id, sesiones opacas con hash, llaves por rol, límite de 10 intentos de login por minuto |
| Resiliencia | `internal/resilience` | Recuperación de pánicos por petición, límite de concurrencia por tienda, cortacircuitos por proveedor, supervisor que reinicia procesos de fondo |
| Seguridad en multi-nodo | `webhooks.Dispatch`, `Send`, `orders.IssueInvoices` | Candado de líder y reservas con `FOR UPDATE SKIP LOCKED`: varias VMs nunca procesan el mismo trabajo |

## Evidencia (medida en esta sesión)

| Prueba | Resultado |
| --- | --- |
| Tienda B leyendo producto de tienda A | 404; listado vacío |
| Rol de la aplicación sin tienda asignada | ve 0 filas; `api_keys` y `sessions` → permiso denegado |
| Cambio de precio → visible en simulación de carrito | **p50 2,9 ms, p95 3,3 ms** incluyendo la escritura HTTP (VTEX: hasta 5 min de caché) |
| Desfase del proyector por evento | promedio 1,6 ms, máximo 4,6 ms |
| Carga inicial de 2 tiendas y 500 productos | 251 ms |
| `kill -9` del proceso | vuelve listo en **0,3 s** sin perder pedidos ni páginas publicadas |
| Base de datos caída | `/readyz` 503, `/healthz` 200, la tienda sigue sirviendo páginas desde memoria |
| Base de datos de vuelta | listo en 0,02 s; el proyector se pone al día solo |
| Dos nodos contra la misma base, 50 cambios | 50 eventos entregados, **0 duplicados**, ambos nodos con el mismo precio |
| Intento de alterar la auditoría | bloqueado por trigger; cadena verificada intacta |

Las pruebas automáticas equivalentes están en `internal/api/integration_test.go` y corren en CI contra PostgreSQL real.

## Pendiente para fases siguientes

- Passkeys y SSO (OIDC/SAML) para administradores; MFA obligatorio.
- Cuotas de CPU y memoria por tienda (hoy: límite de peticiones simultáneas).
- Rotación automática de la llave maestra con Cloud KMS.
