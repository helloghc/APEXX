#!/usr/bin/env bash
# Regional managed instance group (≥2 VMs in different zones) with autohealing
# on /readyz, behind a global external HTTPS load balancer with a
# Google-managed certificate. A VM that stops answering is recreated
# automatically; the LB stops sending it traffic within seconds.
set -euo pipefail
cd "$(dirname "$0")/.."; source ./config.sh
ROOT="$(cd ../.. && pwd)"
CLOUDSQL_HOST="${CLOUDSQL_HOST:?run ha/10-cloudsql.sh and export CLOUDSQL_HOST}"
SIZE="${MIG_SIZE:-2}"; TPL="$NAME-tpl-$(date +%Y%m%d%H%M%S)"

# The HA startup script reuses startup.sh with the HA compose override.
HA_STARTUP="$(mktemp)"
sed -e 's#/usr/bin/docker compose pull --quiet#/usr/bin/docker compose -f docker-compose.yml -f deploy/gcp/ha/docker-compose.ha.yml pull --quiet apexx migrate autoheal#' \
    -e 's#/usr/bin/docker compose up -d --remove-orphans#/usr/bin/docker compose -f docker-compose.yml -f deploy/gcp/ha/docker-compose.ha.yml up -d --remove-orphans apexx autoheal#' \
    -e 's#^mkdir -p "\$DIR/deploy/caddy"#mkdir -p "$DIR/deploy/gcp/ha" "$DIR/deploy/caddy"#' \
    -e 's#^md apexx-ops-agent#md apexx-compose-ha > "$DIR/deploy/gcp/ha/docker-compose.ha.yml"\nmd apexx-ops-agent#' \
    "$ROOT/deploy/gcp/startup.sh" > "$HA_STARTUP"

gc compute instance-templates create "$TPL" --region "$REGION" --machine-type "$MACHINE_TYPE" \
  --image-family ubuntu-2404-lts-amd64 --image-project ubuntu-os-cloud --boot-disk-size 30GB \
  --service-account "$SA_EMAIL" --scopes cloud-platform --tags "$NAME-lb" --no-address \
  --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
  --metadata "apexx-domain=$DOMAIN,apexx-image=$IMAGE,apexx-cloudsql-host=$CLOUDSQL_HOST,apexx-backup-bucket=$BACKUP_BUCKET,enable-oslogin=TRUE,apexx-payments=${APEXX_PAYMENTS:-stripe}" \
  --metadata-from-file "startup-script=$HA_STARTUP,apexx-compose=$ROOT/docker-compose.yml,apexx-compose-ha=$ROOT/deploy/gcp/ha/docker-compose.ha.yml,apexx-caddyfile=$ROOT/deploy/caddy/Caddyfile,apexx-fetch-secrets=$ROOT/deploy/gcp/fetch-secrets.sh,apexx-backup=$ROOT/deploy/backup/pg-backup.sh,apexx-restore=$ROOT/deploy/backup/pg-restore.sh,apexx-ops-agent=$ROOT/deploy/gcp/ops-agent.yaml"

gc compute firewall-rules describe "$NAME-lb-hc" >/dev/null 2>&1 ||
  gc compute firewall-rules create "$NAME-lb-hc" --network default --allow tcp:8080 \
    --source-ranges 130.211.0.0/22,35.191.0.0/16 --target-tags "$NAME-lb"
gc compute health-checks describe "$NAME-readyz" >/dev/null 2>&1 ||
  gc compute health-checks create http "$NAME-readyz" --port 8080 --request-path /readyz \
    --check-interval 5s --timeout 3s --healthy-threshold 2 --unhealthy-threshold 3

if gc compute instance-groups managed describe "$NAME-mig" --region "$REGION" >/dev/null 2>&1; then
  # Rolling update: one new VM at a time, never below capacity.
  gc compute instance-groups managed rolling-action start-update "$NAME-mig" --region "$REGION" \
    --version "template=$TPL" --max-surge 1 --max-unavailable 0
else
  gc compute instance-groups managed create "$NAME-mig" --region "$REGION" --template "$TPL" --size "$SIZE" \
    --health-check "$NAME-readyz" --initial-delay 180
  gc compute instance-groups managed set-named-ports "$NAME-mig" --region "$REGION" --named-ports http:8080
  gc compute instance-groups managed set-autoscaling "$NAME-mig" --region "$REGION" \
    --min-num-replicas "$SIZE" --max-num-replicas 6 --target-cpu-utilization 0.6 --cool-down-period 120
fi

gc compute backend-services describe "$NAME-be" --global >/dev/null 2>&1 || {
  gc compute backend-services create "$NAME-be" --global --protocol HTTP --port-name http \
    --health-checks "$NAME-readyz" --timeout 30s --connection-draining-timeout 25s --enable-logging
  gc compute backend-services add-backend "$NAME-be" --global --instance-group "$NAME-mig" --instance-group-region "$REGION"
}
gc compute url-maps describe "$NAME-map" >/dev/null 2>&1 || gc compute url-maps create "$NAME-map" --default-service "$NAME-be"
gc compute ssl-certificates describe "$NAME-cert" >/dev/null 2>&1 || gc compute ssl-certificates create "$NAME-cert" --domains "$DOMAIN" --global
gc compute target-https-proxies describe "$NAME-https" >/dev/null 2>&1 || gc compute target-https-proxies create "$NAME-https" --url-map "$NAME-map" --ssl-certificates "$NAME-cert"
gc compute addresses describe "$NAME-lb-ip" --global >/dev/null 2>&1 || gc compute addresses create "$NAME-lb-ip" --global
gc compute forwarding-rules describe "$NAME-https-fr" --global >/dev/null 2>&1 ||
  gc compute forwarding-rules create "$NAME-https-fr" --global --address "$NAME-lb-ip" --target-https-proxy "$NAME-https" --ports 443
echo "IP del balanceador: $(gc compute addresses describe "$NAME-lb-ip" --global --format 'value(address)') → registro DNS A para $DOMAIN"
