# Shared settings for the GCP scripts. Override with environment variables.
PROJECT_ID="${PROJECT_ID:?export PROJECT_ID=tu-proyecto}"
REGION="${REGION:-us-central1}"
ZONE="${ZONE:-us-central1-a}"
NAME="${NAME:-apexx}"
DOMAIN="${DOMAIN:?export DOMAIN=tienda.example.com}"
ACME_EMAIL="${ACME_EMAIL:-ops@${DOMAIN#*.}}"
MACHINE_TYPE="${MACHINE_TYPE:-e2-standard-4}"
DISK_GB="${DISK_GB:-100}"
BACKUP_BUCKET="${BACKUP_BUCKET:-${PROJECT_ID}-${NAME}-backups}"
BACKUP_RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-30}"
REPO_URL="${REPO_URL:-https://github.com/helloghc/apexx.git}"
REPO_REF="${REPO_REF:-main}"
AR_REPO="${AR_REPO:-apexx}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
IMAGE="${IMAGE:-${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/apexx:${IMAGE_TAG}}"
SA_NAME="${SA_NAME:-${NAME}-vm}"
SA_EMAIL="${SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
# Secret Manager secret names (one secret per variable).
SECRETS=(POSTGRES_PASSWORD APP_DB_PASSWORD SYSTEM_DB_PASSWORD APEXX_MASTER_KEY STRIPE_SECRET_KEY STRIPE_WEBHOOK_SECRET)
gc() { gcloud --project "$PROJECT_ID" --quiet "$@"; }
