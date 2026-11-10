#!/usr/bin/env bash
# Builds the image with Cloud Build and pushes it to Artifact Registry.
set -euo pipefail
cd "$(dirname "$0")/../.."; source deploy/gcp/config.sh
gcloud builds submit --project "$PROJECT_ID" --region "$REGION" --tag "$IMAGE" .
echo "Imagen: $IMAGE"
