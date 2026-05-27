#!/usr/bin/env bash
set -euo pipefail

container_name="${WAKEPLANE_POSTGRES_TEST_CONTAINER:-wakeplane-postgres-test}"
port="${WAKEPLANE_POSTGRES_TEST_PORT:-55432}"
database_url="postgres://wakeplane:wakeplane@127.0.0.1:${port}/wakeplane?sslmode=disable"
packages=(./internal/store ./internal/app ./internal/dispatcher ./internal/cli)
local_pg_dir=""
local_pg_bin=""

run_postgres_tests() {
  for package in "${packages[@]}"; do
    go test "${package}" -run Postgres -count=1
  done
}

find_postgres_bin() {
  if command -v initdb >/dev/null 2>&1 && command -v pg_ctl >/dev/null 2>&1 && command -v createdb >/dev/null 2>&1; then
    dirname "$(command -v initdb)"
    return
  fi
  for dir in /opt/homebrew/opt/postgresql@17/bin /opt/homebrew/opt/postgresql@16/bin /opt/homebrew/opt/postgresql@15/bin /usr/local/opt/postgresql@17/bin /usr/local/opt/postgresql@16/bin /usr/local/opt/postgresql@15/bin; do
    if [[ -x "${dir}/initdb" && -x "${dir}/pg_ctl" && -x "${dir}/createdb" ]]; then
      echo "${dir}"
      return
    fi
  done
}

cleanup_local_pg() {
  if [[ -n "${local_pg_dir}" && -n "${local_pg_bin}" ]]; then
    "${local_pg_bin}/pg_ctl" -D "${local_pg_dir}" -m fast -w stop >/dev/null 2>&1 || true
    rm -rf "${local_pg_dir}"
  fi
}

if [[ -n "${WAKEPLANE_POSTGRES_TEST_URL:-}" ]]; then
  run_postgres_tests
  exit 0
fi

local_pg_bin="$(find_postgres_bin || true)"
if [[ -n "${local_pg_bin}" ]]; then
  local_pg_dir="$(mktemp -d)"
  trap cleanup_local_pg EXIT
  "${local_pg_bin}/initdb" -D "${local_pg_dir}" -A trust -U wakeplane >/dev/null
  "${local_pg_bin}/pg_ctl" -D "${local_pg_dir}" -o "-h 127.0.0.1 -p ${port}" -w start >/dev/null
  "${local_pg_bin}/createdb" -h 127.0.0.1 -p "${port}" -U wakeplane wakeplane
  WAKEPLANE_POSTGRES_TEST_URL="${database_url}" run_postgres_tests
  exit 0
fi

cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup
docker run -d \
  --name "${container_name}" \
  -e POSTGRES_USER=wakeplane \
  -e POSTGRES_PASSWORD=wakeplane \
  -e POSTGRES_DB=wakeplane \
  -p "127.0.0.1:${port}:5432" \
  postgres:16-alpine >/dev/null

for _ in $(seq 1 60); do
  if docker exec "${container_name}" pg_isready -U wakeplane -d wakeplane >/dev/null 2>&1; then
    WAKEPLANE_POSTGRES_TEST_URL="${database_url}" run_postgres_tests
    exit 0
  fi
  sleep 1
done

echo "postgres test container did not become ready" >&2
exit 1
