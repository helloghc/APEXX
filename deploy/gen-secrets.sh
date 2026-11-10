#!/usr/bin/env bash
# Prints fresh random secrets in .env format. Store them in Secret Manager
# (deploy/gcp/push-secrets.sh) — do not commit them.
set -euo pipefail
rand() { openssl rand -base64 48 | tr -d '/+=\n' | cut -c1-40; }
echo "POSTGRES_PASSWORD=$(rand)"
echo "APP_DB_PASSWORD=$(rand)"
echo "SYSTEM_DB_PASSWORD=$(rand)"
echo "APEXX_MASTER_KEY=$(openssl rand -hex 32)"
