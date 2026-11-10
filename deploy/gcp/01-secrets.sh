#!/usr/bin/env bash
# Creates the Secret Manager secrets. Generates random DB passwords and the
# master key if they do not exist yet; Stripe values are read from the
# environment (leave empty if stores bring their own keys). Each secret is
# readable only by the VM service account. Idempotent: never overwrites.
set -euo pipefail
cd "$(dirname "$0")"; source ./config.sh

gen() {
  case "$1" in
    APEXX_MASTER_KEY) openssl rand -hex 32 ;;
    STRIPE_SECRET_KEY|STRIPE_WEBHOOK_SECRET) printf '%s' "${!1:-}" ;;
    *) openssl rand -base64 48 | tr -d '/+=\n' | cut -c1-40 ;;
  esac
}
for s in "${SECRETS[@]}"; do
  if gc secrets describe "$s" >/dev/null 2>&1; then
    echo "existe: $s (no se modifica)"
  else
    gc secrets create "$s" --replication-policy automatic >/dev/null
    v="$(gen "$s")"; [ -n "$v" ] || v="unset"
    printf '%s' "$v" | gc secrets versions add "$s" --data-file=- >/dev/null
    echo "creado: $s"
  fi
  gc secrets add-iam-policy-binding "$s" --member "serviceAccount:$SA_EMAIL" \
    --role roles/secretmanager.secretAccessor >/dev/null
done
echo "IMPORTANTE: respalda APEXX_MASTER_KEY fuera de línea. Sin ella los datos cifrados no se pueden leer."
