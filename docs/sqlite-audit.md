# SQLite-Specific Assumptions Audit

This document catalogs every SQLite-specific behavior in the Wakeplane storage layer. All identified items are isolated in `internal/store/store.go` and `internal/store/migrations/001_init.sql`.

## Driver and Connection

| Location         | Assumption                                            | Postgres Change                                          |
| ---------------- | ----------------------------------------------------- | -------------------------------------------------------- |
| `store.go:13`    | `modernc.org/sqlite` driver import                    | Replace with `github.com/lib/pq` or `pgx`                |
| `store.go:36`    | `sql.Open("sqlite", path)`                            | Change to `sql.Open("postgres", connStr)`                |
| `store.go:40`    | `SetMaxOpenConns(1)` — single-writer for SQLite WAL   | Remove or set to pool size (20-25)                       |
| `store.go:41-42` | `PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL` | Remove (Postgres enables FK by default, has its own WAL) |

## Time Encoding

All timestamps are stored as `TEXT` in RFC3339Nano format with manual parsing.

| Location                  | Pattern                                                  | Postgres Change                                      |
| ------------------------- | -------------------------------------------------------- | ---------------------------------------------------- |
| `store.go:1023-1025`      | `timeString()` converts `time.Time` → RFC3339Nano string | Use `TIMESTAMPTZ` columns, pass `time.Time` directly |
| `store.go:1039-1045`      | `parseNullTime()` scans string → `*time.Time`            | Scan `*time.Time` directly from native column        |
| `store.go:1055-1062`      | `mustParseTime()` with RFC3339Nano/RFC3339 fallback      | Remove — driver handles natively                     |
| Schema: all `_at` columns | `TEXT NOT NULL` or `TEXT NULL`                           | `TIMESTAMPTZ NOT NULL` or `TIMESTAMPTZ NULL`         |

**Scope:** ~20 columns across 5 tables. Every query that writes or reads a timestamp uses the string helpers.

## Boolean Encoding

Booleans are stored as `INTEGER` with 0/1 conversion.

| Location                    | Pattern                       | Postgres Change                          |
| --------------------------- | ----------------------------- | ---------------------------------------- |
| `store.go:84,120,183,222`   | `boolToInt(schedule.Enabled)` | Pass `bool` directly to `BOOLEAN` column |
| `store.go:649,964`          | `enabledInt == 1` on scan     | Scan `bool` directly                     |
| `store.go:1064-1069`        | `boolToInt()` helper          | Remove                                   |
| Schema: `schedules.enabled` | `INTEGER NOT NULL DEFAULT 1`  | `BOOLEAN NOT NULL DEFAULT TRUE`          |

## JSON Columns

JSON is stored as `TEXT`, serialized manually.

| Location                                                                        | Pattern                                         | Postgres Change                             |
| ------------------------------------------------------------------------------- | ----------------------------------------------- | ------------------------------------------- |
| `store.go:86,122`                                                               | `mustJSONString()` serializes struct → string   | Use `JSONB` columns, pass `[]byte` directly |
| `store.go:287,426`                                                              | `rawJSON()` converts `json.RawMessage` → string | Pass bytes directly                         |
| `store.go:223-224,650-655`                                                      | `json.Unmarshal()` on scan                      | Scan `json.RawMessage` directly from JSONB  |
| Schema: `schedule_spec_json`, `target_spec_json`, `result_json`, `payload_json` | `TEXT NULL`                                     | `JSONB NULL`                                |

## SQL Dialect

| Location           | SQLite Syntax                                                   | Postgres Equivalent                                         |
| ------------------ | --------------------------------------------------------------- | ----------------------------------------------------------- |
| `store.go:508`     | `INSERT OR REPLACE INTO worker_leases`                          | `INSERT INTO ... ON CONFLICT (lease_key) DO UPDATE SET ...` |
| `store.go:855-859` | `julianday(finished_at) - julianday(started_at)` for duration   | `EXTRACT(EPOCH FROM (finished_at - started_at))`            |
| `store.go:906`     | `COALESCE(SUM((julianday(...) - julianday(...)) * 86400.0), 0)` | `COALESCE(SUM(EXTRACT(EPOCH FROM (... - ...))), 0)`         |

## Error Detection

| Location             | Pattern                                                  | Postgres Change                                     |
| -------------------- | -------------------------------------------------------- | --------------------------------------------------- |
| `store.go:1092-1097` | `isUniqueErr()` checks `strings.Contains(err, "unique")` | Check `pq.Error.Code == "23505"` (unique_violation) |

## Raw DB Access

| Location      | Pattern                                               | Note                                                                   |
| ------------- | ----------------------------------------------------- | ---------------------------------------------------------------------- |
| `store.go:49` | `func (s *Store) DB() *sql.DB` exposes raw connection | Used in tests for direct SQL. Must remain dialect-aware or be removed. |

## What Is Already Portable

- All IDs are application-generated `TEXT PRIMARY KEY` (ULID) — no SERIAL/AUTOINCREMENT dependency.
- `CREATE TABLE IF NOT EXISTS` syntax is standard.
- `PRIMARY KEY`, `UNIQUE`, `FOREIGN KEY`, `CHECK` constraints are standard.
- `ON DELETE CASCADE` is standard.
- `COALESCE` is standard (except when wrapping `julianday`).
- Transaction usage (`BeginTx` with default isolation) works on both.
- Cursor-based pagination (`ORDER BY created_at DESC, id DESC`) is standard.

## Migration Path

1. **Abstract the dialect** behind a narrow interface or config flag that switches:
   - Driver import and connection string
   - Pragma initialization
   - Time/boolean/JSON serialization helpers
   - Upsert syntax
   - Date arithmetic functions
   - Error code detection
2. **Dual-schema migration files**: `001_init_sqlite.sql` and `001_init_postgres.sql` with appropriate column types.
3. **No application logic changes needed** — the domain, dispatcher, planner, and API layers do not depend on storage internals.
