#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_dir="${WAKEPLANE_DRILL_ARTIFACT_DIR:-${root_dir}/artifacts/drills/backup-restore-$(date -u +%Y%m%dT%H%M%SZ)}"
port="${WAKEPLANE_BACKUP_PORT:-$((18082 + ($$ % 1000)))}"
addr="http://127.0.0.1:${port}"
source_db="${artifact_dir}/wakeplane-source.db"
backup_db="${artifact_dir}/wakeplane-backup.db"
restore_db="${artifact_dir}/wakeplane-restored.db"
bin_path="${artifact_dir}/wakeplane"
auth_token="${WAKEPLANE_AUTH_TOKEN:-backup-local-token}"
pid=""

cleanup() {
  if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
    kill "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

start_daemon() {
  local db="$1"
  local worker="$2"
  WAKEPLANE_STORE=sqlite \
  WAKEPLANE_DB_PATH="${db}" \
  WAKEPLANE_HTTP_ADDR="127.0.0.1:${port}" \
  WAKEPLANE_WORKER_ID="${worker}" \
  WAKEPLANE_SCHEDULER_INTERVAL_SECONDS=1 \
  WAKEPLANE_DISPATCHER_INTERVAL_SECONDS=1 \
  WAKEPLANE_LEASE_TTL_SECONDS=2 \
  WAKEPLANE_RECEIPT_MAX_BYTES=512 \
  WAKEPLANE_RUN_RETENTION_DAYS=7 \
  WAKEPLANE_AUTH_TOKEN="${auth_token}" \
  "${bin_path}" serve >>"${artifact_dir}/daemon.log" 2>&1 &
  pid="$!"
  for _ in $(seq 1 60); do
    if curl -fs "${addr}/readyz" >"${artifact_dir}/readyz-${worker}.json" 2>/dev/null; then
      return
    fi
    sleep 1
  done
  curl -fsS "${addr}/readyz" >/dev/null
}

wait_for_count() {
  local db="$1"
  local query="$2"
  local want="$3"
  for _ in $(seq 1 60); do
    got="$(sqlite3 "${db}" "${query}")"
    if [[ "${got}" == "${want}" ]]; then
      return
    fi
    sleep 1
  done
  echo "timed out waiting for ${query} == ${want}, got ${got}" >&2
  exit 1
}

mkdir -p "${artifact_dir}"
go build -o "${bin_path}" ./cmd/wakeplane
start_daemon "${source_db}" "wrk_backup_source"

cat >"${artifact_dir}/backup.yaml" <<'YAML'
name: backup-restore-check
enabled: false
timezone: UTC
schedule:
  kind: interval
  every_seconds: 60
target:
  kind: shell
  command: /bin/sh
  args:
    - -c
    - "echo backup-restore-receipt"
policy:
  overlap: forbid
  misfire: run_once_if_late
  timeout_seconds: 5
  max_concurrency: 1
retry:
  max_attempts: 0
  strategy: exponential
  initial_delay_seconds: 1
  max_delay_seconds: 2
YAML

WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule create --file "${artifact_dir}/backup.yaml" >"${artifact_dir}/create.json"
schedule_id="$(sqlite3 "${source_db}" "SELECT id FROM schedules WHERE name = 'backup-restore-check';")"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule trigger "${schedule_id}" >"${artifact_dir}/trigger.json"
wait_for_count "${source_db}" "SELECT COUNT(*) FROM schedule_runs WHERE status = 'succeeded';" "1"
curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/status" >"${artifact_dir}/source-status.json"

sqlite3 "${source_db}" ".backup '${backup_db}'"
cp "${backup_db}" "${restore_db}"

kill "${pid}" >/dev/null 2>&1 || true
wait "${pid}" >/dev/null 2>&1 || true
pid=""

start_daemon "${restore_db}" "wrk_backup_restore"
curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/status" >"${artifact_dir}/restored-status.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule export >"${artifact_dir}/restored-schedules.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" run list >"${artifact_dir}/restored-runs.json"
grep -q '"storage":"ok"' "${artifact_dir}/readyz-wrk_backup_restore.json"
grep -q '"run_retention_days":7' "${artifact_dir}/restored-status.json"
grep -q '"receipt_max_bytes":512' "${artifact_dir}/restored-status.json"

source_schedules="$(sqlite3 "${source_db}" "SELECT COUNT(*) FROM schedules;")"
restore_schedules="$(sqlite3 "${restore_db}" "SELECT COUNT(*) FROM schedules;")"
source_runs="$(sqlite3 "${source_db}" "SELECT COUNT(*) FROM schedule_runs;")"
restore_runs="$(sqlite3 "${restore_db}" "SELECT COUNT(*) FROM schedule_runs;")"
source_receipts="$(sqlite3 "${source_db}" "SELECT COUNT(*) FROM execution_receipts;")"
restore_receipts="$(sqlite3 "${restore_db}" "SELECT COUNT(*) FROM execution_receipts;")"
source_audit="$(sqlite3 "${source_db}" "SELECT COUNT(*) FROM request_audit_logs;")"
restore_audit="$(sqlite3 "${restore_db}" "SELECT COUNT(*) FROM request_audit_logs;")"
commit="$(git rev-parse HEAD 2>/dev/null || true)"

cat >"${artifact_dir}/receipt.json" <<JSON
{
  "drill": "backup_restore",
  "commit": "${commit}",
  "config": {
    "store": "sqlite",
    "source_database_path": "${source_db}",
    "backup_database_path": "${backup_db}",
    "restore_database_path": "${restore_db}",
    "run_retention_days": 7,
    "receipt_max_bytes": 512
  },
  "counts": {
    "source_schedules": ${source_schedules},
    "restored_schedules": ${restore_schedules},
    "source_runs": ${source_runs},
    "restored_runs": ${restore_runs},
    "source_receipts": ${source_receipts},
    "restored_receipts": ${restore_receipts},
    "source_request_audit_rows": ${source_audit},
    "restored_request_audit_rows": ${restore_audit},
    "restored_status_retention_verified": true
  },
  "expectation": "restored instance is usable and preserves schedules, run history, receipts, audit rows, retention settings, and status output"
}
JSON

if [[ "${source_schedules}" != "${restore_schedules}" || "${source_runs}" != "${restore_runs}" || "${source_receipts}" != "${restore_receipts}" ]]; then
  echo "restored database counts do not match source" >&2
  exit 1
fi

echo "${artifact_dir}/receipt.json"
