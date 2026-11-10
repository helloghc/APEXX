#!/usr/bin/env bash
# Runs ON THE VM at boot (systemd ExecStartPre). Reads every secret from
# Secret Manager with the VM service account and writes /opt/apexx/.env
# (0600, root). Public settings come from instance metadata.
set -euo pipefail
DIR=/opt/apexx
md() { curl -fsS -H 'Metadata-Flavor: Google' "http://metadata.google.internal/computeMetadata/v1/instance/attributes/$1" 2>/dev/null || true; }
umask 077
tmp="$(mktemp "$DIR/.env.XXXXXX")"
{
  echo "APEXX_DOMAIN=$(md apexx-domain)"
  echo "ACME_EMAIL=$(md apexx-acme-email)"
  echo "APEXX_IMAGE=$(md apexx-image)"
  echo "CADDY_TLS_MODE=acme"
  p="$(md apexx-payments)"; echo "APEXX_PAYMENTS=${p:-stripe}"
  f="$(md apexx-fee-bps)"; echo "APEXX_PLATFORM_FEE_BPS=${f:-0}"
  echo "CLOUDSQL_HOST=$(md apexx-cloudsql-host)"   # only set in the HA setup
  for s in POSTGRES_PASSWORD APP_DB_PASSWORD SYSTEM_DB_PASSWORD APEXX_MASTER_KEY STRIPE_SECRET_KEY STRIPE_WEBHOOK_SECRET; do
    v="$(gcloud secrets versions access latest --secret "$s" 2>/dev/null || true)"
    [ "$v" = "unset" ] && v=""
    printf '%s=%s\n' "$s" "$v"
  done
} > "$tmp"
chmod 600 "$tmp"
mv -f "$tmp" "$DIR/.env"
