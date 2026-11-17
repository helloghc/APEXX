#!/usr/bin/env bash
# GENERADO por deploy/gcp/build-browser-startup.sh: no lo edites a mano.
# Script de arranque para una VM creada desde la consola web de Google Cloud.
# Pégalo completo en "Automatización > Script de inicio" al crear la VM.
# VM startup script (runs as root on every boot; idempotent).
# Installs Docker + Ops Agent, writes the stack files from instance metadata,
# installs systemd units for the stack and nightly backups, and starts it.
set -euo pipefail
exec > >(logger -t apexx-startup) 2>&1
DIR=/opt/apexx
md() { curl -fsS -H 'Metadata-Flavor: Google' "http://metadata.google.internal/computeMetadata/v1/instance/attributes/$1"; }

if ! command -v docker >/dev/null; then
  apt-get update -y
  apt-get install -y ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
  cat > /etc/docker/daemon.json <<JSON
{"log-driver":"json-file","log-opts":{"max-size":"20m","max-file":"5"},"live-restore":true}
JSON
  systemctl restart docker
fi
if ! systemctl is-active --quiet google-cloud-ops-agent; then
  curl -fsSL https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh -o /tmp/add-ops-agent.sh
  bash /tmp/add-ops-agent.sh --also-install
fi

mkdir -p "$DIR/deploy/caddy" "$DIR/deploy/gcp" "$DIR/deploy/backup"
cat > "$DIR/docker-compose.yml" <<'__APEXX_FIN_DEL_ARCHIVO__'
# Apexx — single-VM production stack.
#   postgres  : PostgreSQL 16 with a persistent volume
#   migrate   : one-shot schema migrations + DB roles (runs on every `up`, idempotent)
#   apexx     : API, storefront, editor and workers (restart: always + healthcheck)
#   apexx2    : second node of the same image; deploy/rolling.sh replaces one
#               node at a time so the store never goes down on a deploy
#   caddy     : automatic HTTPS (Let's Encrypt) and security headers
#   autoheal  : restarts any container whose healthcheck turns unhealthy
#
# Secrets live in .env (0600), written at boot from Google Secret Manager by
# deploy/gcp/fetch-secrets.sh. Never commit .env. See docs/deploy.md.

name: apexx

x-logging: &logging
  driver: json-file
  options:
    max-size: "20m"
    max-file: "5"

x-hardening: &hardening
  security_opt:
    - no-new-privileges:true
  cap_drop:
    - ALL

services:
  postgres:
    image: mirror.gcr.io/library/postgres:16.6-bookworm
    restart: always
    environment:
      POSTGRES_DB: apexx
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_INITDB_ARGS: "--data-checksums"
    command:
      - postgres
      - -c
      - shared_buffers=${PG_SHARED_BUFFERS:-2GB}
      - -c
      - effective_cache_size=${PG_EFFECTIVE_CACHE_SIZE:-6GB}
      - -c
      - work_mem=16MB
      - -c
      - maintenance_work_mem=256MB
      - -c
      - max_connections=200
      - -c
      - wal_compression=on
      - -c
      - checkpoint_timeout=15min
      - -c
      - max_wal_size=4GB
      - -c
      - random_page_cost=1.1
      - -c
      - log_min_duration_statement=500
      - -c
      - idle_in_transaction_session_timeout=60s
    volumes:
      - pgdata:/var/lib/postgresql/data
    shm_size: 512m
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d apexx"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s
    labels:
      autoheal: "true"
    logging: *logging
    networks: [backend]
    deploy:
      resources:
        limits:
          memory: ${PG_MEMORY_LIMIT:-8g}

  migrate:
    image: ${APEXX_IMAGE:-apexx:latest}
    command: ["migrate"]
    restart: "no"
    environment:
      ADMIN_DATABASE_URL: postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/apexx?sslmode=disable
      APP_DB_PASSWORD: ${APP_DB_PASSWORD:?set APP_DB_PASSWORD in .env}
      SYSTEM_DB_PASSWORD: ${SYSTEM_DB_PASSWORD:?set SYSTEM_DB_PASSWORD in .env}
    depends_on:
      postgres:
        condition: service_healthy
    read_only: true
    <<: *hardening
    logging: *logging
    networks: [backend]

  apexx: &apexx-node
    image: ${APEXX_IMAGE:-apexx:latest}
    command: ["serve"]
    restart: always
    environment: &apexx-env
      APEXX_ADDR: ":8080"
      DATABASE_URL: postgres://apexx_app:${APP_DB_PASSWORD}@postgres:5432/apexx?sslmode=disable&pool_max_conns=60
      SYSTEM_DATABASE_URL: postgres://apexx_system:${SYSTEM_DB_PASSWORD}@postgres:5432/apexx?sslmode=disable&pool_max_conns=20
      APEXX_MASTER_KEY: ${APEXX_MASTER_KEY:?set APEXX_MASTER_KEY in .env}
      APEXX_PAYMENTS: ${APEXX_PAYMENTS:-stripe}
      STRIPE_SECRET_KEY: ${STRIPE_SECRET_KEY:-}
      STRIPE_WEBHOOK_SECRET: ${STRIPE_WEBHOOK_SECRET:-}
      APEXX_PLATFORM_FEE_BPS: ${APEXX_PLATFORM_FEE_BPS:-0}
      APEXX_SECURE_COOKIES: "true"
      APEXX_TRUST_PROXY: "true"
      APEXX_DRAIN: ${APEXX_DRAIN:-6s}
      APEXX_SANDBOX_HOST: ${APEXX_SANDBOX_HOST:-}
      # Optional AI providers. Without them every AI feature runs in its
      # deterministic mode (local vectors, quoted answers).
      APEXX_ANTHROPIC_API_KEY: ${APEXX_ANTHROPIC_API_KEY:-}
      APEXX_EMBEDDINGS: ${APEXX_EMBEDDINGS:-local}
      VOYAGE_API_KEY: ${VOYAGE_API_KEY:-}
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
    healthcheck:
      test: ["CMD", "/busybox", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/readyz"]
      interval: 5s
      timeout: 3s
      retries: 3
      start_period: 30s
    labels:
      autoheal: "true"
    read_only: true
    tmpfs:
      - /tmp:size=64m
    <<: *hardening
    # Loopback only: lets the Ops Agent scrape /metrics; not reachable from outside the VM.
    ports:
      - "127.0.0.1:9100:8080"
    # On SIGTERM a node answers /readyz with 503 for APEXX_DRAIN (Caddy stops
    # sending it traffic), finishes what is in flight and hands its job
    # leases over; the grace period covers drain + shutdown, and up to 2
    # minutes for a sandbox copy or scheduled publish running on the node.
    stop_grace_period: 150s
    logging: *logging
    networks: [backend, frontend]
    deploy:
      resources:
        limits:
          cpus: "${APEXX_CPUS:-3}"
          memory: ${APEXX_MEMORY_LIMIT:-3g} # per node; two nodes run

  apexx2:
    <<: *apexx-node
    ports:
      - "127.0.0.1:9101:8080"

  caddy:
    image: mirror.gcr.io/library/caddy:2.8.4-alpine
    restart: always
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
    environment:
      APEXX_DOMAIN: ${APEXX_DOMAIN:?set APEXX_DOMAIN in .env}
      ACME_EMAIL: ${ACME_EMAIL:-}
      CADDY_TLS_MODE: ${CADDY_TLS_MODE:-acme}
    volumes:
      - ./deploy/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    depends_on:
      - apexx
      - apexx2
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:2019/config/"]
      interval: 15s
      timeout: 3s
      retries: 3
    labels:
      autoheal: "true"
    logging: *logging
    networks: [frontend]

  # Restarts containers labelled autoheal=true when their healthcheck fails
  # (restart: always only covers crashes, not hangs).
  autoheal:
    image: mirror.gcr.io/willfarrell/autoheal:1.2.0
    restart: always
    environment:
      AUTOHEAL_CONTAINER_LABEL: autoheal
      AUTOHEAL_INTERVAL: "5"
      AUTOHEAL_START_PERIOD: "0"
      AUTOHEAL_DEFAULT_STOP_TIMEOUT: "5"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    logging: *logging
    network_mode: none

volumes:
  pgdata:
  caddy_data:
  caddy_config:

networks:
  backend:
    internal: true
  frontend:
__APEXX_FIN_DEL_ARCHIVO__
cat > "$DIR/deploy/caddy/Caddyfile" <<'__APEXX_FIN_DEL_ARCHIVO__'
# Apexx edge proxy. CADDY_TLS_MODE=acme (Let's Encrypt, production) or internal (local CA, tests).
{
	admin 127.0.0.1:2019
	servers {
		protocols h1 h2 h3
	}
	# Certificates on demand for the stores' own domains and the sandboxes'
	# temporary addresses: Caddy asks Apexx first, and Apexx only says yes to
	# a verified domain of some store (internal/api/settings_public.go).
	on_demand_tls {
		ask http://apexx:8080/internal/tls/ask
	}
}

(apexx_upstream) {
	encode zstd gzip

	header {
		Strict-Transport-Security "max-age=63072000; includeSubDomains; preload"
		X-Content-Type-Options "nosniff"
		Referrer-Policy "strict-origin-when-cross-origin"
		Permissions-Policy "camera=(), microphone=(), geolocation=(), payment=(self \"https://js.stripe.com\")"
		-Server
	}

	# Health and internal endpoints never answer from outside.
	@internal path /metrics /internal/*
	respond @internal 404

	# Two nodes. A node leaving (deploy, restart) answers /readyz with 503
	# for a few seconds before it stops, so it is taken out of rotation
	# before any request can fail; a request that still meets a closed
	# node is retried on the other one.
	reverse_proxy apexx:8080 apexx2:8080 {
		header_up X-Forwarded-For {remote_host}
		lb_policy least_conn
		health_uri /readyz
		health_interval 2s
		health_timeout 2s
		health_fails 1
		health_passes 2
		fail_duration 10s
		lb_try_duration 10s
		lb_try_interval 100ms
	}

	log {
		output stdout
		format json
	}
}

(tls_acme) {
	tls {$ACME_EMAIL}
}

(tls_internal) {
	tls internal
}

{$APEXX_DOMAIN} {
	import tls_{$CADDY_TLS_MODE}
	import apexx_upstream
}

# Every other host: a store's own domain (tienda.com, pruebas.tienda.com) or a
# sandbox's temporary address ({id}.<APEXX_SANDBOX_HOST>). The certificate is
# issued the first time the host is visited, only if Apexx knows it.
https:// {
	tls {
		on_demand
	}
	import apexx_upstream
}
__APEXX_FIN_DEL_ARCHIVO__
cat > "$DIR/deploy/gcp/fetch-secrets.sh" <<'__APEXX_FIN_DEL_ARCHIVO__'
#!/usr/bin/env bash
# Runs ON THE VM at boot (systemd ExecStartPre). Reads every secret from
# Secret Manager with the VM service account and writes /opt/apexx/.env
# (0600, root). Public settings come from instance metadata.
set -euo pipefail
DIR=/opt/apexx
md() { curl -fsS -H 'Metadata-Flavor: Google' "http://metadata.google.internal/computeMetadata/v1/instance/attributes/$1" 2>/dev/null || true; }
umask 077
tmp="$(mktemp "$DIR/.env.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
{
  echo "APEXX_DOMAIN=$(md apexx-domain)"
  echo "ACME_EMAIL=$(md apexx-acme-email)"
  echo "APEXX_IMAGE=$(md apexx-image)"
  echo "CADDY_TLS_MODE=acme"
  p="$(md apexx-payments)"; echo "APEXX_PAYMENTS=${p:-stripe}"
  f="$(md apexx-fee-bps)"; echo "APEXX_PLATFORM_FEE_BPS=${f:-0}"
  echo "CLOUDSQL_HOST=$(md apexx-cloudsql-host)"   # only set in the HA setup
  echo "APEXX_SANDBOX_HOST=$(md apexx-sandbox-host)"   # optional: wildcard host for sandboxes
  e="$(md apexx-embeddings)"; [ -n "$e" ] && echo "APEXX_EMBEDDINGS=$e"
  # Optional secrets (AI) are simply empty when they do not exist.
  for s in POSTGRES_PASSWORD APP_DB_PASSWORD SYSTEM_DB_PASSWORD APEXX_MASTER_KEY STRIPE_SECRET_KEY STRIPE_WEBHOOK_SECRET APEXX_ANTHROPIC_API_KEY VOYAGE_API_KEY; do
    v="$(gcloud secrets versions access latest --secret "$s" 2>/dev/null || true)"
    [ "$v" = "unset" ] && v=""
    # Set up from the browser: the secret was created with the value "generar".
    # The VM draws the real value at random and stores it as a new version, so
    # nobody ever sees or types it. If storing fails, nothing starts: a value
    # that is not in Secret Manager would be lost at the next boot.
    if [ "$v" = "generar" ]; then
      case "$s" in
        APEXX_MASTER_KEY) v="$(openssl rand -hex 32)" ;;
        POSTGRES_PASSWORD|APP_DB_PASSWORD|SYSTEM_DB_PASSWORD) v="$(openssl rand -base64 48 | tr -d '/+=\n' | cut -c1-40)" ;;
        *) echo "fetch-secrets: $s solo admite un valor real, no 'generar'" >&2; exit 1 ;;
      esac
      printf '%s' "$v" | gcloud secrets versions add "$s" --data-file=- >/dev/null ||
        { echo "fetch-secrets: no se pudo guardar $s en Secret Manager; la cuenta de la VM necesita el rol 'Secret Manager Secret Version Adder'" >&2; exit 1; }
    fi
    printf '%s=%s\n' "$s" "$v"
  done
} > "$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$DIR/.env"
__APEXX_FIN_DEL_ARCHIVO__
cat > "$DIR/deploy/backup/pg-backup.sh" <<'__APEXX_FIN_DEL_ARCHIVO__'
#!/usr/bin/env bash
# Streams a compressed custom-format pg_dump of the apexx database to GCS.
# Nothing is written to the local disk. Retention is enforced by the bucket
# lifecycle rule (deploy/gcp/00-bootstrap.sh).
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${BACKUP_BUCKET:?set BACKUP_BUCKET}"
ts="$(date -u +%Y%m%dT%H%M%SZ)"
obj="gs://${BACKUP_BUCKET}/pg/apexx-${ts}.dump"
docker compose exec -T postgres pg_dump -U postgres -d apexx -Fc -Z 6 | gcloud storage cp - "$obj"
size="$(gcloud storage ls -l "$obj" | awk 'NR==1{print $1}')"
[ "${size:-0}" -gt 1024 ] || { echo "backup too small: $obj ($size bytes)" >&2; exit 1; }
echo "backup ok: $obj ($size bytes)"
__APEXX_FIN_DEL_ARCHIVO__
cat > "$DIR/deploy/backup/pg-restore.sh" <<'__APEXX_FIN_DEL_ARCHIVO__'
#!/usr/bin/env bash
# Restores a backup into the running postgres container.
#   pg-restore.sh gs://BUCKET/pg/apexx-YYYYmmddTHHMMSSZ.dump   (or "latest")
# Stops the app first so no writes happen during the restore, then restarts it.
set -euo pipefail
cd "$(dirname "$0")/../.."
src="${1:?usage: pg-restore.sh <gs://.../file.dump | latest>}"
if [ "$src" = latest ]; then
  : "${BACKUP_BUCKET:?set BACKUP_BUCKET}"
  src="$(gcloud storage ls "gs://${BACKUP_BUCKET}/pg/" | sort | tail -1)"
fi
echo "Restaurando $src — la tienda quedará fuera de línea unos minutos."
read -r -p "Escribe RESTAURAR para continuar: " ok; [ "$ok" = RESTAURAR ] || exit 1
docker compose stop apexx
gcloud storage cp "$src" - | docker compose exec -T postgres pg_restore -U postgres -d apexx --clean --if-exists --no-owner --single-transaction
docker compose run --rm migrate
docker compose up -d apexx
echo "Restauración completa."
__APEXX_FIN_DEL_ARCHIVO__
cat > /etc/google-cloud-ops-agent/config.yaml <<'__APEXX_FIN_DEL_ARCHIVO__'
# Google Cloud Ops Agent: ships container logs and Apexx Prometheus metrics.
logging:
  receivers:
    docker_containers:
      type: files
      include_paths: [/var/lib/docker/containers/*/*.log]
      record_log_file_path: true
  processors:
    docker_json:
      type: parse_json
      field: message
  service:
    pipelines:
      apexx:
        receivers: [docker_containers]
        processors: [docker_json]
metrics:
  receivers:
    apexx_prometheus:
      type: prometheus
      config:
        scrape_configs:
          - job_name: apexx
            scrape_interval: 30s
            metrics_path: /metrics
            static_configs:
              - targets: ["127.0.0.1:9100"]
  service:
    pipelines:
      apexx:
        receivers: [apexx_prometheus]
__APEXX_FIN_DEL_ARCHIVO__
chmod 700 "$DIR/deploy/gcp/fetch-secrets.sh" "$DIR/deploy/backup/"*.sh
systemctl restart google-cloud-ops-agent || true

REGISTRY="$(md apexx-image | cut -d/ -f1)"
gcloud auth configure-docker "$REGISTRY" --quiet

cat > /etc/systemd/system/apexx.service <<UNIT
[Unit]
Description=Apexx stack (docker compose)
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=$DIR
ExecStartPre=$DIR/deploy/gcp/fetch-secrets.sh
ExecStartPre=/usr/bin/docker compose pull --quiet
ExecStart=/usr/bin/docker compose up -d --remove-orphans
ExecStop=/usr/bin/docker compose stop
TimeoutStartSec=600

[Install]
WantedBy=multi-user.target
UNIT
cat > /etc/systemd/system/apexx-backup.service <<UNIT
[Unit]
Description=Apexx nightly PostgreSQL backup to GCS
[Service]
Type=oneshot
WorkingDirectory=$DIR
Environment=BACKUP_BUCKET=$(md apexx-backup-bucket)
ExecStart=$DIR/deploy/backup/pg-backup.sh
UNIT
cat > /etc/systemd/system/apexx-backup.timer <<UNIT
[Unit]
Description=Run Apexx backup nightly
[Timer]
OnCalendar=*-*-* 03:15:00
RandomizedDelaySec=900
Persistent=true
[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl enable --now apexx-backup.timer
systemctl enable apexx.service
systemctl restart apexx.service
