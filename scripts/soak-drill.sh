#!/usr/bin/env bash
set -euo pipefail

duration_seconds="${WAKEPLANE_SOAK_DURATION_SECONDS:-30}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_dir="${WAKEPLANE_DRILL_ARTIFACT_DIR:-${root_dir}/artifacts/drills/soak-$(date -u +%Y%m%dT%H%M%SZ)}"
port="${WAKEPLANE_SOAK_PORT:-$((18080 + ($$ % 1000)))}"
addr="http://127.0.0.1:${port}"
db_path="${artifact_dir}/wakeplane.db"
bin_path="${artifact_dir}/wakeplane"
auth_token="${WAKEPLANE_AUTH_TOKEN:-soak-local-token}"
pid=""

cleanup() {
  if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
    kill "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

mkdir -p "${artifact_dir}"
go build -o "${bin_path}" ./cmd/wakeplane

WAKEPLANE_STORE=sqlite \
WAKEPLANE_DB_PATH="${db_path}" \
WAKEPLANE_HTTP_ADDR="127.0.0.1:${port}" \
WAKEPLANE_WORKER_ID="wrk_soak" \
WAKEPLANE_SCHEDULER_INTERVAL_SECONDS=1 \
WAKEPLANE_DISPATCHER_INTERVAL_SECONDS=1 \
WAKEPLANE_LEASE_TTL_SECONDS=3 \
WAKEPLANE_RECEIPT_MAX_BYTES=256 \
WAKEPLANE_RUN_RETENTION_DAYS=1 \
WAKEPLANE_AUTH_TOKEN="${auth_token}" \
"${bin_path}" serve >"${artifact_dir}/daemon.log" 2>&1 &
pid="$!"

for _ in $(seq 1 60); do
  if curl -fs "${addr}/readyz" >"${artifact_dir}/readyz.json" 2>/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS "${addr}/readyz" >/dev/null

cat >"${artifact_dir}/success.yaml" <<'YAML'
name: soak-success
enabled: true
timezone: UTC
schedule:
  kind: interval
  every_seconds: 1
target:
  kind: shell
  command: /bin/sh
  args:
    - -c
    - "printf 'wakeplane-soak-success-%0400d\n' 1"
policy:
  overlap: allow
  misfire: catch_up
  timeout_seconds: 5
  max_concurrency: 2
retry:
  max_attempts: 0
  strategy: exponential
  initial_delay_seconds: 1
  max_delay_seconds: 2
YAML

cat >"${artifact_dir}/failure.yaml" <<'YAML'
name: soak-failure
enabled: true
timezone: UTC
schedule:
  kind: interval
  every_seconds: 2
target:
  kind: shell
  command: /bin/sh
  args:
    - -c
    - "echo expected-soak-failure >&2; exit 2"
policy:
  overlap: forbid
  misfire: run_once_if_late
  timeout_seconds: 5
  max_concurrency: 1
retry:
  max_attempts: 2
  strategy: exponential
  initial_delay_seconds: 1
  max_delay_seconds: 2
YAML

WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule create --file "${artifact_dir}/success.yaml" >"${artifact_dir}/create-success.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule create --file "${artifact_dir}/failure.yaml" >"${artifact_dir}/create-failure.json"

start_epoch="$(date +%s)"
rss_start_kb="$(ps -o rss= -p "${pid}" | awk '{print $1}')"
sleep "${duration_seconds}"
rss_end_kb="$(ps -o rss= -p "${pid}" | awk '{print $1}')"

retention_probe_id="$(sqlite3 "${db_path}" "SELECT id FROM schedule_runs WHERE status = 'succeeded' AND finished_at IS NOT NULL LIMIT 1;")"
if [[ -n "${retention_probe_id}" ]]; then
  sqlite3 "${db_path}" "UPDATE schedule_runs SET finished_at = '2000-01-01T00:00:00Z', updated_at = '2000-01-01T00:00:00Z' WHERE id = '${retention_probe_id}';"
  sleep 2
fi
retention_probe_remaining="0"
if [[ -n "${retention_probe_id}" ]]; then
  retention_probe_remaining="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE id = '${retention_probe_id}';")"
fi

curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/status" >"${artifact_dir}/status.json"
curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/metrics" >"${artifact_dir}/metrics.txt"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" run list >"${artifact_dir}/runs.json"

end_epoch="$(date +%s)"
run_count="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs;")"
failure_count="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE status IN ('failed','dead_lettered');")"
retry_count="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE attempt > 1 OR status = 'retry_scheduled';")"
receipt_count="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM execution_receipts;")"
max_receipt_bytes="$(sqlite3 "${db_path}" "SELECT COALESCE(MAX(length(body)),0) FROM execution_receipts;")"
audit_count="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM request_audit_logs;")"
retention_days="1"
commit="$(git rev-parse HEAD 2>/dev/null || true)"

cat >"${artifact_dir}/receipt.json" <<JSON
{
  "drill": "soak",
  "commit": "${commit}",
  "started_at_epoch": ${start_epoch},
  "finished_at_epoch": ${end_epoch},
  "duration_seconds": ${duration_seconds},
  "config": {
    "store": "sqlite",
    "database_path": "${db_path}",
    "receipt_max_bytes": 256,
    "run_retention_days": ${retention_days},
    "lease_ttl_seconds": 3
  },
  "counts": {
    "runs": ${run_count},
    "failures_or_dead_letters": ${failure_count},
    "retries": ${retry_count},
    "receipts": ${receipt_count},
    "request_audit_rows": ${audit_count},
    "max_receipt_bytes": ${max_receipt_bytes},
    "retention_probe_remaining": ${retention_probe_remaining}
  },
  "process": {
    "rss_start_kb": ${rss_start_kb:-0},
    "rss_end_kb": ${rss_end_kb:-0}
  },
  "expected_failures": "soak-failure intentionally exits 2"
}
JSON

if (( max_receipt_bytes > 256 )); then
  echo "receipt body exceeded configured bound" >&2
  exit 1
fi
if (( run_count == 0 || receipt_count == 0 )); then
  echo "soak did not produce runs and receipts" >&2
  exit 1
fi
if [[ -n "${retention_probe_id}" && "${retention_probe_remaining}" != "0" ]]; then
  echo "retention did not prune aged terminal run" >&2
  exit 1
fi

echo "${artifact_dir}/receipt.json"
