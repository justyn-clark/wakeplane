# Storage Portability

Summary of what is portable, what remains intentionally SQLite-first for local installs, and what has been verified for the Postgres production backend.

## Already Portable

- **All application logic** (domain, planner, dispatcher, API, CLI) has zero dependency on storage internals.
- **Schema structure** (tables, foreign keys, cascades, indices, constraints) is standard SQL.
- **IDs** are application-generated ULIDs stored as TEXT - no SERIAL/AUTOINCREMENT dependency.
- **Cursor pagination** uses `ORDER BY created_at DESC, id DESC` which is standard.
- **Transaction isolation** uses default levels compatible with both databases.
- **Query patterns** (SELECT, INSERT, UPDATE, DELETE, JOIN, COALESCE, COUNT, SUM) are standard SQL.

## Intentionally SQLite-First

These are deliberate choices for the v1 bootstrap:

- **Single-writer model** (`SetMaxOpenConns(1)`) - simple, avoids write contention, sufficient for single-process deployment.
- **File-based storage** - zero infrastructure dependency, embedded in process.
- **Text-encoded timestamps** - simpler than native types for a single driver, but adds parsing overhead.
- **Text-encoded booleans and JSON** - same rationale.

## Postgres Backend Status

The first Postgres slice is implemented behind the existing store boundary:

- `WAKEPLANE_STORE=sqlite|postgres`, defaulting to `sqlite`
- `WAKEPLANE_DATABASE_URL` for Postgres
- dialect-owned migrations under `internal/store/migrations/{sqlite,postgres}`
- Postgres placeholder rebinding for existing store queries
- Postgres `ON CONFLICT` lease upsert
- row-locking claim path for Postgres (`FOR UPDATE` on the schedule and run rows)

Production verification:

| Verification item                         | Status |
| ----------------------------------------- | ------ |
| Existing SQLite suite unchanged           | Done   |
| Store/app/dispatcher/CLI Postgres tests   | Done   |
| Concurrent worker duplicate-claim test    | Done   |
| Lease expiry and crash-window tests on PG | Done   |
| CLI/API parity run against both backends  | Done   |
| Disposable Postgres execution             | Done   |

See [storage-interface.md](storage-interface.md) for the recommended abstraction strategy.

## SQLite to Postgres Bridge

The current migration bridge is schedule export/import:

```
wakeplane schedule export > schedules.json
wakeplane schedule import --file schedules.json
```

`schedule export` emits full schedule definitions in an import-compatible manifest. It does not migrate historical runs, receipts, request audit logs, worker leases, or dead letters. Use SQLite/Postgres native backup and restore commands when preserving full history is required.

## Verification Commands

1. Run `scripts/test-postgres-store.sh` against local Postgres binaries, Docker, or an externally supplied `WAKEPLANE_POSTGRES_TEST_URL`.
2. Run `scripts/soak-drill.sh`, `scripts/restart-drill.sh`, `scripts/backup-restore-drill.sh`, and `scripts/postgres-backup-restore-drill.sh`.
3. Keep the emitted drill receipts under `artifacts/drills/` for operator review.
