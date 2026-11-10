#!/usr/bin/env bash
# Enables APIs, creates the Artifact Registry repo, backup bucket (with
# retention), and a least-privilege service account for the VM. Idempotent.
set -euo pipefail
cd "$(dirname "$0")"; source ./config.sh

gc services enable compute.googleapis.com secretmanager.googleapis.com artifactregistry.googleapis.com \
  logging.googleapis.com monitoring.googleapis.com storage.googleapis.com cloudbuild.googleapis.com

gc artifacts repositories describe "$AR_REPO" --location "$REGION" >/dev/null 2>&1 ||
  gc artifacts repositories create "$AR_REPO" --repository-format docker --location "$REGION"

if ! gcloud storage buckets describe "gs://$BACKUP_BUCKET" --project "$PROJECT_ID" >/dev/null 2>&1; then
  gcloud storage buckets create "gs://$BACKUP_BUCKET" --project "$PROJECT_ID" --location "$REGION" \
    --uniform-bucket-level-access --public-access-prevention
fi
cat > /tmp/apexx-lifecycle.json <<JSON
{"rule":[{"action":{"type":"Delete"},"condition":{"age":${BACKUP_RETENTION_DAYS}}}]}
JSON
gcloud storage buckets update "gs://$BACKUP_BUCKET" --project "$PROJECT_ID" --lifecycle-file /tmp/apexx-lifecycle.json
gcloud storage buckets update "gs://$BACKUP_BUCKET" --project "$PROJECT_ID" --versioning

gc iam service-accounts describe "$SA_EMAIL" >/dev/null 2>&1 ||
  gc iam service-accounts create "$SA_NAME" --display-name "Apexx VM"
for role in roles/logging.logWriter roles/monitoring.metricWriter roles/artifactregistry.reader; do
  gc projects add-iam-policy-binding "$PROJECT_ID" --member "serviceAccount:$SA_EMAIL" --role "$role" --condition None >/dev/null
done
# Bucket-scoped, not project-wide.
gcloud storage buckets add-iam-policy-binding "gs://$BACKUP_BUCKET" --project "$PROJECT_ID" \
  --member "serviceAccount:$SA_EMAIL" --role roles/storage.objectAdmin >/dev/null
echo "Bootstrap listo. Siguiente: deploy/gcp/01-secrets.sh"
