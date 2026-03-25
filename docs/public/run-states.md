# Run States

Every execution attempt in Wakeplane is a `Run` record with an explicit status. This page defines all statuses, their transitions, and recovery semantics.

## Statuses

| Status | Terminal? | Description |
|---|---|---|
| `pending` | No | Materialized by planner, waiting for dispatcher |
| `claimed` | No | Dispatcher has acquired a lease; execution has not started |
| `running` | No | Executor is actively running the work |
| `succeeded` | Yes | Execution completed without error |
| `failed` | Yes | Execution completed with error (retry may follow via a new run) |
| `retry_scheduled` | No | A retry attempt has been created for this occurrence |
| `dead_lettered` | Yes | All retry attempts exhausted |
| `cancelled` | Yes | Execution was interrupted by shutdown or `replace` overlap policy |
| `skipped` | Yes | Planner skipped this occurrence due to misfire policy |

Terminal statuses are never updated after they are set.

## Transition diagram

```
                    ┌────────────────────────────┐
                    │         PLANNER             │
                    │  occurrence becomes due     │
                    └────────────┬────────────────┘
                                 │
                          ┌──────▼──────┐
                          │   pending   │◄── recovered from expired claim
                          └──────┬──────┘
                                 │ dispatcher claims
                          ┌──────▼──────┐
                          │   claimed   │
                          └──────┬──────┘
                                 │ mark running
                          ┌──────▼──────┐
                          │   running   │
                          └──┬──┬──┬──┬─┘
               success ─────┘  │  │  └─── ctx cancelled
                               │  │
                      failure ─┘  └── lease expired (crash recovery)
                                              │
┌───────────┐  ┌─────────┐  ┌───────────┐   │
│ succeeded │  │ failed  │  │ cancelled │   │ mark failed
└───────────┘  └────┬────┘  └───────────┘   └──► failed
                    │
              retry available?
               ┌────┴────┐
           yes │         │ no
       ┌───────▼───────┐  │
       │retry_scheduled│  │
       └───────┬───────┘  │
               │          │
      new pending run      │
      (next attempt)       ▼
                    ┌──────────────┐
                    │ dead_lettered│
                    └──────────────┘

┌─────────┐
│ skipped │  (misfire policy — never dispatched)
└─────────┘
```

## Transition rules

### `pending` → `claimed`

The dispatcher atomically claims a run: verifies it is still `pending` (or `retry_scheduled` with `retry_available_at` in the past), checks overlap and concurrency policy, updates the run status to `claimed`, and inserts a worker lease.

### `claimed` → `running`

The dispatcher sets `started_at` and transitions to `running`. The heartbeat goroutine begins renewing the lease at `ttl/2` intervals.

### `running` → `succeeded`

The executor returned without error. `finished_at`, `result_json`, and `status=succeeded` are set. The worker lease is deleted.

### `running` → `failed`

The executor returned an error. `finished_at`, `error_text`, and `status=failed` are set. If retry policy allows another attempt, a new run is inserted with `status=retry_scheduled` and `retry_available_at` set to a future time based on exponential backoff.

### `running` → `cancelled`

The executor's context was cancelled (shutdown or `replace` overlap policy). `status=cancelled` is set. No retry is scheduled for cancellation.

### `failed` → new run with `retry_scheduled`

A new run record is created with:
- Same `occurrence_key`
- `attempt = previous_attempt + 1`
- `status = retry_scheduled`
- `retry_available_at` = now + backoff delay

The original failed run stays as `failed`. The new run becomes a candidate when `retry_available_at` passes.

### `failed` → `dead_lettered`

When `attempt >= max_attempts`, no retry is created. A `dead_letters` record is inserted capturing the occurrence key, reason, and payload. The run status is set to `dead_lettered`.

### `claimed` → `pending` (crash recovery)

If the process crashes after claiming but before marking running, the lease eventually expires. The dispatcher's `recoverExpiredLeases` resets the run to `pending` and deletes the stale lease.

### `running` → `failed` (crash recovery)

If the process crashes while a run is in `running` state, the lease eventually expires. Recovery marks the run as `failed` with `error_text = "worker lease expired during execution"` and schedules a retry if policy allows.

### `skipped` (planner only)

The planner creates a run with `status=skipped` and `finished_at` set immediately when misfire policy dictates the occurrence should not execute. This preserves the audit trail — you can see what was skipped and why.

## Occurrence identity

Each run has an `occurrence_key`:

- **Scheduled:** `{schedule_id}:{nominal_time_rfc3339}` — e.g., `sch_01HZ123ABC:2026-04-01T02:00:00-07:00`
- **Manual:** `manual:{run_id}`

The database enforces a unique constraint on `(occurrence_key, attempt)`. This prevents duplicate execution for the same logical occurrence at the same attempt number.

## Lease semantics

- **TTL**: default 30 seconds. Configurable via `WAKEPLANE_LEASE_TTL_SECONDS`.
- **Heartbeat**: renewed at `ttl/2` by the dispatcher goroutine managing the run.
- **Expiry recovery**: runs on every dispatcher tick. Leases older than `expires_at` trigger recovery.
- **Claim expiry**: `claimed` → `pending` (re-dispatchable)
- **Running expiry**: `running` → `failed` → retry per policy

## Known gap

`FinishRun` and the retry `InsertRun` are not in a single database transaction. If the process is killed between these two writes, the retry is lost. The original run is `failed` with no retry scheduled. This is a known limitation of the current alpha. In practice the window is extremely small (two sequential SQLite writes).
