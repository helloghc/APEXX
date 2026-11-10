#!/usr/bin/env bash
# Single-VM production: static IP, firewall 80/443 only (SSH via IAP),
# Shielded VM, OS Login, least-privilege service account. Idempotent.
set -euo pipefail
cd "$(dirname "$0")"; source ./config.sh
ROOT="$(cd ../.. && pwd)"

gc compute addresses describe "$NAME-ip" --region "$REGION" >/dev/null 2>&1 ||
  gc compute addresses create "$NAME-ip" --region "$REGION"
IP="$(gc compute addresses describe "$NAME-ip" --region "$REGION" --format 'value(address)')"

gc compute firewall-rules describe "$NAME-web" >/dev/null 2>&1 ||
  gc compute firewall-rules create "$NAME-web" --network default --direction INGRESS \
    --allow tcp:80,tcp:443,udp:443 --target-tags "$NAME-web" --source-ranges 0.0.0.0/0
# SSH only through Identity-Aware Proxy, never from the internet.
gc compute firewall-rules describe "$NAME-iap-ssh" >/dev/null 2>&1 ||
  gc compute firewall-rules create "$NAME-iap-ssh" --network default --direction INGRESS \
    --allow tcp:22 --target-tags "$NAME-web" --source-ranges 35.235.240.0/20

META="apexx-domain=$DOMAIN,apexx-acme-email=$ACME_EMAIL,apexx-image=$IMAGE,apexx-backup-bucket=$BACKUP_BUCKET,enable-oslogin=TRUE,apexx-payments=${APEXX_PAYMENTS:-stripe},apexx-fee-bps=${APEXX_PLATFORM_FEE_BPS:-0}"
FILES="startup-script=$ROOT/deploy/gcp/startup.sh,apexx-compose=$ROOT/docker-compose.yml,apexx-caddyfile=$ROOT/deploy/caddy/Caddyfile,apexx-fetch-secrets=$ROOT/deploy/gcp/fetch-secrets.sh,apexx-backup=$ROOT/deploy/backup/pg-backup.sh,apexx-restore=$ROOT/deploy/backup/pg-restore.sh,apexx-ops-agent=$ROOT/deploy/gcp/ops-agent.yaml"

if gc compute instances describe "$NAME" --zone "$ZONE" >/dev/null 2>&1; then
  echo "La VM existe: actualizo metadatos y reinicio el stack."
  gc compute instances add-metadata "$NAME" --zone "$ZONE" --metadata "$META" --metadata-from-file "$FILES"
  gc compute ssh "$NAME" --zone "$ZONE" --tunnel-through-iap --command "sudo google_metadata_script_runner startup"
else
  gc compute instances create "$NAME" --zone "$ZONE" --machine-type "$MACHINE_TYPE" \
    --image-family ubuntu-2404-lts-amd64 --image-project ubuntu-os-cloud \
    --boot-disk-size "${DISK_GB}GB" --boot-disk-type pd-balanced \
    --address "$IP" --tags "$NAME-web" \
    --service-account "$SA_EMAIL" --scopes cloud-platform \
    --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
    --metadata "$META" --metadata-from-file "$FILES"
  # Disk snapshots every day, kept 14 days (second line of defence besides pg_dump).
  gc compute resource-policies describe "$NAME-daily" --region "$REGION" >/dev/null 2>&1 ||
    gc compute resource-policies create snapshot-schedule "$NAME-daily" --region "$REGION" \
      --daily-schedule --start-time 04:00 --max-retention-days 14
  gc compute disks add-resource-policies "$NAME" --zone "$ZONE" --resource-policies "$NAME-daily" || true
fi
echo "IP estática: $IP  →  crea un registro DNS A: $DOMAIN → $IP"
echo "Luego: https://$DOMAIN/readyz debe responder 'ready' (Caddy obtiene el certificado al primer acceso)."
