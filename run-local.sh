#!/usr/bin/env bash
# Arranca la instancia LOCAL de demo en :8097. Uso: ./run-local.sh
set -euo pipefail
cd "$(dirname "$0")"
source .env.local
exec ./apexx-local serve
