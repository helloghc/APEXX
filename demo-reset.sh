#!/usr/bin/env bash
# Resetea el AMBIENTE DE DEMO (base apexx_demo + instancia :8098) a su estado
# "dorado". Borra lo que hayan tocado los visitantes. NO toca a los clientes.
set -uo pipefail
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
cd "$(dirname "$0")"
GOLDEN=".demo-golden/apexx_demo.dump"
[ -f "$GOLDEN" ] || { echo "falta snapshot dorado: $GOLDEN"; exit 1; }
# 1) parar la instancia demo (suelta conexiones)
PIDS=$(lsof -nP -tiTCP:8098 -sTCP:LISTEN 2>/dev/null || true); [ -n "$PIDS" ] && kill $PIDS 2>/dev/null || true
sleep 2
# 2) restaurar la base demo al dorado
psql "postgres://andy@localhost:5432/postgres?sslmode=disable" -q -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='apexx_demo' AND pid<>pg_backend_pid();" >/dev/null 2>&1 || true
dropdb --if-exists -h localhost -U andy apexx_demo
createdb -h localhost -U andy apexx_demo
pg_restore -h localhost -U andy -d apexx_demo --no-owner "$GOLDEN" >/dev/null 2>&1 || true
# 3) re-levantar la instancia demo
source .env.demo
nohup ./apexx-local serve >/tmp/apexx-demo.log 2>&1 </dev/null & disown 2>/dev/null || true
echo "demo reseteada y reiniciada $(date '+%F %T')"
