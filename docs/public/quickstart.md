# Quickstart

Wakeplane is a durable scheduling control plane. This guide gets you from nothing to a running daemon with a real schedule in under five minutes.

> **Operator warning:** Wakeplane currently has no auth or RBAC. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network. Do not expose it directly to the public internet. See [Security](security.md).

## What you are setting up

- A single-process daemon that runs the planner and dispatcher loops
- An SQLite database that stores schedules and run records
- A CLI client to create and inspect work

## 0. Install a binary or build from source

Use the release, `go install`, or source-build path in [Install](install.md). For source builds, the repo currently declares `go 1.25.0` in `go.mod`.

Wakeplane is designed to work well as a local single-machine scheduler. That includes the Mac Mini deployment where it was born and personal-machine installs such as a MacBook Air.

If you built into `dist/` and did not install into `PATH`, prefix commands below with `./dist/`.

## 1. Start the daemon

```bash
WAKEPLANE_DB_PATH=./wakeplane.db \
WAKEPLANE_HTTP_ADDR=:8080 \
WAKEPLANE_WORKER_ID=wrk_local \
wakeplane serve
```

The daemon prints structured JSON logs to stdout. Verify it is healthy:

```bash
curl http://localhost:8080/healthz
# {"ok":true}

curl http://localhost:8080/readyz
# {"ok":true,"storage":"ok"}
```

## 2. Create a schedule

Use the shipped HTTP example manifest:

```yaml
# health-check.yaml
name: health-check
enabled: true
timezone: UTC

schedule:
  kind: interval
  every_seconds: 300

target:
  kind: http
  method: GET
  url: https://api.example.com/healthz

policy:
  overlap: forbid
  misfire: skip
  timeout_seconds: 30
  max_concurrency: 1

retry:
  max_attempts: 3
  strategy: exponential
  initial_delay_seconds: 10
  max_delay_seconds: 120
```

Register it:

```bash
wakeplane schedule create -f ./examples/health-check-http.yaml
```

## 3. Inspect schedules and runs

```bash
wakeplane schedule list
wakeplane schedule get <id>
wakeplane run list
wakeplane run get <id>
```

Check operational status:

```bash
curl http://localhost:8080/v1/status
```

The status response shows how many runs are due, running, failed, retry queued, or dead-lettered.

## 4. Trigger a manual run

```bash
wakeplane schedule trigger <id>
```

This creates a run immediately without changing the schedule's normal cadence. The run has a `manual:<run_id>` occurrence key that is separate from scheduled occurrences.

## 5. Pause and resume

```bash
wakeplane schedule pause <id>
wakeplane schedule resume <id>
```

Pausing sets `enabled=false` on the schedule. The planner stops materializing new occurrences. Existing runs are not cancelled.

## Environment variables

| Variable                                | Default          | Description                               |
| --------------------------------------- | ---------------- | ----------------------------------------- |
| `WAKEPLANE_DB_PATH`                     | `./wakeplane.db` | SQLite database file                      |
| `WAKEPLANE_HTTP_ADDR`                   | `:8080`          | HTTP listen address                       |
| `WAKEPLANE_WORKER_ID`                   | `wrk_local`      | Worker identity (used in lease records)   |
| `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS`  | `5`              | How often the planner loop ticks          |
| `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` | `2`              | How often the dispatcher loop ticks       |
| `WAKEPLANE_LEASE_TTL_SECONDS`           | `30`             | Worker lease TTL for stale-claim recovery |

## HTTP surface

All schedule and run management is available through the HTTP API:

```
POST   /v1/schedules
GET    /v1/schedules
GET    /v1/schedules/{id}
PUT    /v1/schedules/{id}
PATCH  /v1/schedules/{id}
DELETE /v1/schedules/{id}
POST   /v1/schedules/{id}/pause
POST   /v1/schedules/{id}/resume
POST   /v1/schedules/{id}/trigger
GET    /v1/schedules/{id}/runs
GET    /v1/runs
GET    /v1/runs/{id}
GET    /v1/runs/{id}/receipts
GET    /v1/status
GET    /v1/metrics
GET    /healthz
GET    /readyz
```

## Next steps

- [Concepts](concepts.md) — understand how Wakeplane thinks about scheduling
- [Schedules](schedules.md) — YAML manifest shape, cron/interval/once, timezone behavior
- [Policies](policies.md) — overlap, misfire, retry, and concurrency
- [Executors](executors.md) — HTTP, shell, and workflow targets
- [Embedding](embedding.md) — use Wakeplane as a library in your Go application
- [Status](status.md) — beta gate, 1.0 gate, and explicit scope boundaries
