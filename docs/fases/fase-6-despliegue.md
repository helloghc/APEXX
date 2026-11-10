# Fase 6 — Despliegue en Google Cloud y autorecuperación

Fecha: 2026-11-10. Guía completa: [`docs/deploy.md`](../deploy.md).

## Entregables

| Archivo | Qué hace |
| --- | --- |
| `Dockerfile` | Compila el binario estático; imagen distroless sin root, de 28 MB, con chequeo de salud en `/readyz`. Usa el espejo `mirror.gcr.io`, sin límites de Docker Hub |
| `docker-compose.yml` | Postgres 16, `migrate` de una sola vez, `apexx` endurecido, Caddy (HTTPS automático) y autoheal |
| `deploy/caddy/Caddyfile` | TLS con Let's Encrypt (`acme`) o CA interna (`internal`), HSTS, compresión y `/metrics` bloqueado |
| `deploy/gcp/00…04-*.sh` | Proyecto, secretos, imagen, VM y monitoreo; idempotentes y con `set -euo pipefail` |
| `deploy/gcp/startup.sh`, `fetch-secrets.sh` | Arranque de la VM: Docker, Ops Agent, secretos a `.env` (0600), servicio y temporizador de systemd |
| `deploy/backup/pg-backup.sh`, `pg-restore.sh` | Respaldo directo a GCS y restauración en una sola transacción |
| `deploy/gcp/ha/` | Cloud SQL regional, grupo administrado con autohealing y balanceador HTTPS |
| `deploy/gcp/ops-agent.yaml` | Logs de contenedores y métricas Prometheus hacia Cloud Logging y Monitoring |
| `deploy/local/` | Override local (TLS interno, puertos altos, sandbox) e imagen desde un binario precompilado |

## Verificado localmente (Docker 29, Compose 5.6)

| Prueba | Resultado medido |
| --- | --- |
| `docker compose config` (producción y HA) | Válido |
| Arranque en frío hasta que `apexx` está sano | 13 s (Postgres sano, migraciones aplicadas, proyecciones cargadas) |
| `/readyz` a través de Caddy con TLS | 200 por HTTP/2; HTTP → HTTPS con 308; HSTS y `nosniff` presentes; `/metrics` → 404 hacia afuera |
| Crear una tienda con `docker compose run apexx tenant …` | Correcto; las llaves solo se imprimen una vez |
| Cargar 200 productos con `seed` | 1,4 s |
| Publicar la home y ver la tienda | 200; 7 ms hasta el primer byte; 1,7 KB |
| **El proceso `apexx` se cae** (`kill -9` del PID) | La tienda responde 200 de nuevo en **1,1 s**; el contenedor queda sano en 6,0 s |
| **Postgres se cae** (`kill -9`) | La tienda **siguió respondiendo 200** desde memoria; `/readyz` respondió 503 mientras tanto; recuperación total en **5,8 s**; las escrituras funcionan después |
| **`apexx` se congela** (`SIGSTOP`) | autoheal lo detecta y lo reinicia: la tienda vuelve en **33 s** (era 48 s antes de bajar la espera de parada de 20 s a 5 s) |
| Respaldo y restauración (`pg_dump -Fc` → `pg_restore --single-transaction`) | 0,3 s y 0,3 s; productos, auditoría (1 007 registros), 26 políticas RLS y revisiones del sitio idénticos |

## No verificado aquí

- **Recursos reales de Google Cloud** (VM, Secret Manager, Cloud SQL, grupo administrado, balanceador, alertas): los scripts se revisaron con `bash -n`, pero no se ejecutaron porque este entorno no tiene un proyecto de GCP. El primer despliegue real debe seguir `docs/deploy.md` paso a paso.
- **`docker build` del Dockerfile de producción:** falló aquí solo porque el proxy de este entorno intercepta TLS y el contenedor no confía en su CA. La imagen se verificó con `deploy/local/Dockerfile.prebuilt`, que tiene el mismo runtime. En Cloud Build no hay ese proxy.
- **Certificado de Let's Encrypt real:** requiere un dominio público. Se probó el mismo Caddyfile con `tls internal`.

## Pendiente en el backend para operar con varias VMs (HA)

1. **Caché del builder:** las demás instancias deben invalidar el sitio en memoria con el evento `site.released`.
2. **Límite de intentos de login:** hay que llevarlo a un almacenamiento compartido.
3. **Procesos de fondo:** necesitan un candado de líder (`pg_try_advisory_lock`) para que solo uno trabaje a la vez.
4. **Rotación de la llave maestra:** falta una herramienta que vuelva a cifrar `tenant_keys`.
