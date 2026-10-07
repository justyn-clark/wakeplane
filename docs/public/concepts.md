# Concepts

Wakeplane is a scheduling control plane, not a thin cron wrapper. Understanding its model makes the API, policies, and durability guarantees predictable.

This page describes the published `v0.3.0-beta.2` beta. See [Install](install.md) for downloads and legacy limitations.

## The problem

Cron expresses cadence. It does not:

- record that a run was due
- track whether it succeeded, failed, or was skipped
- handle missed runs across restarts
- enforce concurrency limits or timeouts
- retry on failure
- preserve an audit trail of what ran and what happened

Wakeplane is the layer above cron. It decides what is due, records each occurrence as a durable database row, dispatches typed execution, and keeps an append-only ledger of what happened.

## Core components

### Planner

The planner loop ticks at a configurable interval (default: every 5 seconds). On each tick it:

1. Loads all enabled, non-paused schedules.
2. Computes the next occurrence for any schedule where `next_run_at` is in the past.
3. Materializes a `Run` record in the database with status `pending`.
4. Updates `next_run_at` on the schedule.

The planner does not execute work. It only materializes the intent.

### Dispatcher

The dispatcher loop ticks at a configurable interval (default: every 2 seconds). On each tick it:

1. Queries for pending runs that are ready to dispatch (respecting overlap and concurrency policy).
2. Claims each eligible run - atomically transitioning it from `pending` to `claimed` and inserting a worker lease.
3. Starts execution in a goroutine.
4. Renews the worker lease at half the TTL until execution finishes.
5. Records the result as `succeeded`, `failed`, `cancelled`, or `dead_lettered`.

The dispatcher checks expired leases on each tick. Expired claims return to `pending`; ordinary running attempts fail or are dead-lettered according to retry policy. Tracked remote jobs are requeued to resume observation of their persisted identity or submission intent, keeping the original target and deadline.

### Occurrence key

Every run has an `occurrence_key` that encodes its identity. Scheduled occurrences use the form:

```
{schedule_id}:{nominal_time_rfc3339}
```

For example: `sch_01HZ123ABC:2026-04-01T09:00:00Z`

Manual triggers use `manual:{run_id}` instead. Events use `event:<sha256>` derived from schedule ID, source, and event ID. The database enforces uniqueness of the pair `(occurrence_key, attempt)`; a retry is a new run record with the same occurrence key and an incremented attempt number.

### Run

A run is a durable record of one execution attempt for one occurrence. It tracks:

- `status` - current state in the run state machine
- `attempt` - which attempt this is (starts at `1`)
- `claimed_by_worker_id`, `claim_expires_at`, `started_at`, `finished_at` - execution timing and lease ownership
- `error_text` - set on failure or recovery
- `result_json` - executor-specific result data

Retries preserve the prior attempt's terminal outcome (`succeeded`, `failed`, `dead_lettered`, `cancelled`, or `skipped`). Remote-job reconciliation may update the remote checkpoint and append receipts while preserving the local run outcome.

### Attempt

Each retry creates a new run record with `attempt = previous + 1`. The prior run stays as `failed`. Both records share the same `occurrence_key`. This gives a full history of every attempt for a given logical occurrence.

### Lease

When the dispatcher claims a run, it inserts a worker lease with an `expires_at` timestamp. The dispatcher renews the lease heartbeat while the run is executing.

If the process crashes, the lease is never renewed. On the next startup, the dispatcher detects expired leases:

- `claimed` run with expired lease -> reset to `pending`
- Ordinary `running` run with expired lease -> mark `failed` and create a retry when allowed, otherwise `dead_lettered`
- Tracked remote `running` run with expired lease -> return to `pending` and resume tracking the same occurrence

An exact lease token fences normal worker completion and checkpoint writes from an owner whose claim has been replaced. The remaining ordinary crash-recovery retry window is documented in [Run States](run-states.md).

### Receipt

When an executor finishes, it writes a receipt. Receipts are execution artifacts attached to a run:

- **shell executor**: `stdout`, `stderr`, exit code
- **Plain HTTP executor**: response status line and content type
- **Tracked HTTP job**: bounded submission, status, and reconciliation evidence
- **workflow executor**: the result map returned by the handler

Receipts are append-only. They are available via `GET /v1/runs/{id}/receipts`.

### Dead letter

When a run exhausts all retry attempts, it is dead-lettered. A `dead_letters` record captures the occurrence key, reason, and payload. Dead letters are visible in `GET /v1/status` and the metrics endpoint.

A dead letter means: "this occurrence failed completely and will not be retried automatically." It requires manual investigation.

### Schedule target

A target defines what to execute when a run is dispatched. Wakeplane supports three target kinds:

- `http` - make an HTTP request to a URL
- `shell` - run a command with arguments
- `workflow` - call a registered in-process handler by ID

An HTTP target with `http_job` tracks remote identity, progress, outputs, and terminal status. An accepted submission does not mean the work succeeded. Timeout does not cancel the remote job, and unresolved remote work continues reserving overlap capacity. See [Automation](automation.md).

### Manual trigger

Triggering a schedule with `POST /v1/schedules/{id}/trigger` creates a run immediately without affecting the schedule's normal cadence. The trigger requires a `reason` string. The run gets a `manual:{run_id}` occurrence key and proceeds through the same dispatch path as any other run.

### Event delivery

`POST /v1/schedules/{id}/events` creates one occurrence for a stable source/event identity. Identical redelivery returns the original run; changed data conflicts. New events require an enabled schedule and still follow execution policy. Deduplication survives run pruning and remains until schedule deletion. Event adapters must validate provider signatures before calling this authenticated API.

## What Wakeplane is not

- It is not a DAG orchestrator. There are no step dependencies, fan-out/fan-in primitives, or workflow graphs.
- It is not a distributed job queue. Scheduling and dispatch run in one daemon against a SQLite or Postgres backend; a separate runner may execute tracked remote work.
- It is not Temporal or Airflow. It is a simpler, embeddable primitive.
- It is not a workflow IDE or no-code tool.

## Current beta source scope

Wakeplane supports single-operator bearer auth for `/v1/...` and serves a local operator console at `/console/`, but it has no RBAC, no multi-tenancy, and no distributed coordination. See [Security](security.md) for binding guidance.
