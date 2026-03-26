# Wakeplane

[![CI](https://github.com/justyn-clark/wakeplane/actions/workflows/ci.yml/badge.svg)](https://github.com/justyn-clark/wakeplane/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> **Public beta — pre-stable `v0.2.0-beta.1`.** No authentication or RBAC. Bind to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network. See [SECURITY.md](SECURITY.md).

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

- pre-stable `v0.2.0-beta.1`
- single-process Go daemon and CLI
- SQLite-first storage with embedded migrations
- planner and dispatcher loops
- HTTP, shell, and in-process workflow executors
- HTTP JSON API and Cobra CLI
- metrics, health, readiness, and status endpoints
- structured shutdown and drain logging
- restart, stale-lease, contention, and non-cooperative shutdown coverage

Current limits:

- Postgres is only planned at the storage seam
- no auth, RBAC, UI, distributed coordination, or plugin loading
- workflow handlers must be registered explicitly by the embedding application or tests
- `replace` is cooperative and best-effort, not forceful

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
- retry with exponential backoff

Policy:

- overlap policies: `allow`, `forbid`, `queue_latest`, `replace`
- misfire policies: `skip`, `run_once_if_late`, `catch_up`
- timeout enforcement
- max concurrency per schedule

Durability and audit:

- SQLite-backed schedules and runs
- append-only attempt history per logical occurrence
- worker leases with stale-claim recovery
- dead-letter capture for exhausted failures
- Prometheus text metrics at `/v1/metrics`
- operational status counts for due, running, failed, retry-queued, dead-lettered, and expired-claim work

## How To Use

1. Start the daemon.
2. Create schedules from a YAML manifest.
3. Inspect schedules and runs with the CLI or HTTP API.
4. Register workflow handlers explicitly if you use workflow targets.

## Install

Preferred operator path: download tagged archives and checksums from [GitHub Releases](https://github.com/justyn-clark/wakeplane/releases).

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
- `WAKEPLANE_DB_PATH` default `./wakeplane.db`
- `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS` default `5`
- `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` default `2`
- `WAKEPLANE_LEASE_TTL_SECONDS` default `30`
- `WAKEPLANE_WORKER_ID` default `wrk_local`

## Docs Map

- [Public Docs Home](docs/public/index.md)
- [Install](docs/public/install.md)
- [CLI Reference](docs/public/cli.md)
- [Public API Reference](docs/public/api.md)
- [Public Status](docs/public/status.md)
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

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## License

MIT — see [LICENSE](LICENSE).

## Development

Runtime and build execution state in this repo is tracked through `small`.

- Use `small plan`, `small checkpoint`, `small handoff`, and `small check --strict` for agent-owned state.
- Use `small draft` and `small accept` for human-owned `.small` artifacts.
- Use `small apply --task ... --cmd ...` for build, test, and verification commands.
