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
