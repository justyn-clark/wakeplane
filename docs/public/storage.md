# Storage

Wakeplane is SQLite-first in the current beta line. This page explains why, what it means in practice, and where the seam for future storage portability lives.

## Why SQLite first

SQLite is the right choice for the current pre-1.0 phase:

- **Zero infrastructure dependency.** The database is a single file. You do not need to provision, connect to, or manage an external database.
- **Simple deployment.** Copy the binary and the database file. That is the entire deployment.
- **Single-writer model.** SQLite is excellent for one writer. Wakeplane runs as a single process with `SetMaxOpenConns(1)`. There is no write contention.
- **Embedded migrations.** Schema migrations run automatically at startup via embedded SQL files. No migration tooling required.

## Current constraints

- One writer. Wakeplane is a single-process daemon. Distributed or multi-writer deployments are not supported in the current beta line.
- File-based. The database must be a local file accessible by the daemon process. Network file systems (NFS, EFS) are not recommended.
- No connection pooling. The single-connection model means read queries also serialize behind the writer. This is fine for single-process workloads.

## Configuration

```bash
WAKEPLANE_DB_PATH=./wakeplane.db   # path to SQLite file
```

The database is created automatically on first startup. Migrations run on every startup and are idempotent.

## Backup

The database is a single file. Back it up with SQLite's backup API:

```bash
sqlite3 /var/lib/wakeplane/data.db ".backup /backups/wakeplane-$(date +%Y%m%d).db"
```

Do not copy the file while the daemon is running. Use the SQLite backup API or stop the daemon first.

## What is stored

- **Schedules**: name, enabled, timezone, schedule spec (cron/interval/once), target spec (HTTP/shell/workflow), policy, retry config, `next_run_at`
- **Runs**: occurrence key, attempt, status, timing (`claimed_at`, `started_at`, `finished_at`), result, error
- **Leases**: worker ID, run ID, `expires_at`
- **Dead letters**: occurrence key, reason, payload
- **Receipts**: executor output attached to a run (stdout, HTTP response, workflow result)

All timestamps are stored as UTC RFC3339 strings. IDs are application-generated ULIDs stored as TEXT — no SERIAL or AUTOINCREMENT dependency.

## What is already portable

The application logic (domain, planner, dispatcher, API, CLI) has zero dependency on storage internals. If you wanted to add a different storage backend, you would only need to change code inside `internal/store`. Everything above the store package is already portable.

Specifically portable:
- All application logic
- Schema structure (tables, foreign keys, indices, constraints are standard SQL)
- IDs (application-generated ULIDs)
- Cursor pagination (uses `ORDER BY created_at DESC, id DESC` — standard SQL)
- Transaction isolation (default levels compatible with standard databases)
- Query patterns (SELECT, INSERT, UPDATE, DELETE, JOIN, COUNT — standard SQL)

## What must change before Postgres

| Change | Effort |
|---|---|
| Driver and connection config | Small |
| Remove SQLite PRAGMAs | Trivial |
| Timestamp columns → `TIMESTAMPTZ` | Medium |
| Boolean columns → `BOOLEAN` | Small |
| JSON columns → `JSONB` | Small |
| `INSERT OR REPLACE` → `ON CONFLICT DO UPDATE` | Small |
| `julianday()` → `EXTRACT(EPOCH FROM ...)` | Small |
| Error detection → Postgres error codes | Small |
| Connection pool sizing | Trivial |
| Dialect-specific migration file | Medium |

Total estimated scope: approximately 200 lines of changes in `store.go` and one new migration file. No changes outside the store package.

## Future portability path

The recommended approach when adding Postgres support:

1. Add a `Dialect` field to the store config.
2. Branch `Open()` by dialect (driver, pool, init queries).
3. Replace time/boolean/JSON serialization helpers with dialect-aware versions.
4. Fix the three non-portable SQL queries (`INSERT OR REPLACE`, two `julianday()` calls).
5. Fix error detection for Postgres error codes.
6. Create `001_init_postgres.sql` with native types.
7. Test against a real Postgres instance.

This work is scoped and bounded. It does not touch any application logic.

## Reference docs

- [SQLite Audit](../sqlite-audit.md) — complete inventory of SQLite-specific assumptions
- [Storage Interface](../storage-interface.md) — full store method contract and dialect seam design
- [Storage Portability](../storage-portability.md) — portability summary and implementation order
