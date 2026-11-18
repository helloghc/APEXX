#!/usr/bin/env bash
# Resetea el AMBIENTE DE DEMO (base apexx_demo) a su estado "dorado".
# Restaura el snapshot tomado tras sembrar; borra cualquier cambio que hayan
# hecho los visitantes. No toca tu base de producción.
set -euo pipefail
cd "$(dirname "$0")"
GOLDEN=".demo-golden/apexx_demo.dump"
MAINT="postgres://andy@localhost:5432/postgres?sslmode=disable"
[ -f "$GOLDEN" ] || { echo "falta snapshot dorado: $GOLDEN"; exit 1; }
psql "$MAINT" -q -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='apexx_demo' AND pid<>pg_backend_pid();" >/dev/null 2>&1 || true
dropdb --if-exists -h localhost -U andy apexx_demo
createdb -h localhost -U andy apexx_demo
pg_restore -h localhost -U andy -d apexx_demo --no-owner "$GOLDEN" >/dev/null 2>&1 || true
echo "demo reseteada $(date '+%F %T')"
