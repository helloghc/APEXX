#!/usr/bin/env bash
# Cloud SQL for PostgreSQL 16, regional HA (synchronous standby in another zone),
# private IP only, automated backups with point-in-time recovery. Idempotent.
set -euo pipefail
cd "$(dirname "$0")/.."; source ./config.sh
SQL="${SQL_INSTANCE:-$NAME-pg}"; TIER="${SQL_TIER:-db-custom-4-16384}"

gc services enable sqladmin.googleapis.com servicenetworking.googleapis.com
gc compute addresses describe google-managed-services-default --global >/dev/null 2>&1 ||
  gc compute addresses create google-managed-services-default --global --purpose VPC_PEERING --prefix-length 16 --network default
gc services vpc-peerings connect --service servicenetworking.googleapis.com --ranges google-managed-services-default --network default || true

if ! gc sql instances describe "$SQL" >/dev/null 2>&1; then
  gc sql instances create "$SQL" --database-version POSTGRES_16 --region "$REGION" --tier "$TIER" \
    --availability-type REGIONAL --storage-type SSD --storage-auto-increase \
    --no-assign-ip --network default --require-ssl \
    --backup --backup-start-time 03:00 --enable-point-in-time-recovery --retained-backups-count 14 \
    --database-flags max_connections=400,log_min_duration_statement=500 \
    --deletion-protection
fi
gc sql databases describe apexx --instance "$SQL" >/dev/null 2>&1 || gc sql databases create apexx --instance "$SQL"
gc sql users set-password postgres --instance "$SQL" --password "$(gc secrets versions access latest --secret POSTGRES_PASSWORD)"
echo "CLOUDSQL_HOST=$(gc sql instances describe "$SQL" --format 'value(ipAddresses[0].ipAddress)')"
