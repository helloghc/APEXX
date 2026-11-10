#!/usr/bin/env bash
# Starts a real Apexx server for the admin E2E test:
# builds the binary (embedding web/dist), creates a fresh store with an owner,
# seeds 50 products and serves on 127.0.0.1:8094 with sandbox payments.
# Database settings come from APEXX_E2E_ENV (a file of `export VAR=...` lines).
set -euo pipefail
cd "$(dirname "$0")/../.."
ENV_FILE="${APEXX_E2E_ENV:-/tmp/claude-0/-home-user-APEXX/40fac8be-9880-5b1d-a60d-791befcacbef/scratchpad/dev.env}"
# shellcheck disable=SC1090
source "$ENV_FILE"
OUT=/tmp/apexx-e2e
mkdir -p "$OUT"
go build -o "$OUT/apexx" ./cmd/apexx
"$OUT/apexx" migrate >/dev/null
TENANT="e2e-$(date +%s)-$RANDOM"
PASS="e2e-owner-password-1"
"$OUT/apexx" tenant -id "$TENANT" -name "E2E" -owner-email "owner@$TENANT.test" -owner-password "$PASS" > "$OUT/tenant.txt"
"$OUT/apexx" seed -tenant "$TENANT" -n 50 >/dev/null
SF=$(grep -o 'apx_[a-z0-9_]*' "$OUT/tenant.txt" | head -1)
cat > "$OUT/tenant.json" <<JSON
{"tenant":"$TENANT","email":"owner@$TENANT.test","password":"$PASS","storefrontKey":"$SF"}
JSON
export APEXX_ADDR=127.0.0.1:8094 APEXX_PAYMENTS=sandbox APEXX_SECURE_COOKIES=false
exec "$OUT/apexx" serve
