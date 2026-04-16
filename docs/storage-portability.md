# Storage Portability

Summary of what is portable, what is intentionally SQLite-first, and what must change before Postgres work begins.

## Already Portable

- **All application logic** (domain, planner, dispatcher, API, CLI) has zero dependency on storage internals.
- **Schema structure** (tables, foreign keys, cascades, indices, constraints) is standard SQL.
- **IDs** are application-generated ULIDs stored as TEXT — no SERIAL/AUTOINCREMENT dependency.
- **Cursor pagination** uses `ORDER BY created_at DESC, id DESC` which is standard.
- **Transaction isolation** uses default levels compatible with both databases.
- **Query patterns** (SELECT, INSERT, UPDATE, DELETE, JOIN, COALESCE, COUNT, SUM) are standard SQL.

## Intentionally SQLite-First

These are deliberate choices for the v1 bootstrap:

- **Single-writer model** (`SetMaxOpenConns(1)`) — simple, avoids write contention, sufficient for single-process deployment.
- **File-based storage** — zero infrastructure dependency, embedded in process.
- **Text-encoded timestamps** — simpler than native types for a single driver, but adds parsing overhead.
- **Text-encoded booleans and JSON** — same rationale.

## Must Change Before Postgres

See [sqlite-audit.md](sqlite-audit.md) for the full inventory. The critical changes are:

| Change                                 | Effort  | Files                          |
| -------------------------------------- | ------- | ------------------------------ |
| Driver and connection config           | Small   | `store.go:Open()`              |
| Remove PRAGMAs                         | Trivial | `store.go:Open()`              |
| Timestamp columns → `TIMESTAMPTZ`      | Medium  | Schema + all time helpers      |
| Boolean columns → `BOOLEAN`            | Small   | Schema + `boolToInt` removal   |
| JSON columns → `JSONB`                 | Small   | Schema + serialization helpers |
| `INSERT OR REPLACE` → `ON CONFLICT`    | Small   | 1 query                        |
| `julianday()` → `EXTRACT(EPOCH FROM)`  | Small   | 2 queries                      |
| Error detection → Postgres error codes | Small   | 1 helper                       |
| Connection pool sizing                 | Trivial | `store.go:Open()`              |
| Dialect-specific migration files       | Medium  | New migration file             |

See [storage-interface.md](storage-interface.md) for the recommended abstraction strategy.

## Implementation Order

1. Add `Dialect` field to store config.
2. Create `001_init_postgres.sql` with native types.
3. Branch `Open()` by dialect (driver, pool, init queries).
4. Replace serialization helpers with dialect-aware versions.
5. Fix the 3 non-portable SQL queries (upsert, 2x julianday).
6. Fix error detection for Postgres error codes.
7. Test against a real Postgres instance.

Total estimated scope: ~200 lines of changes in `store.go`, one new migration file, no changes outside the store package.
