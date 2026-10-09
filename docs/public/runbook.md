# Operator Runbook

Operational reference for running Wakeplane in production or staging environments.

This runbook covers the published `v0.3.0-beta.3` beta. Check [Install](install.md) before upgrading; older binaries do not provide all of these controls.

> **Operator warning:** Wakeplane supports single-operator bearer auth for `/v1/...`, but it has no RBAC or multi-tenancy. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network. Do not expose it directly to the public internet. See [Security](security.md) before deploying.

## Startup

```bash
WAKEPLANE_DB_PATH=/var/lib/wakeplane/data.db \
WAKEPLANE_HTTP_ADDR=127.0.0.1:8080 \
WAKEPLANE_WORKER_ID=wrk_prod_01 \
wakeplane serve
```

Verify startup:

```bash
curl http://localhost:8080/healthz   # {"ok":true}
curl http://localhost:8080/readyz    # {"ok":true,"storage":"ok"}
```

If readiness fails (`"storage":"error"`), check SQLite file permissions and disk space, or the configured Postgres URL, credentials, network access, and database availability.

## Health endpoints

| Endpoint       | Purpose               | Probe type |
| -------------- | --------------------- | ---------- |
| `GET /healthz` | Process is alive      | Liveness   |
| `GET /readyz`  | Database is reachable | Readiness  |

Use `/healthz` for liveness and `/readyz` for readiness in your container orchestrator or reverse proxy health check configuration.

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

If shutdown stalls, you will see timeout warnings:

```
{"level":"WARN","msg":"shutdown timeout: run loop did not stop in time"}
{"level":"WARN","msg":"shutdown timeout: dispatcher drain exceeded deadline","remaining":N}
```

A stalled shutdown means an executor did not honor context cancellation within the drain deadline. The store is left open. Active runs keep their `running` status. On next startup, expired leases trigger automatic recovery.

## Metrics

Scrape `GET /v1/metrics` (Prometheus text format).

| Metric                      | Alert condition   | Meaning                                            |
| --------------------------- | ----------------- | -------------------------------------------------- |
| `runs_failed_total`         | Increasing        | Executions failing                                 |
| `dead_letters_total`        | > 0               | Runs exhausted all retries                         |
| `claimed_but_expired_total` | > 0               | Workers dying mid-execution or lease TTL too short |
| `runs_due`                  | Growing over time | Dispatcher not keeping up                          |
| `runs_retry_queued`         | Growing over time | Retries accumulating                               |

## Status interpretation

`GET /v1/status` returns live operational counts. Key fields:

- `scheduler.due_runs` - how many runs are currently pending dispatch. Normally near zero.
- `workers.claimed_but_expired` - leases that expired without a heartbeat. Should be zero in steady state.
- `runs.running` - currently executing runs.
- `runs.dead_letter` - exhausted failure runs requiring manual investigation.
- `runs.retry_queued` - runs waiting for their `retry_available_at` to pass.

## Common failures

### Runs stuck in `running`

**Cause:** Executor did not finish before the process was killed.

**Recovery:** Automatic on next startup. The dispatcher detects expired leases and marks running runs as `failed`, then retries or dead-letters per schedule policy.

For tracked HTTP jobs, recovery instead resumes observation of the persisted remote job or submission intent. Its original target and deadline remain in force.

**Action:** Monitor `claimed_but_expired_total` and the run's receipts. If local tracking has ended but remote completion is unknown, use `POST /v1/runs/{id}/reconcile` to observe status. This does not resubmit work; a runner without a known identity or lookup capability requires runner-side investigation.

---

### Runs stuck in `claimed`

**Cause:** Process crashed between claim and execution start.

**Recovery:** Automatic. Expired claimed runs are reset to `pending` and re-dispatched.

**Action:** No manual intervention needed.

---

### Dead letters accumulating

**Cause:** Runs failing repeatedly and exhausting all retry attempts.

**Action:** Inspect the schedule's target configuration. Check executor logs. Review dead letter payloads via the API:

```bash
curl http://localhost:8080/v1/runs?status=dead_lettered
```

---

### `due_runs` count growing

**Cause:** Dispatcher is blocked, or overlap policy is preventing dispatch (e.g., `forbid` with a long-running active execution).

**Action:** Check `runs_running` and `workers.active`. If an active run is stuck, it will time out per `policy.timeout_seconds` or be recovered via lease expiry.

---

### Database locked errors

**Cause:** SQLite single-writer contention. Should not occur with `SetMaxOpenConns(1)` unless external tools are accessing the file.

**Action:** Ensure no other process is writing to the SQLite file. Check for stale WAL/SHM files (`*.db-wal`, `*.db-shm`).

---

### Schedule not firing

**Cause:** Schedule is paused, or `next_run_at` is in the future, or misfire policy skipped overdue runs.

**Action:**

```bash
wakeplane schedule get <id>   # check enabled, paused_at, next_run_at
wakeplane run list             # check for skipped runs
```

## Backup

```bash
sqlite3 /var/lib/wakeplane/data.db ".backup /backups/wakeplane-$(date +%Y%m%d).db"
```

Do not copy the file while the daemon is running. Use SQLite's backup API or stop the daemon first.

For Postgres installs, use the platform-native backup path, for example:

```bash
pg_dump "$WAKEPLANE_DATABASE_URL" > "wakeplane-$(date +%Y%m%d).sql"
```

## Restore

For SQLite, restore the backup file into a fresh path and start Wakeplane against it:

```bash
cp /backups/wakeplane-20260527.db /var/lib/wakeplane/restored.db
WAKEPLANE_STORE=sqlite WAKEPLANE_DB_PATH=/var/lib/wakeplane/restored.db wakeplane serve
wakeplane status
```

For Postgres, restore into a fresh database and point Wakeplane at that URL:

```bash
createdb wakeplane_restored
psql "$WAKEPLANE_DATABASE_URL_RESTORED" < wakeplane-20260527.sql
WAKEPLANE_STORE=postgres WAKEPLANE_DATABASE_URL="$WAKEPLANE_DATABASE_URL_RESTORED" wakeplane serve
wakeplane status
```

Verify schedules, run history, receipts, audit rows, retention settings, and status output before switching operators to the restored instance.

## SQLite to Postgres Schedule Bridge

For a schedule-definition migration, use the CLI export/import bridge:

```bash
# Export from the SQLite-backed daemon.
wakeplane schedule export > schedules.json

# Import into a fresh Postgres-backed daemon.
WAKEPLANE_STORE=postgres \
WAKEPLANE_DATABASE_URL=postgres://wakeplane:secret@db.example.com:5432/wakeplane \
wakeplane serve

wakeplane schedule import --file schedules.json
wakeplane status
```

This bridge moves schedule definitions only and creates new schedule IDs. Use database-native backup/restore when you need run history, receipts, request audit logs, leases, dead letters, external-job checkpoints, or event replay protection.

## Environment reference

| Variable                                | Default          | Description                                    |
| --------------------------------------- | ---------------- | ---------------------------------------------- |
| `WAKEPLANE_STORE`                       | `sqlite`         | Storage backend: `sqlite` or `postgres`        |
| `WAKEPLANE_DB_PATH`                     | `./wakeplane.db` | SQLite database file path                      |
| `WAKEPLANE_DATABASE_URL`                | unset            | Postgres URL when `WAKEPLANE_STORE=postgres`   |
| `WAKEPLANE_HTTP_ADDR`                   | `:8080`          | HTTP listen address                            |
| `WAKEPLANE_WORKER_ID`                   | `wrk_local`      | Worker identity string in lease records        |
| `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS`  | `5`              | Planner loop tick interval                     |
| `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` | `2`              | Dispatcher loop tick interval                  |
| `WAKEPLANE_LEASE_TTL_SECONDS`           | `30`             | Worker lease TTL for crash recovery            |
| `WAKEPLANE_RECEIPT_MAX_BYTES`           | `262144`         | Maximum stored body size per receipt           |
| `WAKEPLANE_RUN_RETENTION_DAYS`          | `0`              | Days to keep terminal runs; 0 disables pruning |
| `WAKEPLANE_AUTH_TOKEN`                  | unset            | Bearer token required for `/v1/...` when set   |
| `WAKEPLANE_REQUEST_AUDIT`               | `true`           | Record HTTP control-plane requests             |

## Auth and Audit

Set `WAKEPLANE_AUTH_TOKEN` to require `Authorization: Bearer <token>` on `/v1/...` routes. Health and readiness routes stay unauthenticated so process supervisors can check liveness.

Wakeplane records request audit rows for `/v1/...` routes when `WAKEPLANE_REQUEST_AUDIT=true`. Audit rows include method, path, status code, remote address, user agent, auth subject, and timestamp.

## Retention

Wakeplane bounds receipt body storage with `WAKEPLANE_RECEIPT_MAX_BYTES`. Oversized receipt bodies are truncated before storage and marked with a truncation note.

Set `WAKEPLANE_RUN_RETENTION_DAYS` to prune terminal runs older than the configured number of days. Pruning deletes terminal run rows and cascades to attached receipts and dead letters. Active, pending, claimed, running, and retry-queued runs are not pruned.

Unresolved remote-job attempts remain inspectable for recovery even when locally terminal. Confirmed terminal checkpoints are compacted after all their attempts are pruned. Event deduplication records survive run pruning and remain until schedule deletion. Audit rows have no automatic pruning policy. Account for those records separately when sizing storage; see [Automation](automation.md).

## Production Drills

Wakeplane ships repeatable local drill scripts that create isolated SQLite-backed daemons and write receipts under `artifacts/drills/` by default.

```bash
scripts/soak-drill.sh
scripts/restart-drill.sh
scripts/backup-restore-drill.sh
scripts/postgres-backup-restore-drill.sh
```

The soak drill records run counts, retry behavior, receipt truncation bounds, audit growth, retention settings, and process RSS samples while representative schedules run.

The restart drill forces a daemon stop while work is running, restarts against the same database, and records completed-work and lease-recovery evidence.

The backup/restore drill backs up a SQLite database with `.backup`, restores it into a fresh path, starts Wakeplane against the restored data, and records schedule, run, receipt, audit, retention, and status evidence.

The Postgres backup/restore drill starts a temporary local Postgres cluster when Postgres binaries are available, runs Wakeplane against a source database, restores a `pg_dump` into a fresh database, and records schedule, run, receipt, audit, and status evidence.

For a short local verification run:

```bash
WAKEPLANE_SOAK_DURATION_SECONDS=5 scripts/soak-drill.sh
```
