# Quickstart

Wakeplane is a durable scheduling control plane. This guide gets you from nothing to a running daemon with a real schedule in under five minutes.

This guide covers the stable `v1.0.0` release, including the console, authentication, and Postgres options described here. See [Install](install.md) for downloads and checksum verification.

> **Operator warning:** Wakeplane supports single-operator bearer auth for `/v1/...`, but it has no RBAC or multi-tenancy. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network. Do not expose it directly to the public internet. See [Security](security.md).

## What you are setting up

- A single-process daemon that runs the planner and dispatcher loops
- An SQLite database that stores schedules and run records
- A CLI client to create and inspect work

## 0. Install a binary or build from source

Use the release, `go install`, or source-build path in [Install](install.md). For source builds, the repo currently declares `go 1.25.0` in `go.mod`.

Wakeplane is designed to work well as a local single-machine scheduler.

If you built into `dist/` and did not install into `PATH`, prefix commands below with `./dist/`.

## 1. Start the daemon

```bash
WAKEPLANE_DB_PATH=./wakeplane.db \
WAKEPLANE_HTTP_ADDR=127.0.0.1:8080 \
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

Open the local operator console:

```text
http://localhost:8080/console/
```

If `WAKEPLANE_AUTH_TOKEN` is set, use the Token button in the console before inspecting runs or schedules.

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

| Variable                                | Default          | Description                                    |
| --------------------------------------- | ---------------- | ---------------------------------------------- |
| `WAKEPLANE_DB_PATH`                     | `./wakeplane.db` | SQLite database file                           |
| `WAKEPLANE_STORE`                       | `sqlite`         | Storage backend: `sqlite` or `postgres`        |
| `WAKEPLANE_DATABASE_URL`                | unset            | Connection URL for the Postgres backend        |
| `WAKEPLANE_HTTP_ADDR`                   | `:8080`          | HTTP listen address                            |
| `WAKEPLANE_WORKER_ID`                   | `wrk_local`      | Worker identity (used in lease records)        |
| `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS`  | `5`              | How often the planner loop ticks               |
| `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` | `2`              | How often the dispatcher loop ticks            |
| `WAKEPLANE_LEASE_TTL_SECONDS`           | `30`             | Worker lease TTL for stale-claim recovery      |
| `WAKEPLANE_RECEIPT_MAX_BYTES`           | `262144`         | Maximum stored body size per receipt           |
| `WAKEPLANE_RUN_RETENTION_DAYS`          | `0`              | Days to keep terminal runs; 0 disables pruning |
| `WAKEPLANE_AUTH_TOKEN`                  | unset            | Require a bearer token for `/v1/...` routes    |
| `WAKEPLANE_REQUEST_AUDIT`               | `true`           | Record control-plane request metadata          |

The daemon's default listen address is `:8080`, which binds all interfaces. This quickstart explicitly binds loopback. The CLI reads `WAKEPLANE_AUTH_TOKEN` when calling a protected daemon; authenticated `curl` requests also need the matching bearer header.

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

- [Concepts](concepts.md) - understand how Wakeplane thinks about scheduling
- [Schedules](schedules.md) - YAML manifest shape, cron/interval/once, timezone behavior
- [Policies](policies.md) - overlap, misfire, retry, and concurrency
- [Executors](executors.md) - HTTP, shell, and workflow targets
- [Embedding](embedding.md) - use Wakeplane as a library in your Go application
- [Operator Console](console.md) - local inspection surface for runs, schedules, receipts, and daemon posture
- [Status](status.md) - beta gate, 1.0 gate, and explicit scope boundaries
