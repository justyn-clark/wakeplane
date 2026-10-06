# Run States

Every execution attempt in Wakeplane is a `Run` record with an explicit status. This page defines all statuses, their transitions, and recovery semantics.

## Statuses

| Status            | Terminal? | Description                                                                    |
| ----------------- | --------- | ------------------------------------------------------------------------------ |
| `pending`         | No        | Recorded by planner, manual trigger, or event delivery; waiting for dispatcher |
| `claimed`         | No        | Dispatcher has acquired a lease; execution has not started                     |
| `running`         | No        | Executor is actively running the work                                          |
| `succeeded`       | Yes       | Execution completed without error                                              |
| `failed`          | Yes       | Execution completed with error (retry may follow via a new run)                |
| `retry_scheduled` | No        | A retry attempt has been created for this occurrence                           |
| `dead_lettered`   | Yes       | All retry attempts exhausted                                                   |
| `cancelled`       | Yes       | Execution was interrupted by shutdown or `replace` overlap policy              |
| `skipped`         | Yes       | Misfire or queue-coalescing policy skipped unsubmitted work                    |

A retry is a new run record, not a transition out of a terminal status. Remote-job reconciliation updates the remote checkpoint while preserving the original local run outcome.

## Transition diagram

```
pending -> claimed -> running -> succeeded
                         |----> cancelled
                         |----> dead_lettered (no retry or terminal failure)
                         +----> failed (retry allowed)

failed attempt remains terminal
  + new run: retry_scheduled -> claimed -> running -> ...

expired claim:          claimed -> pending
expired remote tracker: running -> pending (resume tracking)
misfire/queue policy:   unsubmitted occurrence -> skipped
```

## Transition rules

### `pending` -> `claimed`

The dispatcher atomically claims a run: verifies it is still `pending` (or `retry_scheduled` with `retry_available_at` in the past), checks overlap and concurrency policy, updates the run status to `claimed`, and inserts a worker lease.

### `claimed` -> `running`

The dispatcher sets `started_at` and transitions to `running`. The heartbeat goroutine begins renewing the lease at `ttl/2` intervals.

### `running` -> `succeeded`

The executor returned without error. `finished_at`, `result_json`, and `status=succeeded` are set. The worker lease is deleted.

### `running` -> `failed`

The executor returned an error. `finished_at`, `error_text`, and `status=failed` are set. If retry policy allows another attempt, a new run is inserted with `status=retry_scheduled` and `retry_available_at` set to a future time based on exponential backoff.

### `running` -> `cancelled`

The executor's context was cancelled (shutdown or `replace` overlap policy). `status=cancelled` is set. No retry is scheduled for cancellation.

### `failed` -> new run with `retry_scheduled`

A new run record is created with:

- Same `occurrence_key`
- `attempt = previous_attempt + 1`
- `status = retry_scheduled`
- `retry_available_at` = now + backoff delay

The original failed run stays as `failed`. The new run becomes a candidate when `retry_available_at` passes.

### `failed` -> `dead_lettered`

When `attempt >= max_attempts`, no retry is created. A `dead_letters` record is inserted capturing the occurrence key, reason, and payload. The run status is set to `dead_lettered`.

### `claimed` -> `pending` (crash recovery)

If the process crashes after claiming but before marking running, the lease eventually expires. The dispatcher's `recoverExpiredLeases` resets the run to `pending` and deletes the stale lease.

### `running` -> `failed` (crash recovery)

If the process crashes while an ordinary run is in `running` state, the lease eventually expires. Recovery records `error_text = "worker lease expired during execution"`. It marks the run `failed` and creates a retry when policy allows, or marks it `dead_lettered` when no retry is available.

This describes ordinary targets. A tracked HTTP job is requeued for observation of its persisted remote identity or submission intent. It retains its target and absolute deadline; recovery does not interpret a lost local worker as remote completion.

### `skipped`

The planner creates a run with `status=skipped` and `finished_at` set immediately when misfire policy dictates the occurrence should not execute. This preserves the audit trail - you can see what was skipped and why.

The dispatcher also skips unsubmitted pending work when `queue_latest` or `replace` coalesces a backlog. It does not discard a tracker for work already accepted remotely.

## Occurrence identity

Each run has an `occurrence_key`:

- **Scheduled:** `{schedule_id}:{nominal_time_rfc3339}` - e.g., `sch_01HZ123ABC:2026-04-01T09:00:00Z`
- **Manual:** `manual:{run_id}`
- **Event:** `event:<sha256>` derived from schedule ID, source, and event ID; identical deliveries reuse the original run

The database enforces a unique constraint on `(occurrence_key, attempt)`. This prevents duplicate execution for the same logical occurrence at the same attempt number.

## Lease semantics

- **TTL**: default 30 seconds. Configurable via `WAKEPLANE_LEASE_TTL_SECONDS`.
- **Heartbeat**: renewed at `ttl/2` by the dispatcher goroutine managing the run.
- **Expiry recovery**: runs on every dispatcher tick. Leases older than `expires_at` trigger recovery.
- **Claim expiry**: `claimed` -> `pending` (re-dispatchable)
- **Ordinary running expiry**: `running` -> `failed` plus a new retry, or `dead_lettered` if no retry is available

Tracked remote jobs use `running` -> `pending` recovery so the next owner can resume tracking. Check `external_job.status` as well as the local run status: a local timeout does not mean remote cancellation, and reconciliation preserves the original local outcome.

## Known gap

Normal leased-worker failure records the outcome and its retry or dead letter in one transaction, fenced by the exact lease token. Tracked-job lease recovery requeues the existing tracker.

Ordinary running-lease recovery still uses a separate outcome write and retry insertion. A crash between those writes can leave a failed ordinary run with no retry scheduled; recovery does not reconstruct the missing retry. This remaining window applies to both storage backends and must not be described as an exactly-once guarantee.
