# API Contract

This document defines the error envelope, pagination, and behavioral guarantees of the Wakeplane HTTP JSON API.

## Error Envelope

All errors return a JSON body with this shape:

```json
{
  "code": "string",
  "error": "human-readable message",
  "details": []
}
```

**Fields:**

- `code` — machine-readable error category (see table below).
- `error` — human-readable description. Not guaranteed to be stable across versions.
- `details` — array of `{"field": "...", "message": "..."}` objects. Present only for `validation_failed` responses. Empty or omitted otherwise.

**Error codes and HTTP status mapping:**

| HTTP Status | Code                | When                                                                 |
| ----------- | ------------------- | -------------------------------------------------------------------- |
| 400         | `bad_request`       | Malformed JSON, invalid query parameters, missing required fields    |
| 400         | `validation_failed` | Schedule or patch fails domain validation. `details` array populated |
| 404         | `not_found`         | Resource ID does not exist                                           |
| 409         | `conflict`          | Unique constraint violation (e.g., duplicate schedule name)          |
| 500         | `internal_error`    | Unexpected server error                                              |

**Non-API errors:** If the server cannot parse the request at all (e.g., wrong content type, unsupported method), the Go standard library returns its own plain-text error. These do not follow the JSON envelope.

## Pagination

List endpoints (`GET /v1/schedules`, `GET /v1/runs`, `GET /v1/schedules/{id}/runs`) use cursor-based pagination.

**Request parameters:**

- `limit` — maximum items to return. Default `50`. Invalid or non-positive values fall back to `50`.
- `cursor` — opaque cursor string from a previous response. Omit for the first page. Malformed cursor values return `400 bad_request`.

**Response shape:**

```json
{
  "items": [...],
  "next_cursor": "opaque_string_or_null"
}
```

- `items` — array of results, ordered by `created_at DESC, id DESC`.
- `next_cursor` — if non-null, pass as `cursor` to fetch the next page. When null, there are no more results.

**Cursor format:** The cursor is a base64url-encoded JSON object containing `created_at` and `id`. Clients must treat it as opaque. Cursors from one endpoint are not valid at another. Cursors do not expire but may become invalid if the underlying data is deleted.

**Ordering:** List results are ordered newest-first (`created_at DESC`). Ties are broken by `id DESC`. This order is stable and consistent across pages.

## Filtering

**`GET /v1/schedules`:**

- `enabled=true|false` — filter by enabled state. The value is case-sensitive and strict: `true` filters enabled schedules, `false` filters disabled schedules, and any other value returns `400 bad_request`.

**`GET /v1/runs` and `GET /v1/schedules/{id}/runs`:**

- `schedule_id=<id>` — filter by schedule (only on `/v1/runs`).
- `status=<status>` — filter by run status. Accepted values are `pending`, `claimed`, `running`, `succeeded`, `failed`, `retry_scheduled`, `dead_lettered`, `cancelled`, and `skipped`.

Filters are combined with AND. `enabled` and `status` are validated strictly by the handler and reject invalid values with `400 bad_request`. Matching is exact and case-sensitive.

## Content Types

- Request bodies must be `application/json`.
- Responses are `application/json` except `/v1/metrics` which returns `text/plain; version=0.0.4` (Prometheus exposition format).

## Endpoint Semantics

### Schedule CRUD

| Method                      | Path                                                                                                               | Semantics |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------ | --------- |
| `POST /v1/schedules`        | Create. Returns `201` with the full schedule on success. Defaults are applied for omitted policy and retry fields. |
| `GET /v1/schedules/{id}`    | Read. Returns `200` with the full schedule including computed `next_run_at`.                                       |
| `PUT /v1/schedules/{id}`    | Full replacement. All fields are required (same validation as create). Returns `200`.                              |
| `PATCH /v1/schedules/{id}`  | Partial update. Only provided fields are changed. Returns `200`.                                                   |
| `DELETE /v1/schedules/{id}` | Delete. Cascades to runs, leases, receipts, and dead letters. Returns `200` with `{"deleted": true, "id": "..."}`. |

### Schedule Actions

| Method             | Path                                                                                                                                                                              | Semantics |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- |
| `POST .../pause`   | Sets `enabled=false`, records `paused_at`. Returns `200` with `{id, paused_at, enabled}`.                                                                                         |
| `POST .../resume`  | Sets `enabled=true`, clears `paused_at`, recomputes `next_run_at`. Returns `200` with `{id, paused_at, enabled, next_run_at}`.                                                    |
| `POST .../trigger` | Creates a manual run with `manual:{run_id}` occurrence key. Requires `{"reason": "..."}` in body. Returns `200` with `{run_id, schedule_id, occurrence_key, status, created_at}`. |

### Run Inspection

| Method                       | Path                                                                         | Semantics |
| ---------------------------- | ---------------------------------------------------------------------------- | --------- |
| `GET /v1/runs/{id}`          | Returns the full run record including all result fields.                     |
| `GET /v1/runs/{id}/receipts` | Returns execution receipts (stdout, stderr, HTTP response, workflow result). |

### Operational

| Method            | Path                                                                                                                                | Semantics |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------- | --------- |
| `GET /healthz`    | Always returns `{"ok": true}`. Use for liveness probes.                                                                             |
| `GET /readyz`     | Returns `{"ok": bool, "storage": "ok\|error"}`. Use for readiness probes.                                                           |
| `GET /v1/status`  | Returns full operational status: scheduler state, worker counts, run counts, next-due schedule information, and dead-letter counts. |
| `GET /v1/metrics` | Prometheus text metrics, including `runs_due`, `runs_retry_queued`, `dead_letters_total`, and `claimed_but_expired_total`.          |

## Default Policy Values

When creating a schedule, omitted policy and retry fields receive these defaults:

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

Note: `max_attempts: 0` means no retries by default. Set to a positive integer to enable retry behavior.
