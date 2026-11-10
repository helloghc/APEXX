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
