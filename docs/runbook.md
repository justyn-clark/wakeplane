# Operator Runbook

Wakeplane currently runs as a single-process, SQLite-first daemon. Treat the database file as the operational source of truth and keep one writer per file.

## Startup

```
WAKEPLANE_DB_PATH=/var/lib/wakeplane/data.db \
WAKEPLANE_HTTP_ADDR=:8080 \
WAKEPLANE_WORKER_ID=wrk_prod_01 \
wakeplane serve
```

Verify startup:

```
curl http://localhost:8080/healthz    # {"ok":true}
curl http://localhost:8080/readyz     # {"ok":true,"storage":"ok"}
```

## Health Checks

| Endpoint       | Purpose               | Probe Type |
| -------------- | --------------------- | ---------- |
| `GET /healthz` | Process is alive      | Liveness   |
| `GET /readyz`  | Database is reachable | Readiness  |

If readiness fails (`"storage":"error"`), check SQLite file permissions and disk space.

## Shutdown

Send `SIGINT` or `SIGTERM`. The daemon logs a structured shutdown sequence:

```
{"level":"INFO","msg":"signal received, shutting down HTTP server"}
{"level":"INFO","msg":"shutdown requested"}
{"level":"INFO","msg":"draining: waiting for run loop to stop"}
{"level":"INFO","msg":"run loop stopped"}
{"level":"INFO","msg":"draining: shutting down dispatcher"}
{"level":"INFO","msg":"dispatcher shutdown: cancelling active executions","count":N}
{"level":"INFO","msg":"dispatcher shutdown complete"}
{"level":"INFO","msg":"shutdown complete"}
{"level":"INFO","msg":"serve stopped cleanly"}
```

**If shutdown stalls**, you will see:

```
{"level":"WARN","msg":"shutdown timeout: run loop did not stop in time"}
{"level":"WARN","msg":"shutdown timeout: dispatcher drain exceeded deadline","remaining":N}
```

This means the process did not drain cleanly within the shutdown deadline. The store is left open until the shutdown path finishes, active executions retain their `running` status, and on next startup expired leases trigger recovery (`claimed` -> `pending`, `running` -> `failed` with retry or dead-letter handling per policy).

## Metrics

Scrape `GET /v1/metrics` (Prometheus text format).

Key metrics to alert on:

| Metric                      | Alert Condition   | Meaning                                            |
| --------------------------- | ----------------- | -------------------------------------------------- |
| `runs_failed_total`         | Increasing        | Executions failing                                 |
| `dead_letters_total`        | > 0               | Runs exhausted all retries                         |
| `claimed_but_expired_total` | > 0               | Workers dying mid-execution or lease TTL too short |
| `runs_due`                  | Growing over time | Dispatcher not keeping up                          |
| `runs_retry_queued`         | Growing over time | Retries accumulating                               |

## Status

`GET /v1/status` returns operational state:

```json
{
  "service": "wakeplane",
  "version": "0.1.0",
  "started_at": "2026-03-19T00:00:00Z",
  "database": {
    "driver": "sqlite",
    "path": "/var/lib/wakeplane/data.db"
  },
  "scheduler": {
    "loop_interval_seconds": 5,
    "last_tick_at": "2026-03-19T00:00:05Z",
    "due_runs": 0,
    "next_due_schedule_id": "sch_...",
    "next_due_run_at": "2026-03-19T00:05:00Z"
  },
  "workers": {
    "active": 0,
    "claimed_but_expired": 0
  },
  "runs": {
    "running": 0,
    "failed": 0,
    "retry_queued": 0,
    "dead_letter": 0
  }
}
```

## Common Failures

### Runs stuck in "running"

**Cause:** Executor did not finish before the process was killed.
**Recovery:** Automatic on next startup. The dispatcher detects expired leases and marks running runs as failed, then retries or dead-letters them per schedule policy.
**Action:** No manual intervention needed. Check `claimed_but_expired_total` metric.

### Runs stuck in "claimed"

**Cause:** Process crashed between claim and execution start.
**Recovery:** Automatic. Expired claimed runs are reset to pending and re-dispatched.
**Action:** No manual intervention needed.

### Dead letters accumulating

**Cause:** Runs failing repeatedly and exhausting retry attempts.
**Action:** Inspect the schedule's target configuration. Check executor logs. Review the dead letter payloads via the API or database.

### "due_runs" count growing

**Cause:** Dispatcher is blocked or overlap policy is preventing dispatch (e.g., `forbid` with a long-running active execution).
**Action:** Check `runs_running` and `workers.active`. If an active run is stuck, it will time out per `policy.timeout_seconds` or be recovered via lease expiry.

### Database locked errors

**Cause:** SQLite single-writer contention. Should not occur with `SetMaxOpenConns(1)` unless external tools are accessing the file.
**Action:** Ensure no other process is writing to the SQLite file. Check for stale WAL/SHM files.

### Schedule not firing

**Cause:** Schedule is paused, or `next_run_at` is in the future, or misfire policy skipped overdue runs.
**Action:**

```
wakeplane schedule get <id>   # Check enabled, paused_at, next_run_at
wakeplane run list             # Check for skipped runs
```

## Backup

The SQLite database is a single file. Back it up with:

```
sqlite3 /var/lib/wakeplane/data.db ".backup /backups/wakeplane-$(date +%Y%m%d).db"
```

Do not copy the file directly while the daemon is running - use SQLite's backup API or stop the daemon first.

For Postgres installs, use the platform-native backup path, for example:

```
pg_dump "$WAKEPLANE_DATABASE_URL" > "wakeplane-$(date +%Y%m%d).sql"
```

## Restore

For SQLite, restore the backup file into a fresh path and start Wakeplane against it:

```
cp /backups/wakeplane-20260527.db /var/lib/wakeplane/restored.db
WAKEPLANE_STORE=sqlite WAKEPLANE_DB_PATH=/var/lib/wakeplane/restored.db wakeplane serve
wakeplane status
```

For Postgres, restore into a fresh database and point Wakeplane at that URL:

```
createdb wakeplane_restored
psql "$WAKEPLANE_DATABASE_URL_RESTORED" < wakeplane-20260527.sql
WAKEPLANE_STORE=postgres WAKEPLANE_DATABASE_URL="$WAKEPLANE_DATABASE_URL_RESTORED" wakeplane serve
wakeplane status
```

Verify schedules, run history, receipts, audit rows, retention settings, and status output before switching operators to the restored instance.

## SQLite to Postgres Schedule Bridge

For a schedule-definition migration, use the CLI export/import bridge:

```
wakeplane schedule export > schedules.json

WAKEPLANE_STORE=postgres \
WAKEPLANE_DATABASE_URL=postgres://wakeplane:secret@db.example.com:5432/wakeplane \
wakeplane serve

wakeplane schedule import --file schedules.json
wakeplane status
```

This bridge moves schedule definitions only. Use database-native backup/restore when you need run history, receipts, request audit logs, leases, or dead letters.

## Production Drills

Wakeplane ships repeatable local drill scripts that create isolated SQLite-backed daemons and write receipts under `artifacts/drills/` by default.

```
scripts/soak-drill.sh
scripts/restart-drill.sh
scripts/backup-restore-drill.sh
scripts/postgres-backup-restore-drill.sh
```

The soak drill runs representative successful and intentionally failing schedules, then records run counts, retry behavior, receipt truncation bounds, audit growth, retention settings, and process RSS samples.

The restart drill forces a daemon stop while work is running, restarts against the same database, and records whether completed work stayed completed and claimed/running work recovered through lease expiry.

The backup/restore drill backs up a SQLite database with `.backup`, restores it into a fresh path, starts Wakeplane against the restored data, and records schedule, run, receipt, audit, retention, and status evidence.

The Postgres backup/restore drill starts a temporary local Postgres cluster when Postgres binaries are available, runs Wakeplane against a source database, restores a `pg_dump` into a fresh database, and records schedule, run, receipt, audit, and status evidence.

For shorter local verification runs:

```
WAKEPLANE_SOAK_DURATION_SECONDS=5 scripts/soak-drill.sh
```

For persistent artifacts:

```
WAKEPLANE_DRILL_ARTIFACT_DIR=artifacts/drills/manual-soak scripts/soak-drill.sh
```
