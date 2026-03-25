# Operator Runbook

Operational reference for running Wakeplane in production or staging environments.

> **Alpha notice:** Wakeplane has no authentication. It must be bound to a trusted network. See [Security](security.md) before deploying.

## Startup

```bash
WAKEPLANE_DB_PATH=/var/lib/wakeplane/data.db \
WAKEPLANE_HTTP_ADDR=:8080 \
WAKEPLANE_WORKER_ID=wrk_prod_01 \
wakeplane serve
```

Verify startup:

```bash
curl http://localhost:8080/healthz   # {"ok":true}
curl http://localhost:8080/readyz    # {"ok":true,"storage":"ok"}
```

If readiness fails (`"storage":"error"`), check SQLite file permissions and disk space.

## Health endpoints

| Endpoint | Purpose | Probe type |
|---|---|---|
| `GET /healthz` | Process is alive | Liveness |
| `GET /readyz` | Database is reachable | Readiness |

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

| Metric | Alert condition | Meaning |
|---|---|---|
| `runs_failed_total` | Increasing | Executions failing |
| `dead_letters_total` | > 0 | Runs exhausted all retries |
| `claimed_but_expired_total` | > 0 | Workers dying mid-execution or lease TTL too short |
| `runs_due` | Growing over time | Dispatcher not keeping up |
| `runs_retry_queued` | Growing over time | Retries accumulating |

## Status interpretation

`GET /v1/status` returns live operational counts. Key fields:

- `scheduler.due_runs` — how many runs are currently pending dispatch. Normally near zero.
- `workers.claimed_but_expired` — leases that expired without a heartbeat. Should be zero in steady state.
- `runs.running` — currently executing runs.
- `runs.dead_letter` — exhausted failure runs requiring manual investigation.
- `runs.retry_queued` — runs waiting for their `retry_available_at` to pass.

## Common failures

### Runs stuck in `running`

**Cause:** Executor did not finish before the process was killed.

**Recovery:** Automatic on next startup. The dispatcher detects expired leases and marks running runs as `failed`, then retries or dead-letters per schedule policy.

**Action:** No manual intervention needed. Monitor `claimed_but_expired_total`.

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

## Environment reference

| Variable | Default | Description |
|---|---|---|
| `WAKEPLANE_DB_PATH` | `./wakeplane.db` | SQLite database file path |
| `WAKEPLANE_HTTP_ADDR` | `:8080` | HTTP listen address |
| `WAKEPLANE_WORKER_ID` | `wrk_local` | Worker identity string in lease records |
| `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS` | `5` | Planner loop tick interval |
| `WAKEPLANE_DISPATCHER_INTERVAL_SECONDS` | `2` | Dispatcher loop tick interval |
| `WAKEPLANE_LEASE_TTL_SECONDS` | `30` | Worker lease TTL for crash recovery |
