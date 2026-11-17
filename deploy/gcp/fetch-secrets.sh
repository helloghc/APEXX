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
