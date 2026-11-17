#!/usr/bin/env bash
# Writes deploy/gcp/startup-navegador.sh: the VM startup script with every
# file it needs inside, for a VM created by clicking in the Google Cloud
# console (one paste instead of seven). Run it after changing any of those
# files; a test fails while the committed copy is out of date.
set -euo pipefail
cd "$(dirname "$0")/../.."
declare -A SRC=(
  [apexx-compose]=docker-compose.yml
  [apexx-caddyfile]=deploy/caddy/Caddyfile
  [apexx-fetch-secrets]=deploy/gcp/fetch-secrets.sh
  [apexx-backup]=deploy/backup/pg-backup.sh
  [apexx-restore]=deploy/backup/pg-restore.sh
  [apexx-ops-agent]=deploy/gcp/ops-agent.yaml
)
D=__APEXX_FIN_DEL_ARCHIVO__
OUT="${1:-deploy/gcp/startup-navegador.sh}"
{
  echo '#!/usr/bin/env bash'
  echo '# GENERADO por deploy/gcp/build-browser-startup.sh: no lo edites a mano.'
  echo '# Script de arranque para una VM creada desde la consola web de Google Cloud.'
  echo '# Pégalo completo en "Automatización > Script de inicio" al crear la VM.'
  tail -n +2 deploy/gcp/startup.sh | while IFS= read -r line; do
    if [[ $line =~ ^md\ (apexx-[a-z-]+)\ +\>\ (.+)$ ]] && [ -n "${SRC[${BASH_REMATCH[1]}]:-}" ]; then
      f="${SRC[${BASH_REMATCH[1]}]}"
      if grep -q "$D" "$f"; then echo "el delimitador aparece en $f" >&2; exit 1; fi
      [ -z "$(tail -c1 "$f")" ] || { echo "$f no termina en salto de línea" >&2; exit 1; }
      echo "cat > ${BASH_REMATCH[2]} <<'$D'"
      cat "$f"
      echo "$D"
    else
      printf '%s\n' "$line"
    fi
  done
} > "$OUT"
chmod +x "$OUT"
echo "$OUT: $(wc -c < "$OUT") bytes"
