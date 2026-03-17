# Wakeplane

Wakeplane is a durable scheduling and automated execution engine for long-running systems.

It defines when work should happen, records what happened, and safely dispatches execution across local and remote runtimes.

This is not a thin wrapper around cron.

Cron is only one scheduling input. The real product is the control plane above it:

- durable schedules
- typed job targets
- retries and backoff
- missed-run handling
- overlap and concurrency policy
- append-only run ledger
- operator visibility
- pluggable executors
- reusable API and CLI surface

Wakeplane is designed as a standalone primitive that can be embedded across JCN systems.

## Design goals

- durable and restart-safe
- timezone-aware
- explicit run records
- pluggable executors
- reusable across products
- operator-friendly
- SQLite first, Postgres-ready
- static binary deployment

## Current status

This repository contains a working v1 bootstrap implementation:

- single-process Go daemon and CLI
- SQLite schema and embedded migrations
- planner and dispatcher loops
- HTTP, shell, and in-process workflow executors
- HTTP JSON API and Cobra CLI
- operator-facing metrics, health, and status endpoints
- structured shutdown lifecycle logging with drain visibility
- restart, stale-lease, contention, and non-cooperative shutdown tests

## Implemented v1 surface

Scheduling

- cron schedules
- interval schedules
- once schedules
- timezone-aware next-run calculation
- pause and resume
- manual trigger-now without changing normal cadence

Execution

- HTTP executor
- shell executor
- in-process workflow executor backed by a registry
- explicit workflow registration boundary with no default business handlers
- durable claim before execution
- execution receipts for stdout, stderr, HTTP response summary, and workflow result
- retry with exponential backoff

Policy

- overlap policies: `allow`, `forbid`, `queue_latest`, `replace`
- misfire policies: `skip`, `run_once_if_late`, `catch_up`
- timeout enforcement
- max concurrency per schedule

Durability and audit

- SQLite-backed durable schedules and runs
- append-only attempt history per logical occurrence
- worker leases with stale-claim recovery
- dead-letter capture for exhausted failures
- Prometheus text metrics at `/v1/metrics`
- operational status counts for due, running, failed, retry-queued, and expired-claim work

## Shutdown behavior

The service emits structured lifecycle logs during shutdown so operators can distinguish clean stops from timed-out drains:

1. `shutdown requested` - `CloseContext` or signal received
2. `draining: waiting for run loop to stop` - scheduler and dispatcher ticker loops stopping
3. `run loop stopped` - planner and dispatcher loops exited
4. `dispatcher shutdown: cancelling active executions` - active work contexts cancelled, with count
5. `dispatcher shutdown complete` or `dispatcher shutdown timeout` - drain outcome
6. `shutdown complete` - store closed, process can exit

If an executor delays or ignores cancellation, `CloseContext` returns a deadline error and the store remains open rather than closing underneath the active run. Runs that were active at timeout retain their `running` status in the ledger.

## Current implementation notes

- The daemon is single-process and SQLite-first. It is designed to be Postgres-ready at the storage boundary, but Postgres is not implemented yet.
- Workflow targets are in-process only in v1. Handlers must be registered explicitly by the embedding application or tests.
- `replace` overlap is best-effort cooperative cancellation. If the active execution cannot be interrupted cleanly, behavior degrades toward `queue_latest`.
- Expired `claimed` runs are returned to `pending`; expired `running` runs are marked failed and retried or dead-lettered according to retry policy.
- There is no auth, UI, distributed coordination, or plugin loading in the current implementation.

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

## CLI

```text
wakeplane serve
wakeplane schedule create -f ./examples/nightly-sync.yaml
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

## HTTP API

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

## Runtime configuration

The daemon reads configuration from environment variables:

- `WAKEPLANE_HTTP_ADDR` default `:8080`
- `WAKEPLANE_DB_PATH` default `./wakeplane.db`
- `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS` default `5`
- `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` default `2`
- `WAKEPLANE_LEASE_TTL_SECONDS` default `30`
- `WAKEPLANE_WORKER_ID` default `wrk_local`

## Development

Runtime and build execution state in this repo is tracked through `small`.

- Use `small plan`, `small checkpoint`, `small handoff`, and `small check --strict` for agent-owned state.
- Use `small draft` and `small accept` for human-owned `.small` artifacts.
- Use `small apply --task ... --cmd ...` for build, test, and verification commands.
