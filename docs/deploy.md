# Despliegue de Apexx en Google Cloud

Dos formas de operar Apexx, con los mismos archivos:

| | **VM única** | **Alta disponibilidad (HA)** |
| --- | --- | --- |
| Para | Lanzamiento, piloto, tiendas medianas | Producción con muchas tiendas y SLA |
| Cómputo | 1 VM `e2-standard-4` con Docker Compose | Grupo administrado regional (2 a 6 VMs en zonas distintas) |
| Base de datos | PostgreSQL 16 en la misma VM | Cloud SQL PostgreSQL 16 regional (réplica síncrona, IP privada, PITR) |
| TLS y entrada | Caddy con Let's Encrypt | Balanceador HTTPS global con certificado de Google |
| Si el proceso se cae | Docker lo reinicia (1,1 s medido) | Igual, y el balanceador lo saca del tráfico |
| Si el proceso se cuelga | autoheal lo reinicia (33 s medido) | El grupo recrea la VM; el resto sigue atendiendo |
| Si la VM o la zona cae | Se recrea a mano o desde snapshot | Automático: otra zona sigue sirviendo |
| Costo aproximado | 150 a 200 USD/mes | 900 a 1 500 USD/mes (2 VMs + Cloud SQL HA + balanceador) |

Las cifras de costo son un orden de magnitud para `us-central1` con precios públicos. Confírmalas en la calculadora de Google Cloud antes de decidir.

## 1. Requisitos

- Un proyecto de Google Cloud con facturación activa y `gcloud` autenticado (`gcloud auth login`).
- Un dominio con acceso a su DNS.
- Este repositorio clonado en tu máquina.

```bash
export PROJECT_ID=mi-proyecto
export DOMAIN=tienda.midominio.mx
export ACME_EMAIL=ops@midominio.mx
export REGION=us-central1 ZONE=us-central1-a      # o northamerica-south1 (México) si está disponible para tu cuenta
```

## 2. VM única, paso a paso

```bash
deploy/gcp/00-bootstrap.sh     # APIs, Artifact Registry, bucket de respaldos (30 días), cuenta de servicio mínima
deploy/gcp/01-secrets.sh       # Secret Manager: contraseñas de BD y llave maestra aleatorias
deploy/gcp/02-build-image.sh   # Cloud Build → Artifact Registry
deploy/gcp/03-create-vm.sh     # IP estática, firewall 80/443, Shielded VM, OS Login, snapshots diarios
```

1. Crea el registro DNS `A` de `$DOMAIN` hacia la IP que imprime el último script.
2. Espera unos 3 minutos. La VM instala Docker y el Ops Agent, baja los secretos, levanta el stack y Caddy obtiene el certificado.
3. Verifica: `curl https://$DOMAIN/readyz` debe responder `ready`.
4. Configura alertas: `NOTIFY_EMAIL=ops@midominio.mx deploy/gcp/04-monitoring.sh`.

**Qué queda corriendo en la VM:**
- **`apexx.service` (systemd):** al encender la VM baja los secretos, actualiza las imágenes y levanta el stack.
- **`apexx-backup.timer`:** respaldo diario a las 03:15 UTC hacia el bucket, sin escribir en el disco local.
- **Contenedores:** `postgres`, `migrate` (corre una vez en cada arranque), `apexx`, `caddy` y `autoheal`.

**Seguridad de la VM:**
- SSH solo por IAP, nunca desde internet.
- La cuenta de servicio solo puede:
  - leer los secretos de Apexx;
  - escribir en el bucket de respaldos;
  - escribir logs y métricas;
  - leer imágenes.
- `apexx` corre sin root y con el sistema de archivos en solo lectura, sin capacidades de Linux (`cap_drop ALL`) y con `no-new-privileges`.
- La base de datos está en una red interna de Docker sin salida.

### Primera tienda

```bash
gcloud compute ssh apexx --zone $ZONE --tunnel-through-iap
cd /opt/apexx
sudo docker compose run --rm apexx tenant -id mitienda -name "Mi Tienda" \
  -owner-email dueno@mitienda.mx -owner-password 'una-contraseña-de-12-o-más'
```

El comando imprime la llave pública de storefront y una llave admin. Guarda la admin en un gestor de contraseñas, porque no se vuelve a mostrar. Después:

1. El dueño inicia sesión en `https://$DOMAIN/admin/`, invita a su equipo (puestos ilimitados) y publica su sitio.
2. La tienda queda en `https://$DOMAIN/s/mitienda/main/index`.

## 3. Stripe por tienda

Cada tienda usa **su propia cuenta de Stripe**. Hay dos modos (`PUT /v1/payments/account`, permiso pagos: admin):

| Modo | Cuándo | Qué se guarda |
| --- | --- | --- |
| `connect` | Recomendado. El comerciante conecta su cuenta con Stripe Connect | Solo el `acct_…`. Requiere `STRIPE_SECRET_KEY` de la plataforma en Secret Manager |
| `own_keys` | El comerciante pega su llave `sk_…` o, mejor, una restringida `rk_…` | La llave, cifrada con la llave de su tienda |

**Webhook:**
- En el panel de Stripe de la tienda crea un endpoint hacia `https://$DOMAIN/v1/webhooks/stripe/{tienda}` con los eventos `payment_intent.succeeded` y `payment_intent.payment_failed`.
- Guarda el secreto de firma (`whsec_…`) en `webhookSecret` de la misma llamada.
- En modo `connect` se usa `STRIPE_WEBHOOK_SECRET` de la plataforma.

**Comisión de la plataforma:** `APEXX_PLATFORM_FEE_BPS` en puntos base (100 = 1%). Solo aplica en modo `connect`.

**Factura legal en México:** Stripe Invoicing emite la factura comercial, pero no un CFDI. Para el CFDI hace falta un PAC certificado (por ejemplo Facturapi); ver `docs/audit/conectores.md`.

## 4. Variables de entorno

Se generan en `/opt/apexx/.env` (permisos 600) desde Secret Manager y metadatos. Nunca se guardan en git.

| Variable | Origen | Uso |
| --- | --- | --- |
| `POSTGRES_PASSWORD` | Secret Manager | Superusuario de PostgreSQL; solo lo usa `migrate` |
| `APP_DB_PASSWORD` | Secret Manager | Rol `apexx_app`, sujeto a RLS por tienda |
| `SYSTEM_DB_PASSWORD` | Secret Manager | Rol `apexx_system` (proyector, autenticación) |
| `APEXX_MASTER_KEY` | Secret Manager | 64 hex; cifra la llave de cada tienda. **Respáldala fuera de línea** |
| `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` | Secret Manager | Plataforma, solo para tiendas `connect` |
| `APEXX_PAYMENTS` | Metadato | `stripe` en producción; `sandbox` nunca cobra |
| `APEXX_PLATFORM_FEE_BPS` | Metadato | Comisión de la plataforma |
| `APEXX_DOMAIN`, `ACME_EMAIL` | Metadato | Dominio y correo para Let's Encrypt |
| `APEXX_IMAGE` | Metadato | Imagen en Artifact Registry |
| `CLOUDSQL_HOST` | Metadato (HA) | IP privada de Cloud SQL |
| `PG_SHARED_BUFFERS`, `PG_EFFECTIVE_CACHE_SIZE`, `PG_MEMORY_LIMIT`, `APEXX_CPUS`, `APEXX_MEMORY_LIMIT` | `.env` | Tamaño; valores por defecto para 4 vCPU y 16 GB |

## 5. Respaldos y restauración

- **Diario:** `pg_dump` en formato custom se envía directo al bucket (`gs://…/pg/apexx-AAAAMMDDTHHMMSSZ.dump`). El bucket tiene versionado y borra los respaldos de más de 30 días.
- **Snapshots del disco:** diarios, se conservan 14 días.
- **Respaldo manual:** `sudo BACKUP_BUCKET=… /opt/apexx/deploy/backup/pg-backup.sh`.
- **Restaurar:** `sudo BACKUP_BUCKET=… /opt/apexx/deploy/backup/pg-restore.sh latest` o la ruta de un archivo. Detiene `apexx`, restaura en una sola transacción, corre migraciones y vuelve a levantar.
- **En HA:** Cloud SQL hace respaldos automáticos (14 retenidos) y permite recuperar a un momento exacto (PITR).

## 6. Actualizaciones

**VM única** (corte de unos segundos; Caddy reintenta durante 10 s, así que casi nadie lo nota):
```bash
IMAGE_TAG=v1.2.0 deploy/gcp/02-build-image.sh
IMAGE_TAG=v1.2.0 deploy/gcp/03-create-vm.sh      # actualiza metadatos y reinicia el stack
```
Las migraciones corren solas antes de que arranque la nueva versión. Escríbelas compatibles hacia atrás: primero agregar columnas, después usarlas y al final quitar lo viejo.

**HA (sin corte):** `IMAGE_TAG=v1.2.0 deploy/gcp/ha/11-mig-lb.sh` crea una plantilla nueva y reemplaza las VMs de una en una (`max-surge 1`, `max-unavailable 0`). El balanceador drena las conexiones de cada VM durante 25 s.

**Volver a la versión anterior:** repite el despliegue con el `IMAGE_TAG` anterior.

## 7. Alta disponibilidad

```bash
deploy/gcp/ha/10-cloudsql.sh                 # imprime CLOUDSQL_HOST
export CLOUDSQL_HOST=10.x.x.x
deploy/gcp/ha/11-mig-lb.sh                   # plantilla, grupo regional con autohealing y balanceador HTTPS
```

- **Autohealing:** si `/readyz` falla 3 veces seguidas (cada 5 s), el grupo recrea la VM. Hay 180 s de gracia al arrancar.
- **Autoescalado:** de 2 a 6 VMs cuando el CPU pasa del 60%.
- **Cloud SQL regional:** si la zona primaria cae, el failover es automático (unos 60 s).

**Antes de pasar a varias VMs, el backend debe resolver tres cosas** (pendientes en el código Go):
1. **Caché del builder:** cada instancia guarda en memoria el sitio publicado. Al publicar o revertir, las demás instancias deben invalidarlo con el evento `site.released` del outbox; hoy solo lo hace la instancia que publicó.
2. **Límite de intentos de login:** vive en memoria, así que cuenta por instancia. Para un límite global hay que moverlo a la base de datos o a Valkey.
3. **Procesos de fondo:** corren en todas las instancias. Hoy son seguros porque usan `FOR UPDATE` y claves de idempotencia en Stripe, pero conviene un candado (`pg_try_advisory_lock`) para que solo uno trabaje a la vez.

## 8. Runbook de incidentes

| Síntoma | Qué pasa | Qué hacer |
| --- | --- | --- |
| `/readyz` responde 503 | La BD no responde o el proyector está cargando | Las páginas publicadas siguen sirviéndose desde memoria. `docker compose ps` y `docker compose logs postgres`; Postgres se reinicia solo (5,7 s medido). En HA revisa el estado de Cloud SQL |
| Disco lleno | Postgres deja de escribir | `df -h`, `docker system prune -f` (no borra volúmenes). Si es `pgdata`, agranda el disco: `gcloud compute disks resize` y luego `resize2fs` |
| Certificado vencido o no emitido | Caddy no pudo validar el dominio | Revisa el DNS `A` y que el puerto 80 esté abierto; `docker compose logs caddy`. Caddy renueva solo 30 días antes |
| Contenedor reiniciándose en bucle | Error de configuración o de BD | `docker compose logs --tail 200 apexx`; revisa `.env` con `sudo cat` (permisos 600) |
| Cobros "pendientes" | Stripe no respondió a tiempo | El reconciliador los resuelve cada minuto consultando Stripe. El webhook también los cierra |
| Rotar la llave maestra | Sospecha de compromiso | Crea una versión nueva en Secret Manager **sin borrar la anterior**, re-cifra las llaves de las tiendas (`tenant_keys`) con la nueva y reinicia. La herramienta de re-cifrado está pendiente en Go; mientras tanto, no borres la versión anterior |
| Revocar una API key | Llave filtrada | Por API: el dueño la revoca. Inmediato: `UPDATE api_keys SET revoked_at=now() WHERE id='k…'` como `postgres`. Las sesiones de un miembro se cortan al desactivarlo |
| Restaurar datos | Borrado accidental | Ver sección 5; en HA, clona Cloud SQL a un momento exacto (PITR) y compara antes de reemplazar |

## 9. SLO y alertas

| SLO | Meta | Medición |
| --- | --- | --- |
| Disponibilidad del storefront | 99,95% | Uptime check en `/readyz` cada minuto |
| Disponibilidad del checkout | 99,99% (HA) | Tasa de 5xx en `/v1/checkout` |
| Frescura de datos | Menos de 2 s | `apexx_projection_lag_ms` |
| Latencia p99 de la API | Menos de 100 ms | `apexx_http_request_seconds` |

`deploy/gcp/04-monitoring.sh` crea tres alertas por correo:
1. `/readyz` falla durante 2 minutos.
2. El desfase del proyector pasa de 2 s.
3. Hay más de 0,5 errores 5xx por segundo durante 5 minutos.

`/metrics` solo se expone en el loopback de la VM para el Ops Agent; Caddy lo bloquea hacia afuera.
