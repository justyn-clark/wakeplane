# Storage Interface Design

This document defines the contract that the Wakeplane storage layer must satisfy, regardless of dialect. It serves as the specification for future Postgres support.

## Current Architecture

All storage is in `internal/store/store.go` as a concrete `*Store` struct. The dispatcher, planner, and service layers call `Store` methods directly. There is no interface today — the seam is the method set on `*Store`.

## Store Method Contract

These are the operations that any storage backend must implement:

### Lifecycle

| Method               | Contract                                                            |
| -------------------- | ------------------------------------------------------------------- |
| `Open(path/connStr)` | Open database, configure connection pool, run dialect-specific init |
| `Close()`            | Close all connections                                               |
| `Ping(ctx)`          | Verify connectivity                                                 |
| `Migrate(ctx)`       | Apply schema migrations (dialect-specific SQL)                      |

### Schedule Operations

| Method                                       | Contract                                                          |
| -------------------------------------------- | ----------------------------------------------------------------- |
| `CreateSchedule(ctx, schedule)`              | Insert schedule. Error if name is not unique.                     |
| `GetSchedule(ctx, id)`                       | Return schedule by ID. `ErrNotFound` if missing.                  |
| `UpdateSchedule(ctx, schedule)`              | Replace all schedule fields by ID.                                |
| `DeleteSchedule(ctx, id)`                    | Delete schedule and cascade to runs/leases/receipts/dead-letters. |
| `ListSchedules(ctx, enabled, limit, cursor)` | Paginated list, ordered by `created_at DESC, id DESC`.            |
| `ListAllSchedules(ctx)`                      | Return all enabled schedules for planner tick.                    |
| `ScheduleEnabledCount(ctx)`                  | Count of enabled schedules.                                       |

### Run Operations

| Method                                             | Contract                                                                                              |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `InsertRun(ctx, run)`                              | Insert run. `ErrAlreadyExists` if `(occurrence_key, attempt)` exists.                                 |
| `GetRun(ctx, id)`                                  | Return run by ID. `ErrNotFound` if missing.                                                           |
| `ListRuns(ctx, scheduleID, status, limit, cursor)` | Paginated list, ordered by `created_at DESC, id DESC`.                                                |
| `ListCandidateRuns(ctx, now, limit)`               | Runs with `status=pending AND due_time<=now` OR `status=retry_scheduled AND retry_available_at<=now`. |
| `FinishRun(ctx, run)`                              | Update run with terminal status, result fields, timestamps. Delete lease if finished.                 |
| `MarkRunRunning(ctx, runID, now)`                  | Set `status=running`, `started_at=COALESCE(started_at, now)`.                                         |
| `UpdateScheduleRuntime(ctx, ...)`                  | Update `next_run_at`, `last_run_at`, `enabled`, `paused_at`.                                          |

### Claim and Lease Operations

| Method                                               | Contract                                                                                                                                                                          |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ClaimRun(ctx, schedule, runID, workerID, now, ttl)` | **Transactional.** Verify run is pending/retry_scheduled, check concurrency, update to claimed, insert lease. Return `(true, nil)` if claimed, `(false, nil)` if already claimed. |
| `RenewLease(ctx, runID, workerID, now, ttl)`         | Extend lease `expires_at`. `ErrNotFound` if lease missing.                                                                                                                        |
| `ResetClaimedRun(ctx, runID, now)`                   | Set claimed run back to pending, clear worker/lease fields, delete lease.                                                                                                         |
| `ClearLease(ctx, runID)`                             | Delete lease by run ID.                                                                                                                                                           |
| `ListExpiredLeases(ctx, now)`                        | Return runs with leases where `expires_at < now`, joined with schedule.                                                                                                           |

### Overlap Policy Operations

| Method                                       | Contract                                           |
| -------------------------------------------- | -------------------------------------------------- |
| `ActiveRunCount(ctx, scheduleID)`            | Count of `claimed` or `running` runs for schedule. |
| `ListActiveRuns(ctx, scheduleID)`            | Full run records for active runs.                  |
| `ListPendingRunsBySchedule(ctx, scheduleID)` | Pending runs ordered by `due_time ASC`.            |

### Metrics and Counts

| Method                                   | Contract                                             |
| ---------------------------------------- | ---------------------------------------------------- |
| `CountTable(ctx, table)`                 | Total row count.                                     |
| `CountStatus(ctx, table, column, value)` | Count where column equals value.                     |
| `DueRunCount(ctx, now)`                  | Pending runs with `due_time <= now`.                 |
| `RetryQueuedCount(ctx)`                  | Runs with `status=retry_scheduled`.                  |
| `ClaimedExpiredCount(ctx, now)`          | Claimed runs with `claim_expires_at < now`.          |
| `WorkerLeaseCount(ctx)`                  | Active worker leases.                                |
| `NextDueSchedule(ctx, now)`              | Schedule with earliest due pending run.              |
| `ExecutorOutcomeCounts(ctx)`             | Run counts grouped by target kind and status.        |
| `ExecutorDurationStats(ctx)`             | Sum and count of execution durations by target kind. |

### Receipt and Dead Letter Operations

| Method                              | Contract                   |
| ----------------------------------- | -------------------------- |
| `InsertReceipt(ctx, receipt)`       | Insert execution receipt.  |
| `ListReceipts(ctx, runID)`          | Receipts for a run.        |
| `InsertDeadLetter(ctx, deadLetter)` | Insert dead letter record. |

## Dialect-Specific Seams

When introducing Postgres support, these are the narrowest points to abstract:

1. **`Open()`** — driver name, connection string, pragma vs server config.
2. **`Migrate()`** — different SQL files per dialect.
3. **Serialization helpers** — `timeString`, `parseNullTime`, `boolToInt`, `mustJSONString`, `rawJSON` are only needed for SQLite. Postgres native types eliminate them.
4. **Upsert syntax** — `INSERT OR REPLACE` (SQLite) vs `ON CONFLICT DO UPDATE` (Postgres).
5. **Date arithmetic** — `julianday()` (SQLite) vs `EXTRACT(EPOCH FROM ...)` (Postgres).
6. **Error detection** — `isUniqueErr()` string matching vs Postgres error code `23505`.

## Recommended Abstraction Strategy

Do **not** create a full Go interface prematurely. Instead:

1. Add a `Dialect` config field (`sqlite` or `postgres`) to `Store`.
2. Branch dialect-specific behavior inside the existing `Store` methods (the total surface is ~6 helpers and 2 SQL queries).
3. Use separate migration files per dialect: `001_init_sqlite.sql`, `001_init_postgres.sql`.
4. Keep a single `Store` struct — the method signatures do not change.

This keeps the codebase simple and avoids an interface that maps 1:1 to a single implementation.
