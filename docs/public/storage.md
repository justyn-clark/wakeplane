# Storage

Wakeplane supports SQLite and Postgres backends. SQLite remains the default; Postgres is selected by configuration when an operator wants an external database and its operational tooling.

## Why SQLite first

SQLite is the right choice for the current pre-1.0 phase:

- **Zero infrastructure dependency.** The database is a single file. You do not need to provision, connect to, or manage an external database.
- **Simple deployment.** Copy the binary and the database file. That is the entire deployment.
- **Serialized local access.** Wakeplane uses `SetMaxOpenConns(1)` for SQLite, serializing database operations within one daemon. This does not provide coordination between multiple daemon processes.
- **Embedded migrations.** Schema migrations run automatically at startup via embedded SQL files. No migration tooling required.

## Current constraints

- SQLite local mode is one writer. Wakeplane is still a single-process daemon; distributed worker deployments are not part of this phase.
- SQLite is file-based. The database must be a local file accessible by the daemon process. Network file systems (NFS, EFS) are not recommended.
- Postgres mode uses a connection pool and row locks for run claiming. Store, application, dispatcher, and CLI parity tests run against real disposable Postgres databases, including Postgres 16 in hosted CI. This verification does not establish distributed-worker support or a 1.0 stability guarantee.

## Configuration

```bash
WAKEPLANE_STORE=sqlite              # default
WAKEPLANE_DB_PATH=./wakeplane.db   # path to SQLite file
```

For Postgres:

```bash
WAKEPLANE_STORE=postgres
WAKEPLANE_DATABASE_URL=postgres://wakeplane:secret@db.example.com:5432/wakeplane
```

Migrations are dialect-owned under `internal/store/migrations/{sqlite,postgres}` and run on startup. Keep SQLite for local trusted installs; use Postgres when you need an external production database, backups, and operational database tooling.

## Backup

The database is a single file. Back it up with SQLite's backup API:

```bash
sqlite3 /var/lib/wakeplane/data.db ".backup /backups/wakeplane-$(date +%Y%m%d).db"
```

Do not copy the file while the daemon is running. Use the SQLite backup API or stop the daemon first.

For Postgres:

```bash
pg_dump "$WAKEPLANE_DATABASE_URL" > "wakeplane-$(date +%Y%m%d).sql"
```

## SQLite to Postgres schedule bridge

The supported bridge in this phase is schedule export/import:

```bash
# Against the SQLite-backed daemon.
wakeplane schedule export > schedules.json

# Start a fresh Postgres-backed daemon, then import.
WAKEPLANE_STORE=postgres \
WAKEPLANE_DATABASE_URL=postgres://wakeplane:secret@db.example.com:5432/wakeplane \
wakeplane serve

wakeplane schedule import --file schedules.json
wakeplane status
```

`schedule export` emits an import-compatible manifest with schedule definitions. It does not move run history, execution receipts, request audit rows, worker leases, dead letters, external-job checkpoints, or event deduplication records. Import creates new schedule identities. Preserve history and replay protection with database-native backups when you need a full restore.

## What is stored

- **Schedules**: name, enabled, timezone, schedule spec (cron/interval/once), target spec (HTTP/shell/workflow), policy, retry config, `next_run_at`
- **Runs**: occurrence key, attempt, status, lease ownership (`claimed_by_worker_id`, `claim_expires_at`), timing (`started_at`, `finished_at`), result, error
- **Leases**: worker ID, run ID, `expires_at`
- **Dead letters**: occurrence key, reason, payload
- **Receipts**: executor output attached to a run (stdout, HTTP response, workflow result)
- **Request audit**: control-plane request metadata when auditing is enabled
- **External jobs**: occurrence identity, immutable target snapshot, deadline, remote status, progress, and outputs
- **Trigger events**: source/event identity, payload, and original run identity for delivery deduplication

Terminal-run retention preserves unresolved remote jobs for recovery. Confirmed terminal checkpoints are compacted after their attempts are pruned. Event deduplication records survive run pruning and remain until their schedule is deleted. See [Automation](automation.md) for the recovery and retention contracts.

All timestamps are stored as UTC RFC3339 strings. IDs are application-generated ULIDs stored as TEXT - no SERIAL or AUTOINCREMENT dependency.

## What is already portable

SQL dialect handling lives in `internal/store`. The application, planner, dispatcher, API, and CLI call its method contract rather than issuing dialect-specific SQL. A new backend must preserve those transaction, claim, and retention semantics and pass the parity tests.

Specifically portable:

- All application logic
- Schema structure (tables, foreign keys, indices, constraints are standard SQL)
- IDs (application-generated ULIDs)
- Cursor pagination (uses `ORDER BY created_at DESC, id DESC` - standard SQL)
- Transaction isolation (default levels compatible with standard databases)
- Query patterns (SELECT, INSERT, UPDATE, DELETE, JOIN, COUNT - standard SQL)

## Postgres Hardening Status

| Verification item                                   | Status |
| --------------------------------------------------- | ------ |
| Driver and connection config                        | Done   |
| Dialect-owned migrations                            | Done   |
| Postgres placeholder binding and lease upsert       | Done   |
| Postgres row-lock claim path                        | Done   |
| Store/app/dispatcher/CLI Postgres test suite        | Done   |
| Disposable Postgres execution                       | Done   |
| Native Postgres timestamp/boolean/JSON column types | Later  |

## Verification path

The recommended verification path for Postgres changes:

1. Run `scripts/test-postgres-store.sh` against local Postgres binaries, Docker, or an externally supplied `WAKEPLANE_POSTGRES_TEST_URL`.
2. Fix any store/app/dispatcher/CLI parity failures exposed by real Postgres.
3. Run the soak, restart-recovery, and backup/restore drills after backend changes.
4. Move to native Postgres timestamp/boolean/JSON column types only behind an explicit migration.

This work stays behind the store boundary. Scheduler, dispatcher, run ledger, policy, and operator surfaces remain separate.

## Reference docs

- [SQLite Audit](../sqlite-audit.md) - complete inventory of SQLite-specific assumptions
- [Storage Interface](../storage-interface.md) - full store method contract and dialect seam design
- [Storage Portability](../storage-portability.md) - portability summary and implementation order
