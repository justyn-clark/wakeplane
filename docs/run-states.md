# Run State Transition Model

Every execution attempt in Wakeplane is tracked as a `Run` record with an explicit status. This document defines all statuses, their transitions, and recovery semantics.

## Statuses

| Status | Terminal? | Description |
|---|---|---|
| `pending` | No | Materialized by planner, waiting for dispatcher to claim |
| `claimed` | No | Dispatcher has acquired a lease, execution has not started |
| `running` | No | Executor is actively running the work |
| `succeeded` | Yes | Execution completed without error |
| `failed` | Yes | Execution completed with error (retry may follow) |
| `retry_scheduled` | No | A retry attempt has been created for this occurrence |
| `dead_lettered` | Yes | All retry attempts exhausted |
| `cancelled` | Yes | Execution was interrupted by shutdown or policy |
| `skipped` | Yes | Planner skipped this occurrence due to misfire policy |

## Transition Diagram

```
                          ┌──────────────────────────────────┐
                          │         PLANNER                  │
                          │                                  │
                          │   due occurrence materialized    │
                          └──────────┬───────────────────────┘
                                     │
                              ┌──────▼──────┐
                              │   pending   │◄──── recovered from expired claim
                              └──────┬──────┘
                                     │ dispatcher claims
                              ┌──────▼──────┐
                              │   claimed   │
                              └──────┬──────┘
                                     │ mark running
                              ┌──────▼──────┐
                              │   running   │
                              └──┬──┬──┬──┬─┘
                     success ───┘  │  │  └─── ctx cancelled
                                   │  │
                          failure ─┘  └─ lease expired
                                          (recovery)
    ┌───────────┐  ┌─────────┐  ┌───────────┐  ┌───────────────┐
    │ succeeded │  │ failed  │  │ cancelled │  │retry_scheduled│
    └───────────┘  └────┬────┘  └───────────┘  └───────┬───────┘
                        │                              │
                  retry available?              new pending run
                   ┌────┴────┐                  (next attempt)
                   │         │
            ┌──────▼──────┐  │
            │retry_sched. │  │
            └─────────────┘  │
                             │ no retries left
                      ┌──────▼───────┐
                      │ dead_lettered│
                      └──────────────┘

    ┌─────────┐
    │ skipped │  (misfire policy, never dispatched)
    └─────────┘
```

## Transition Rules

### pending → claimed

Occurs in `store.ClaimRun`. This is the only transactional state change: it atomically verifies the run is still `pending` (or `retry_scheduled`), checks overlap/concurrency policy, updates the run status, and inserts a worker lease.

### claimed → running

Occurs in `store.MarkRunRunning`. Sets `started_at` and transitions to `running`. The heartbeat goroutine begins renewing the lease at `ttl/2` intervals.

### running → succeeded

The executor returned without error. `store.FinishRun` sets `finished_at`, `result_json`, and `status = succeeded`. The worker lease is deleted.

### running → failed

The executor returned an error. `store.FinishRun` sets `finished_at`, `error_text`, and `status = failed`. If retry policy allows, a new run is inserted with `status = retry_scheduled` and `retry_available_at` set to a future time based on exponential backoff.

### running → cancelled

The executor's context was cancelled (shutdown or `replace` overlap policy). `store.FinishRun` sets `status = cancelled`. No retry is scheduled for cancellation.

### failed → retry_scheduled (via new run)

When a failed run has remaining retry attempts, the dispatcher inserts a **new** run record with:
- Same `occurrence_key`
- `attempt = previous_attempt + 1`
- `status = retry_scheduled`
- `retry_available_at` = now + backoff delay

The original failed run stays as `failed`. The new run becomes a candidate when its `retry_available_at` passes.

### failed → dead_lettered

When `attempt >= max_attempts`, no retry is created. Instead, a `dead_letters` record is inserted capturing the occurrence key, reason, and payload. The run status is set to `dead_lettered`.

### claimed → pending (recovery)

If the process crashes after claiming but before marking running, the lease eventually expires. The dispatcher's `recoverExpiredLeases` resets the run to `pending` and deletes the stale lease.

### running → failed (recovery)

If the process crashes while a run is in `running` state, the lease eventually expires. Recovery marks the run as `failed` with error text `"worker lease expired during execution"` and schedules a retry if policy allows.

### skipped (planner only)

The planner creates a run with `status = skipped` and `finished_at` set immediately when misfire policy dictates that an overdue occurrence should not execute (e.g., `skip` policy, or non-latest occurrences under `run_once_if_late`).

## Occurrence Identity

Each run has an `occurrence_key` of the form `{schedule_id}:{nominal_time_rfc3339}`. Manual triggers use `manual:{run_id}` instead.

The database enforces a unique constraint on `(occurrence_key, attempt)`. This prevents duplicate execution for the same logical occurrence at the same attempt number.

## Lease Semantics

- **TTL**: Configurable (default 30s). The heartbeat goroutine renews at `ttl/2`.
- **Expiry recovery**: Runs on every dispatcher tick. Leases older than their `expires_at` are recovered.
- **Claim expiry**: `claimed` runs reset to `pending` (re-dispatchable).
- **Running expiry**: `running` runs marked `failed` and retried per policy.
