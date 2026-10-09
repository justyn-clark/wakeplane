# API Contract

This document defines the machine contract of the standalone Wakeplane HTTP API. The [generated API reference](public/api.md) lists every implemented route; the [stable contract](public/stable-contract.md) defines the compatibility boundary for 1.0.

## Authentication and transport

When `WAKEPLANE_AUTH_TOKEN` is configured, all `/v1/...` requests require `Authorization: Bearer <token>`. Missing or invalid authorization returns `401 unauthorized`. Health/readiness probes and static console files are outside this auth boundary. Wakeplane requires trusted networking and operator-controlled TLS; a bearer token does not provide RBAC, users, or multi-tenancy.

Send JSON request bodies with `Content-Type: application/json`. REST JSON responses use `application/json`; `/v1/metrics` uses `text/plain; version=0.0.4`. The server decodes JSON bodies; it does not uniformly reject a missing or different request content type. Clients must not rely on that permissiveness. Unknown fields may be rejected, particularly by the bounded preview and event decoders.

MCP uses JSON-RPC and its own tool-result envelopes after the shared HTTP authentication boundary. It is not an alternative REST error envelope.

## Error envelope

Application-level REST errors return:

```json
{
  "code": "validation_failed",
  "error": "human-readable description",
  "details": [
    { "field": "timezone", "message": "must be a valid IANA timezone" }
  ]
}
```

- `code` is the machine-readable error category.
- `error` and validation-detail `message` text may change; do not parse their wording.
- `details` identifies validation fields and is omitted or empty for other errors.

| HTTP status | Code                        | Meaning                                                                                            |
| ----------- | --------------------------- | -------------------------------------------------------------------------------------------------- |
| 400         | `bad_request`               | Malformed JSON, invalid filter/cursor, or invalid trigger/event request                            |
| 400         | `validation_failed`         | Schedule definition or patch fails domain validation                                               |
| 401         | `unauthorized`              | Missing or invalid configured bearer token                                                         |
| 404         | `not_found`                 | Resource does not exist                                                                            |
| 409         | `conflict`                  | Conflicting event replay, paused event target, or incompatible remote reconciliation state         |
| 410         | `history_pruned`            | Event was already processed, but its original run has been pruned                                  |
| 500         | `internal_error`            | Unexpected application or storage error                                                            |
| 502         | `runner_observation_failed` | Remote status or lookup cannot be safely observed; checkpoint and overlap reservation are retained |

Unmatched routes, unsupported methods, and HTTP transport failures can use Go's plain-text `404`/`405` or transport responses. Readiness failure uses a probe response, not this envelope. Handle non-JSON responses and non-2xx status codes before parsing application errors.

## Pagination and filtering

Schedule and run list endpoints return:

```json
{
  "items": [],
  "next_cursor": null
}
```

- Empty `items` is an array (`[]`), never null.
- `limit` defaults to `50`; invalid or non-positive values also fall back to `50`.
- `cursor` is the opaque value from the preceding response. Malformed values return `400 bad_request`.
- Results are ordered by `created_at DESC, id DESC`.
- Continue until `next_cursor` is null. Do not interpret cursor encoding, reuse it with different filters, or assume it provides a transactional snapshot across concurrent writes/deletes.
- `GET /v1/schedules` accepts strict `enabled=true|false`.
- `GET /v1/runs` accepts `schedule_id`, `status`, and `target_kind`.
- `GET /v1/schedules/{id}/runs` accepts `status` and `target_kind`.
- Status filters accept `pending`, `claimed`, `running`, `succeeded`, `failed`, `retry_scheduled`, `dead_lettered`, `cancelled`, and `skipped`.
- Target filters accept `http`, `shell`, and `workflow`.

Filters combine with AND, match exactly and are case-sensitive. Invalid enabled/status/target filters return `400 bad_request`. Receipt lists use the `items` envelope but are not cursor-paginated.

## Schedule and run semantics

- Create returns `201` and the full schedule. PUT returns `200`, preserves schedule ID/history, and replaces the definition; omitted optional fields use create defaults. PATCH changes provided fields only, including individual policy/retry fields.
- Pause prevents new planned occurrences. It does not cancel existing work. Resume computes the next slot from the current time and does not replay the paused period.
- Manual trigger requires a non-empty reason and creates a distinct occurrence. It does not move the regular cadence or override executor policy.
- Event delivery returns `201` on first delivery and `200` for an identical replay. A different payload for the same schedule/source/event identity returns `409`. Pruned event history returns `410`, preserving replay protection.
- Preview validates a draft without storing or executing it and returns up to five timezone-aware slots.
- Delete removes the schedule and its runs, receipts, leases, dead letters, remote checkpoints, and event deduplication records. Request audit history remains. Deletion is not remote cancellation; stop and reconcile work first.
- A run represents one attempt. A retry creates another run with the same `occurrence_key` and a greater `attempt`. Failed attempts remain terminal.
- Remote reconciliation observes existing remote work and appends evidence. It neither submits new work nor rewrites the original local terminal outcome.

See [Run states](public/run-states.md), [Policies](public/policies.md), and [Automation](public/automation.md) for execution and remote-job contracts. A durable occurrence identity does not imply exactly-once side effects at a remote provider.

## Probes and operational output

- `GET /healthz`: `200` with `{ "ok": true }` while the process serves requests.
- `GET /readyz`: `200` with `{ "ok": true, "storage": "ok" }` when storage is reachable; `503` with `{ "ok": false, "storage": "error" }` otherwise.
- `GET /v1/status`: JSON operational status; it can return an application error if storage cannot be inspected.
- `GET /v1/metrics`: Prometheus text, not JSON. Metrics and status are observations, not execution-time guarantees.

## Creation defaults

Omitted policy/retry fields receive:

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

`max_attempts` counts total attempts including the first. Values `0` and `1` allow no retry. Use `max_attempts > 1` with `strategy: exponential` for retries; `strategy: none` disables retries. Set important policy values explicitly in operator manifests. Changing a documented default is a breaking compatibility change after 1.0.
