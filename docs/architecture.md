# Timekeeper Architecture

Timekeeper is built as a single-process control plane with explicit runtime boundaries:

- planner loop
- dispatcher loop
- executor registry
- SQLite-backed durable store
- HTTP JSON API
- CLI client against the daemon

The scheduler decides what is due. The dispatcher decides what can run now. Executors only perform work after a durable claim and run record exist.

## Package shape

- `internal/domain`: stable schedule, target, policy, retry, run, and API contract types
- `internal/store`: SQLite repository and embedded migrations
- `internal/timecalc`: timezone-aware next-fire calculation for cron, interval, and once schedules
- `internal/planner`: due-occurrence materialization and misfire handling
- `internal/dispatcher`: claim, lease renewal, execution dispatch, retries, and dead-lettering
- `internal/executors`: executor registry plus `http`, `shell`, and `workflow` implementations
- `internal/api`: HTTP JSON routes
- `internal/cli`: Cobra-based operator CLI
- `internal/app`: service composition and runtime wiring

## Runtime flow

1. Schedule definitions are persisted in SQLite with typed schedule and target specs encoded as JSON.
2. The planner loop computes due occurrences from `next_run_at`, schedule timezone, and policy.
3. Due logical occurrences are materialized into `schedule_runs` with deterministic occurrence keys.
4. The dispatcher claims eligible runs, creates or renews worker leases, and transitions runs to `running`.
5. Executors produce result data and receipts, after which the dispatcher records success, retry scheduling, cancellation, or dead-letter state.

## Actual v1 behavior

- The service runs as a single process with one SQLite connection writer (`SetMaxOpenConns(1)`).
- The workflow executor is an in-process registry. v1 does not load workflows dynamically or out-of-process.
- Manual triggers create `manual:<run_id>` occurrence keys outside the scheduled occurrence identity space.
- Metrics are exposed in Prometheus text format.
- The default service bootstrap registers a sample `sync.customers` workflow so the workflow path is runnable immediately.

## Current limits

- No auth, RBAC, or multi-tenant model.
- No distributed worker coordination beyond SQLite-backed claims and leases.
- No UI, DAG orchestration, or calendar/business-rule engine.
- `replace` overlap is cooperative and best-effort; if a running executor cannot be interrupted, the practical result is queued-latest behavior until the active run exits.
