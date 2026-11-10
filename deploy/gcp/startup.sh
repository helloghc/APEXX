#!/usr/bin/env bash
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
md apexx-compose      > "$DIR/docker-compose.yml"
md apexx-caddyfile    > "$DIR/deploy/caddy/Caddyfile"
md apexx-fetch-secrets > "$DIR/deploy/gcp/fetch-secrets.sh"
md apexx-backup       > "$DIR/deploy/backup/pg-backup.sh"
md apexx-restore      > "$DIR/deploy/backup/pg-restore.sh"
md apexx-ops-agent    > /etc/google-cloud-ops-agent/config.yaml
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
