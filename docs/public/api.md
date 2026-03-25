# API Reference

Wakeplane exposes a JSON HTTP API for schedule and run management. All routes are under `/v1/` except the health/readiness endpoints.

> **Alpha notice:** No authentication. Bind to a trusted network only. See [Security](security.md).

## Endpoints

### Health and readiness

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness probe. Returns `{"ok":true}` always. |
| `GET` | `/readyz` | Readiness probe. Returns `{"ok":true,"storage":"ok"}` when the database is reachable. |

### Operational status and metrics

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/status` | Full operational status including scheduler timing, worker counts, and run counts. |
| `GET` | `/v1/metrics` | Prometheus text metrics. |

### Schedule management

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/schedules` | Create a schedule. Returns `201` with the full schedule. |
| `GET` | `/v1/schedules` | List schedules. Supports `enabled`, `limit`, and `cursor` query params. |
| `GET` | `/v1/schedules/{id}` | Get a single schedule including computed `next_run_at`. |
| `PUT` | `/v1/schedules/{id}` | Full replacement. All fields required. |
| `PATCH` | `/v1/schedules/{id}` | Partial update. Only provided fields change. |
| `DELETE` | `/v1/schedules/{id}` | Delete schedule and cascade to runs, leases, receipts, dead letters. Returns `{"deleted":true,"id":"..."}`. |
| `POST` | `/v1/schedules/{id}/pause` | Set `enabled=false`, record `paused_at`. |
| `POST` | `/v1/schedules/{id}/resume` | Set `enabled=true`, clear `paused_at`, recompute `next_run_at`. |
| `POST` | `/v1/schedules/{id}/trigger` | Create a manual run. Requires `{"reason":"..."}` body. |

### Run inspection

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/runs` | List runs across all schedules. Supports `schedule_id`, `status`, `limit`, `cursor`. |
| `GET` | `/v1/schedules/{id}/runs` | List runs for a specific schedule. |
| `GET` | `/v1/runs/{id}` | Get a single run with full result fields. |
| `GET` | `/v1/runs/{id}/receipts` | Get execution receipts (stdout/stderr, HTTP response, workflow result). |

## Error envelope

All errors return a JSON body:

```json
{
  "code": "not_found",
  "error": "schedule not found",
  "details": []
}
```

| HTTP Status | Code | When |
|---|---|---|
| 400 | `bad_request` | Malformed JSON, invalid query parameters, missing required fields |
| 400 | `validation_failed` | Schedule fails domain validation. `details` array populated with `{field, message}` |
| 404 | `not_found` | Resource ID does not exist |
| 409 | `conflict` | Unique constraint violation (e.g., duplicate schedule name) |
| 500 | `internal_error` | Unexpected server error |

The `error` field is human-readable and not guaranteed to be stable across versions. Use `code` for programmatic error handling.

If the server cannot parse the request at all (wrong content type, unsupported method), the Go standard library returns a plain-text response that does not follow the JSON envelope.

## Pagination

List endpoints use cursor-based pagination:

```
GET /v1/schedules?limit=25&cursor=<opaque>
```

| Parameter | Default | Description |
|---|---|---|
| `limit` | 50 | Maximum items per page. Invalid values fall back to 50. |
| `cursor` | — | Opaque cursor from a previous response. Omit for the first page. |

Response shape:

```json
{
  "items": [...],
  "next_cursor": "opaque_string_or_null"
}
```

- `next_cursor` is null when there are no more results.
- Items are ordered newest-first (`created_at DESC, id DESC`).
- Cursors are base64url-encoded and opaque. Do not construct them manually.
- Cursors do not expire but may return a `400` if the underlying data is deleted.

## Filtering

**`GET /v1/schedules`:**
- `enabled=true` or `enabled=false` — strict boolean filter. Any other value returns `400`.

**`GET /v1/runs` and `GET /v1/schedules/{id}/runs`:**
- `schedule_id=<id>` — filter by schedule (only on `/v1/runs`).
- `status=<value>` — filter by run status. Valid values: `pending`, `claimed`, `running`, `succeeded`, `failed`, `retry_scheduled`, `dead_lettered`, `cancelled`, `skipped`. Invalid values return `400`.

Filters are AND-combined. All matching is exact and case-sensitive.

## Content types

- Request bodies: `application/json`
- Responses: `application/json`
- Exception: `GET /v1/metrics` returns `text/plain; version=0.0.4` (Prometheus exposition format)

## Status response shape

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

## Trigger response shape

`POST /v1/schedules/{id}/trigger` returns:

```json
{
  "run_id": "run_01...",
  "schedule_id": "sch_01...",
  "occurrence_key": "manual:run_01...",
  "status": "pending",
  "created_at": "2026-03-25T12:00:00Z"
}
```

## Default policy values on create

When creating a schedule with omitted policy or retry fields:

```json
{
  "policy": {
    "overlap": "forbid",
    "misfire": "run_once_if_late",
    "timeout_seconds": 300,
    "max_concurrency": 1
  },
  "retry": {
    "max_attempts": 0,
    "strategy": "exponential",
    "initial_delay_seconds": 30,
    "max_delay_seconds": 900
  }
}
```

`max_attempts: 0` means no retries.
