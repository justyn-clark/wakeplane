#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_dir="${WAKEPLANE_DRILL_ARTIFACT_DIR:-${root_dir}/artifacts/drills/postgres-backup-restore-$(date -u +%Y%m%dT%H%M%SZ)}"
pg_port="${WAKEPLANE_POSTGRES_DRILL_PORT:-$((55432 + ($$ % 1000)))}"
http_port="${WAKEPLANE_POSTGRES_BACKUP_HTTP_PORT:-$((19082 + ($$ % 1000)))}"
source_db="wakeplane_source"
restore_db="wakeplane_restored"
source_url="postgres://wakeplane@127.0.0.1:${pg_port}/${source_db}?sslmode=disable"
restore_url="postgres://wakeplane@127.0.0.1:${pg_port}/${restore_db}?sslmode=disable"
addr="http://127.0.0.1:${http_port}"
bin_path="${artifact_dir}/wakeplane"
auth_token="${WAKEPLANE_AUTH_TOKEN:-postgres-backup-local-token}"
pg_dir=""
pg_bin=""
pid=""

find_postgres_bin() {
  if command -v initdb >/dev/null 2>&1 && command -v pg_ctl >/dev/null 2>&1 && command -v createdb >/dev/null 2>&1 && command -v pg_dump >/dev/null 2>&1 && command -v psql >/dev/null 2>&1; then
    dirname "$(command -v initdb)"
    return
  fi
  for dir in /opt/homebrew/opt/postgresql@17/bin /opt/homebrew/opt/postgresql@16/bin /opt/homebrew/opt/postgresql@15/bin /usr/local/opt/postgresql@17/bin /usr/local/opt/postgresql@16/bin /usr/local/opt/postgresql@15/bin; do
    if [[ -x "${dir}/initdb" && -x "${dir}/pg_ctl" && -x "${dir}/createdb" && -x "${dir}/pg_dump" && -x "${dir}/psql" ]]; then
      echo "${dir}"
      return
    fi
  done
}

cleanup() {
  if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
    kill "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${pg_dir}" && -n "${pg_bin}" ]]; then
    "${pg_bin}/pg_ctl" -D "${pg_dir}" -m fast -w stop >/dev/null 2>&1 || true
    rm -rf "${pg_dir}"
  fi
}
trap cleanup EXIT

start_daemon() {
  local database_url="$1"
  local worker="$2"
  WAKEPLANE_STORE=postgres \
  WAKEPLANE_DATABASE_URL="${database_url}" \
  WAKEPLANE_HTTP_ADDR="127.0.0.1:${http_port}" \
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

pg_count() {
  local database="$1"
  local query="$2"
  "${pg_bin}/psql" -h 127.0.0.1 -p "${pg_port}" -U wakeplane -d "${database}" -At -c "${query}"
}

wait_for_count() {
  local database="$1"
  local query="$2"
  local want="$3"
  for _ in $(seq 1 60); do
    got="$(pg_count "${database}" "${query}")"
    if [[ "${got}" == "${want}" ]]; then
      return
    fi
    sleep 1
  done
  echo "timed out waiting for ${query} == ${want}, got ${got}" >&2
  exit 1
}

mkdir -p "${artifact_dir}"
pg_bin="$(find_postgres_bin || true)"
if [[ -z "${pg_bin}" ]]; then
  echo "postgres binaries not found; install postgres or use the SQLite backup drill" >&2
  exit 1
fi

go build -o "${bin_path}" ./cmd/wakeplane
pg_dir="$(mktemp -d)"
"${pg_bin}/initdb" -D "${pg_dir}" -A trust -U wakeplane >/dev/null
"${pg_bin}/pg_ctl" -D "${pg_dir}" -o "-h 127.0.0.1 -p ${pg_port}" -w start >/dev/null
"${pg_bin}/createdb" -h 127.0.0.1 -p "${pg_port}" -U wakeplane "${source_db}"

start_daemon "${source_url}" "wrk_pg_backup_source"

cat >"${artifact_dir}/postgres-backup.yaml" <<'YAML'
name: postgres-backup-restore-check
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
    - "echo postgres-backup-restore-receipt"
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

WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule create --file "${artifact_dir}/postgres-backup.yaml" >"${artifact_dir}/create.json"
schedule_id="$(pg_count "${source_db}" "SELECT id FROM schedules WHERE name = 'postgres-backup-restore-check';")"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule trigger "${schedule_id}" >"${artifact_dir}/trigger.json"
wait_for_count "${source_db}" "SELECT COUNT(*) FROM schedule_runs WHERE status = 'succeeded';" "1"
curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/status" >"${artifact_dir}/source-status.json"

"${pg_bin}/pg_dump" -h 127.0.0.1 -p "${pg_port}" -U wakeplane "${source_db}" >"${artifact_dir}/wakeplane-source.sql"
kill "${pid}" >/dev/null 2>&1 || true
wait "${pid}" >/dev/null 2>&1 || true
pid=""

"${pg_bin}/createdb" -h 127.0.0.1 -p "${pg_port}" -U wakeplane "${restore_db}"
"${pg_bin}/psql" -h 127.0.0.1 -p "${pg_port}" -U wakeplane -d "${restore_db}" -f "${artifact_dir}/wakeplane-source.sql" >/dev/null

start_daemon "${restore_url}" "wrk_pg_backup_restore"
curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/status" >"${artifact_dir}/restored-status.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule export >"${artifact_dir}/restored-schedules.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" run list >"${artifact_dir}/restored-runs.json"
grep -q '"storage":"ok"' "${artifact_dir}/readyz-wrk_pg_backup_restore.json"
grep -q '"driver":"postgres"' "${artifact_dir}/restored-status.json"

source_schedules="$(pg_count "${source_db}" "SELECT COUNT(*) FROM schedules;")"
restore_schedules="$(pg_count "${restore_db}" "SELECT COUNT(*) FROM schedules;")"
source_runs="$(pg_count "${source_db}" "SELECT COUNT(*) FROM schedule_runs;")"
restore_runs="$(pg_count "${restore_db}" "SELECT COUNT(*) FROM schedule_runs;")"
source_receipts="$(pg_count "${source_db}" "SELECT COUNT(*) FROM execution_receipts;")"
restore_receipts="$(pg_count "${restore_db}" "SELECT COUNT(*) FROM execution_receipts;")"
source_audit="$(pg_count "${source_db}" "SELECT COUNT(*) FROM request_audit_logs;")"
restore_audit="$(pg_count "${restore_db}" "SELECT COUNT(*) FROM request_audit_logs;")"
commit="$(git rev-parse HEAD 2>/dev/null || true)"

cat >"${artifact_dir}/receipt.json" <<JSON
{
  "drill": "postgres_backup_restore",
  "commit": "${commit}",
  "config": {
    "store": "postgres",
    "source_database_url": "${source_url}",
    "restore_database_url": "${restore_url}",
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
    "restored_request_audit_rows": ${restore_audit}
  },
  "expectation": "restored Postgres instance is usable and preserves schedules, run history, receipts, audit rows, and status output"
}
JSON

if [[ "${source_schedules}" != "${restore_schedules}" || "${source_runs}" != "${restore_runs}" || "${source_receipts}" != "${restore_receipts}" ]]; then
  echo "restored Postgres counts do not match source" >&2
  exit 1
fi

echo "${artifact_dir}/receipt.json"
