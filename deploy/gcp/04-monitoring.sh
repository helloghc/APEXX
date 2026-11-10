#!/usr/bin/env bash
# Uptime check on /readyz and alert policies (uptime, 5xx rate, projection lag).
# NOTIFY_EMAIL receives alerts. Idempotent by display name.
set -euo pipefail
cd "$(dirname "$0")"; source ./config.sh
NOTIFY_EMAIL="${NOTIFY_EMAIL:?export NOTIFY_EMAIL=ops@tu-dominio}"

CH="$(gc beta monitoring channels list --filter "displayName=\"$NAME-ops\"" --format 'value(name)' | head -1)"
[ -n "$CH" ] || CH="$(gc beta monitoring channels create --display-name "$NAME-ops" --type email \
  --channel-labels "email_address=$NOTIFY_EMAIL" --format 'value(name)')"

gc monitoring uptime list-configs --format 'value(displayName)' | grep -qx "$NAME-readyz" ||
  gc monitoring uptime create "$NAME-readyz" --resource-type uptime-url \
    --resource-labels "host=$DOMAIN,project_id=$PROJECT_ID" --path /readyz --protocol https \
    --period 1 --timeout 10 --matcher-content ready --matcher-type contains-string

policy() { # name, json
  gc alpha monitoring policies list --format 'value(displayName)' | grep -qx "$1" && return 0
  printf '%s' "$2" > /tmp/apexx-policy.json
  gc alpha monitoring policies create --policy-from-file /tmp/apexx-policy.json
}
policy "$NAME uptime" "{\"displayName\":\"$NAME uptime\",\"combiner\":\"OR\",\"notificationChannels\":[\"$CH\"],
 \"conditions\":[{\"displayName\":\"/readyz falla\",\"conditionThreshold\":{
  \"filter\":\"metric.type=\\\"monitoring.googleapis.com/uptime_check/check_passed\\\" AND resource.type=\\\"uptime_url\\\" AND metric.label.check_id=\\\"$NAME-readyz\\\"\",
  \"aggregations\":[{\"alignmentPeriod\":\"60s\",\"perSeriesAligner\":\"ALIGN_NEXT_OLDER\",\"crossSeriesReducer\":\"REDUCE_COUNT_FALSE\",\"groupByFields\":[\"resource.label.*\"]}],
  \"comparison\":\"COMPARISON_GT\",\"thresholdValue\":1,\"duration\":\"120s\"}}]}"
policy "$NAME projection lag" "{\"displayName\":\"$NAME projection lag\",\"combiner\":\"OR\",\"notificationChannels\":[\"$CH\"],
 \"conditions\":[{\"displayName\":\"Desfase del proyector > 2 s\",\"conditionThreshold\":{
  \"filter\":\"metric.type=\\\"prometheus.googleapis.com/apexx_projection_lag_ms/gauge\\\" AND metric.label.kind=\\\"last\\\"\",
  \"aggregations\":[{\"alignmentPeriod\":\"60s\",\"perSeriesAligner\":\"ALIGN_MAX\"}],
  \"comparison\":\"COMPARISON_GT\",\"thresholdValue\":2000,\"duration\":\"120s\"}}]}"
policy "$NAME 5xx" "{\"displayName\":\"$NAME 5xx\",\"combiner\":\"OR\",\"notificationChannels\":[\"$CH\"],
 \"conditions\":[{\"displayName\":\"Errores 5xx\",\"conditionThreshold\":{
  \"filter\":\"metric.type=\\\"prometheus.googleapis.com/apexx_http_requests_total/counter\\\" AND metric.label.code=monitoring.regex.full_match(\\\"5..\\\")\",
  \"aggregations\":[{\"alignmentPeriod\":\"300s\",\"perSeriesAligner\":\"ALIGN_RATE\",\"crossSeriesReducer\":\"REDUCE_SUM\"}],
  \"comparison\":\"COMPARISON_GT\",\"thresholdValue\":0.5,\"duration\":\"300s\"}}]}"
echo "Monitoreo listo."
