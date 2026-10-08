#!/usr/bin/env bash
set -euo pipefail

image="wakeplane-container-smoke:${GITHUB_SHA:-local}"
daemon="wakeplane-daemon-smoke-$$"
runner="wakeplane-runner-smoke-$$"
daemon_volume="${daemon}-data"
runner_volume="${runner}-data"
token="container-smoke-only-token"

cleanup() {
  docker rm -f "$daemon" "$runner" >/dev/null 2>&1 || true
  docker volume rm "$daemon_volume" "$runner_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -t "$image" .
docker volume create "$daemon_volume" >/dev/null
docker volume create "$runner_volume" >/dev/null
# Simulate Railway's root-owned mount before invoking the bootstrap.
docker run --rm --user 0 -v "${runner_volume}:/data" --entrypoint sh "$image" -c 'chown 0:0 /data; chmod 755 /data'
docker run -d --name "$daemon" -p 127.0.0.1::8080 \
  -v "${daemon_volume}:/data" -e PORT=8080 -e WAKEPLANE_AUTH_TOKEN="$token" "$image" >/dev/null

wait_ready() {
  local name="$1" path="$2" port
  port="$(docker port "$name" 8080/tcp | awk -F: '{print $NF}')" || {
    docker logs "$name" >&2
    return 1
  }
  for _ in $(seq 1 60); do
    if [[ "$(docker inspect -f '{{.State.Running}}' "$name")" != true ]]; then
      docker logs "$name" >&2
      return 1
    fi
    if curl -fsS "http://127.0.0.1:${port}${path}" >/dev/null 2>&1; then
      printf '%s' "$port"
      return
    fi
    sleep 1
  done
  docker logs "$name" >&2
  return 1
}

daemon_port="$(wait_ready "$daemon" /readyz)"
test "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${daemon_port}/v1/status")" = 401
curl -fsS -H "Authorization: Bearer $token" "http://127.0.0.1:${daemon_port}/v1/status" >/dev/null
docker cp examples/developer-repository-watch-discord.yaml "${daemon}:/tmp/schedule.yaml"
docker exec "$daemon" wakeplane schedule create -f /tmp/schedule.yaml >/dev/null
docker restart --time 30 "$daemon" >/dev/null
daemon_port="$(wait_ready "$daemon" /readyz)"
curl -fsS -H "Authorization: Bearer $token" "http://127.0.0.1:${daemon_port}/v1/schedules" | \
  python3 -c 'import json,sys; data=json.load(sys.stdin); assert "weekday-repository-watch-discord" in json.dumps(data), data'

docker run -d --user 0 --name "$runner" -p 127.0.0.1::8080 \
  -v "${runner_volume}:/data" -e PORT=8080 -e CONTAINER_HEALTH_PATH=/healthz \
  -e AUTOMATION_RUNNER_ADDR=:8080 -e AUTOMATION_RUNNER_STATE_DIR=/data/runner \
  -e AUTOMATION_RUNNER_PUBLIC_URL=http://127.0.0.1:8080 \
  -e AUTOMATION_RUNNER_TOKEN="$token" "$image" /usr/local/bin/automation-runner >/dev/null
runner_port="$(wait_ready "$runner" /healthz)"
test "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${runner_port}/jobs/lookup")" = 401
docker restart --time 30 "$runner" >/dev/null
wait_ready "$runner" /healthz >/dev/null
test "$(docker exec "$daemon" id -u)" = 10001
test "$(docker exec "$runner" cat /proc/1/status | awk '/^Uid:/{print $2}')" = 10001
printf '%s\n' 'Container build, non-root execution, auth, SQLite persistence, and runner restart PASS.'
