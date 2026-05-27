#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_dir="${WAKEPLANE_DRILL_ARTIFACT_DIR:-${root_dir}/artifacts/drills/restart-$(date -u +%Y%m%dT%H%M%SZ)}"
port="${WAKEPLANE_RESTART_PORT:-$((18081 + ($$ % 1000)))}"
addr="http://127.0.0.1:${port}"
db_path="${artifact_dir}/wakeplane.db"
bin_path="${artifact_dir}/wakeplane"
auth_token="${WAKEPLANE_AUTH_TOKEN:-restart-local-token}"
pid=""

cleanup() {
  if [[ -n "${pid}" ]] && kill -0 "${pid}" >/dev/null 2>&1; then
    kill "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

start_daemon() {
  WAKEPLANE_STORE=sqlite \
  WAKEPLANE_DB_PATH="${db_path}" \
  WAKEPLANE_HTTP_ADDR="127.0.0.1:${port}" \
  WAKEPLANE_WORKER_ID="$1" \
  WAKEPLANE_SCHEDULER_INTERVAL_SECONDS=1 \
  WAKEPLANE_DISPATCHER_INTERVAL_SECONDS=1 \
  WAKEPLANE_LEASE_TTL_SECONDS=2 \
  WAKEPLANE_AUTH_TOKEN="${auth_token}" \
  "${bin_path}" serve >>"${artifact_dir}/daemon.log" 2>&1 &
  pid="$!"
  for _ in $(seq 1 60); do
    if curl -fs "${addr}/readyz" >"${artifact_dir}/readyz-${1}.json" 2>/dev/null; then
      return
    fi
    sleep 1
  done
  curl -fsS "${addr}/readyz" >/dev/null
}

wait_for_count() {
  local query="$1"
  local want="$2"
  for _ in $(seq 1 60); do
    got="$(sqlite3 "${db_path}" "${query}")"
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
start_daemon "wrk_restart_a"

cat >"${artifact_dir}/quick.yaml" <<'YAML'
name: restart-quick
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
    - "echo restart-completed"
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

cat >"${artifact_dir}/slow.yaml" <<'YAML'
name: restart-slow
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
    - "sleep 20; echo should-not-complete-before-crash"
policy:
  overlap: forbid
  misfire: run_once_if_late
  timeout_seconds: 30
  max_concurrency: 1
retry:
  max_attempts: 2
  strategy: exponential
  initial_delay_seconds: 1
  max_delay_seconds: 2
YAML

WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule create --file "${artifact_dir}/quick.yaml" >"${artifact_dir}/create-quick.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule create --file "${artifact_dir}/slow.yaml" >"${artifact_dir}/create-slow.json"

quick_id="$(sqlite3 "${db_path}" "SELECT id FROM schedules WHERE name = 'restart-quick';")"
slow_id="$(sqlite3 "${db_path}" "SELECT id FROM schedules WHERE name = 'restart-slow';")"
pending_id="run_pending_$(date +%s)"
pending_time="$(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)"
sqlite3 "${db_path}" "
  INSERT INTO schedule_runs (
    id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, created_at, updated_at
  ) VALUES (
    '${pending_id}', '${quick_id}', '${quick_id}:pending-drill', '${pending_time}', '${pending_time}', 'pending', 1, '${pending_time}', '${pending_time}'
  );
"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule trigger "${quick_id}" >"${artifact_dir}/trigger-quick.json"
wait_for_count "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${quick_id}' AND status = 'succeeded';" "1"

WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" schedule trigger "${slow_id}" >"${artifact_dir}/trigger-slow.json"
wait_for_count "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${slow_id}' AND status = 'running';" "1"

kill -KILL "${pid}" >/dev/null 2>&1 || true
wait "${pid}" >/dev/null 2>&1 || true
pid=""

start_daemon "wrk_restart_b"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" status --watch --every 1 >"${artifact_dir}/status-watch-after-restart.txt" 2>&1 &
watch_pid="$!"
sleep 5
kill "${watch_pid}" >/dev/null 2>&1 || true
wait "${watch_pid}" >/dev/null 2>&1 || true
curl -fsS -H "Authorization: Bearer ${auth_token}" "${addr}/v1/status" >"${artifact_dir}/status-after-restart.json"
WAKEPLANE_AUTH_TOKEN="${auth_token}" "${bin_path}" --addr "${addr}" run list >"${artifact_dir}/runs-after-restart.json"

completed_after="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${quick_id}' AND status = 'succeeded';")"
failed_or_retry="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${slow_id}' AND status IN ('failed','retry_scheduled','dead_lettered');")"
expired_claims="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${slow_id}' AND status IN ('claimed','running') AND claim_expires_at <= datetime('now');")"
pending_after="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE id = '${pending_id}' AND status = 'pending';")"
running_after="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${slow_id}' AND status = 'running';")"
failed_after="$(sqlite3 "${db_path}" "SELECT COUNT(*) FROM schedule_runs WHERE schedule_id = '${slow_id}' AND status = 'failed';")"
status_watch_samples="$(grep -c '^wakeplane ' "${artifact_dir}/status-watch-after-restart.txt" || true)"
commit="$(git rev-parse HEAD 2>/dev/null || true)"

cat >"${artifact_dir}/receipt.json" <<JSON
{
  "drill": "restart",
  "commit": "${commit}",
  "config": {
    "store": "sqlite",
    "database_path": "${db_path}",
    "lease_ttl_seconds": 2
  },
  "evidence": {
    "completed_run_count_after_restart": ${completed_after},
    "unfinished_run_failed_or_retry_count": ${failed_or_retry},
    "expired_active_claims_after_recovery": ${expired_claims},
    "pending_run_count_after_restart": ${pending_after},
    "running_run_count_after_restart": ${running_after},
    "failed_run_count_after_restart": ${failed_after},
    "status_watch_samples": ${status_watch_samples}
  },
  "expectation": "completed work is not repeated; crashed running work is recovered by lease expiry"
}
JSON

if [[ "${completed_after}" != "1" ]]; then
  echo "completed run was repeated or lost" >&2
  exit 1
fi
if [[ "${pending_after}" != "1" ]]; then
  echo "pending run was not preserved across restart" >&2
  exit 1
fi
if (( failed_or_retry < 1 )); then
  echo "crashed running work was not recovered into failed/retry/dead-letter state" >&2
  exit 1
fi
if (( running_after < 1 || failed_after < 1 )); then
  echo "restart drill did not observe running and failed states after restart" >&2
  exit 1
fi
if (( status_watch_samples < 2 )); then
  echo "status --watch did not produce recovery-visible samples" >&2
  exit 1
fi

echo "${artifact_dir}/receipt.json"
