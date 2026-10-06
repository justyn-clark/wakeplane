# Wakeplane

[![CI](https://github.com/justyn-clark/wakeplane/actions/workflows/ci.yml/badge.svg)](https://github.com/justyn-clark/wakeplane/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> **Public beta - `v0.3.0-beta.1`.** Single-operator bearer auth is available, but there is no RBAC or multi-tenancy. Bind to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network. Download the published binaries from [GitHub Releases](https://github.com/justyn-clark/wakeplane/releases/tag/v0.3.0-beta.1). See [SECURITY.md](SECURITY.md).

Wakeplane is a durable scheduling control plane for long-running systems.

It decides when work is due, records the occurrence durably, dispatches execution through typed targets, and keeps an append-only ledger of what happened.

This is not a thin wrapper around cron. Cron is only one schedule input. The control plane above it is the product:

- durable schedules
- typed job targets
- retries and backoff
- missed-run handling
- overlap and concurrency policy
- append-only run ledger
- operator visibility
- pluggable executors
- reusable API and CLI surface

Wakeplane is designed as a reusable primitive across JCN systems. Nothing here is treated as disposable.

## Current Status

Current shipped state:

- pre-stable public beta release line
- core boundary preserved across distinct planner, dispatcher, store, executor, API, and CLI packages
- single-process Go daemon and CLI
- SQLite-first storage with embedded migrations and a Postgres production backend seam
- planner and dispatcher loops
- HTTP, shell, and in-process workflow executors
- HTTP JSON API and Cobra CLI
- embedded single-operator console at `/console/`
- guided schedule creation/editing, reusable recipes, and next-run previews
- MCP assistant tools at `/v1/mcp`, sharing the API auth and audit boundary
- durable asynchronous HTTP job tracking with progress, results, and artifacts
- idempotent event delivery to enabled schedules
- metrics, health, readiness, and status endpoints
- structured shutdown and drain logging
- restart, stale-lease, contention, and non-cooperative shutdown coverage

Current limits:

- native Postgres column types are still conservative/text-compatible in this first production backend slice
- no RBAC, distributed coordination, DAG editor, or plugin loading
- remote job runners must implement the documented idempotency and status contract
- remote cancellation, OAuth account connections, and enforced provider spending budgets are not implemented
- workflow handlers must be registered explicitly by the embedding application or tests
- `replace` is cooperative and best-effort, not forceful
- shell targets inherit the daemon environment; per-target env or secret injection is not implemented
- the current embedding surface is source-level and internal-package based, not a stable public library API

Core invariants currently enforced in code:

- execution only starts after a durable run record exists and the dispatcher has claimed it
- `timezone` is required and validated as an IANA timezone
- targets are typed as `http`, `shell`, or `workflow`
- retries append new attempts under the same `occurrence_key`
- policy enforcement happens at claim time, not inside executors

## Operational Context

Wakeplane has a real local-operator deployment that remains an important proving ground for the product.

That provenance is operational context, not product lock-in. Wakeplane is still intended to stand on its own:

- as the local scheduler on a personal machine
- as the control plane for small internal systems
- as a standalone tool other operators can run without inheriting any private operator environment

Single-machine operation is a first-class use case, not a temporary bootstrap mode. Changes here should not regress the local system Wakeplane was built to support.

## Supported Model

Scheduling:

- cron schedules
- interval schedules
- once schedules
- timezone-aware next-run calculation
- pause and resume
- manual trigger-now without changing normal cadence

Execution:

- HTTP executor
- shell executor
- in-process workflow executor backed by a registry
- durable claim before execution
- execution receipts for stdout, stderr, HTTP response summary, and workflow result
- configurable receipt body size bounds and terminal run retention
- retry with exponential backoff

Policy:

- overlap policies: `allow`, `forbid`, `queue_latest`, `replace`
- misfire policies: `skip`, `run_once_if_late`, `catch_up`
- timeout enforcement
- max concurrency per schedule

Durability and audit:

- SQLite-backed schedules and runs by default; Postgres is selectable for production installs
- append-only attempt history per logical occurrence
- worker leases with stale-claim recovery
- dead-letter capture for exhausted failures
- Prometheus text metrics at `/v1/metrics`
- operational status counts for due, running, failed, retry-queued, dead-lettered, and expired-claim work

## How To Use

1. Build the binary, or plan to run the daemon directly from source.
2. Start the daemon.
3. Create schedules from a YAML manifest.
4. Inspect schedules and runs with the console, CLI, or HTTP API.
5. Register workflow handlers explicitly if you use workflow targets.

For assistant access, event-triggered work, and remote job tracking, see [Automation](docs/public/automation.md). The [runnable recipes](docs/public/recipes.md) demonstrate a read-only repository watch and a weekly RSS/Atom reading summary with optional webhook notification. Connector and agent logic stays in the runner, outside Wakeplane's core.

Build both entry points:

```bash
go build -o dist/wakeplane ./cmd/wakeplane
go build -o dist/wakeplaned ./cmd/wakeplaned
```

If you are running directly from source, use `go run ./cmd/wakeplane serve` in place of the binary invocation below. If you built into `dist/` and did not install into `PATH`, prefix commands with `./dist/`.

For local console development from source:

```bash
make console
# or, if you use just:
just console
```

Then open `http://127.0.0.1:8080/console/`. Override the address or dev database with `make console ADDR=127.0.0.1:18080 DB=./tmp/wakeplane.db` or `just console 127.0.0.1:18080 ./tmp/wakeplane.db`.

## Install

Preferred operator path: download tagged archives and checksums from [GitHub Releases](https://github.com/justyn-clark/wakeplane/releases).

One-command install for macOS or Linux:

```bash
curl -fsSL https://wakeplane.dev/install.sh | sh
```

Additional install paths:

- `go install github.com/justyn-clark/wakeplane/cmd/wakeplane@latest`
- `go install github.com/justyn-clark/wakeplane/cmd/wakeplaned@latest`
- source build with the repo's declared Go version (`go 1.25.0`)

See [docs/public/install.md](docs/public/install.md) for the full install and smoke-test flow.

Example daemon start:

```bash
WAKEPLANE_DB_PATH=./wakeplane.db \
WAKEPLANE_HTTP_ADDR=:8080 \
WAKEPLANE_WORKER_ID=wrk_local \
wakeplane serve
```

Create a schedule from one of the shipped examples:

```bash
wakeplane schedule create -f ./examples/nightly-sync.yaml
```

Common operator commands:

```text
wakeplane schedule list
wakeplane schedule get <id>
wakeplane schedule pause <id>
wakeplane schedule resume <id>
wakeplane schedule delete <id>
wakeplane schedule trigger <id>
wakeplane run list
wakeplane run get <id>
```

Both `wakeplane` and `wakeplaned` currently expose the same command surface.

HTTP surface:

```text
GET    /healthz
GET    /readyz
GET    /v1/status
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
GET    /v1/metrics
```

The daemon also serves the local operator console at `http://localhost:8080/console/`. The console uses the same `/v1/...` API as the CLI. If `WAKEPLANE_AUTH_TOKEN` is set, open the console and enter the bearer token with the Token button before inspecting runs or schedules.

## Embedding

Wakeplane does not ship hidden workflow handlers. Embedding applications must register each workflow explicitly.

See [examples/embedded/main.go](examples/embedded/main.go) for a minimal daemon that:

- constructs `app.NewWithOptions(...)`
- registers a workflow with `app.WithWorkflowHandler(...)`
- exposes the HTTP control-plane API with `api.NewMux(...)`
- coordinates service and HTTP shutdown on process cancellation

Minimal registration shape:

```go
service, err := app.NewWithOptions(ctx, cfg,
	app.WithWorkflowHandler("sync.customers", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		return map[string]any{"status": "completed"}, nil
	}),
)
```

## Runtime Configuration

The daemon reads configuration from environment variables:

- `WAKEPLANE_HTTP_ADDR` default `:8080`
- `WAKEPLANE_STORE` default `sqlite`; set to `postgres` for the production Postgres backend
- `WAKEPLANE_DB_PATH` default `./wakeplane.db`
- `WAKEPLANE_DATABASE_URL` required when `WAKEPLANE_STORE=postgres`
- `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS` default `5`
- `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` default `2`
- `WAKEPLANE_LEASE_TTL_SECONDS` default `30`
- `WAKEPLANE_WORKER_ID` default `wrk_local`
- `WAKEPLANE_RECEIPT_MAX_BYTES` default `262144`
- `WAKEPLANE_RUN_RETENTION_DAYS` default `0` (disabled)
- `WAKEPLANE_AUTH_TOKEN` default unset; when set, `/v1/...` requires `Authorization: Bearer <token>`
- `WAKEPLANE_REQUEST_AUDIT` default `true`

## SQLite to Postgres Bridge

SQLite remains the default local mode. To move schedules to Postgres, export an import-compatible schedule manifest from the SQLite-backed daemon, start a fresh Postgres-backed daemon, then import it:

```bash
wakeplane schedule export > schedules.json

WAKEPLANE_STORE=postgres \
WAKEPLANE_DATABASE_URL=postgres://wakeplane:secret@db.example.com:5432/wakeplane \
wakeplane serve

wakeplane schedule import --file schedules.json
wakeplane status
```

This bridge moves schedule definitions. Run history, receipts, audit logs, leases, and dead letters stay in the source database unless restored with database-native backup tooling.

## Docs Map

- [Public Docs Home](docs/public/index.md)
- [Install](docs/public/install.md)
- [CLI Reference](docs/public/cli.md)
- [Public API Reference](docs/public/api.md)
- [Operator Console](docs/public/console.md)
- [Public Status](docs/public/status.md)
- [Current Status](docs/current-status.md)
- [Architecture](docs/architecture.md)
- [API Contract](docs/api-contract.md)
- [Run States](docs/run-states.md)
- [Embedding Contract](docs/embedding.md)
- [Operator Runbook](docs/runbook.md)
- [Storage Interface](docs/storage-interface.md)
- [Storage Portability](docs/storage-portability.md)
- [Replace Semantics](docs/replace-semantics.md)
- [SQLite Audit](docs/sqlite-audit.md)
- [Release Discipline](docs/release.md)
- [Deployment Notes](docs/deployment.md)

For the current coherence audit, hardening gaps, and scale path, see [Current Status](docs/current-status.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## License

MIT - see [LICENSE](LICENSE).

## Development

Runtime and build execution state in this repo is tracked through `small`.

- Use `small plan`, `small checkpoint`, `small handoff`, and `small check --strict` for agent-owned state.
- Use `small draft` and `small accept` for human-owned `.small` artifacts.
- Use `small apply --task ... --cmd ...` for build, test, and verification commands.
