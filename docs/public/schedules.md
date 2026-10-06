# Schedules

A schedule is the top-level definition in Wakeplane. It combines a cadence (cron, interval, once), a target (what to run), and policies (how to behave on overlap, misfire, failure).

## YAML manifest shape

Schedules can be created from a YAML file using `wakeplane schedule create -f <file>` or via `POST /v1/schedules` with a JSON body.

```yaml
name: nightly-sync # required, unique identifier for display
enabled: true # set false to create paused
timezone: America/Los_Angeles

schedule:
  kind: cron # cron | interval | once
  expr: "0 2 * * *" # for cron: standard 5-field cron expression

target:
  kind: workflow # http | shell | workflow
  workflow_id: sync.customers
  input:
    source: crm

policy:
  overlap: forbid # allow | forbid | queue_latest | replace
  misfire: run_once_if_late # skip | run_once_if_late | catch_up
  timeout_seconds: 900
  max_concurrency: 1

retry:
  max_attempts: 5
  strategy: exponential
  initial_delay_seconds: 30
  max_delay_seconds: 900
```

## Schedule kinds

### cron

Uses a standard 5-field cron expression (minute, hour, day-of-month, month, day-of-week). The next occurrence is computed using the schedule's `timezone`.

```yaml
schedule:
  kind: cron
  expr: "0 2 * * *" # 2am daily
```

```yaml
schedule:
  kind: cron
  expr: "*/15 * * * *" # every 15 minutes
```

### interval

Fires every N seconds on a fixed cadence. The anchor is `schedule.anchor_at` when supplied, otherwise `start_at`, otherwise the schedule's creation time. Restart and resume compute the next slot on that cadence rather than shifting the anchor to the current time.

```yaml
schedule:
  kind: interval
  every_seconds: 300 # every 5 minutes
```

### once

Fires once at a specific time.

```yaml
schedule:
  kind: once
  at: "2026-06-01T09:00:00-07:00"
```

`schedule.at` is an absolute timestamp. Provide a full RFC3339 value with offset. If you want "9am Los Angeles time", encode that offset in the timestamp itself.

After the occurrence is materialized, `next_run_at` becomes `nil`. The schedule is not automatically rewritten to `enabled=false`; it simply has no next occurrence left to materialize.

## Timezone behavior

Every schedule requires an explicit `timezone` field (IANA timezone string, e.g. `America/Los_Angeles`, `UTC`, `Europe/Berlin`). Empty values and the host-dependent `Local` value are rejected.

- Cron expressions are evaluated in the schedule's timezone.
- The `once.at` timestamp is stored as an absolute instant; include the intended offset in the value.
- Interval schedules use UTC internally; timezone affects only display.
- All `next_run_at` values stored in the database are UTC.

**DST transitions:** Cron times in a spring-forward gap are skipped. During fall-back, a repeated local clock time can produce two occurrences at distinct UTC instants. For example, `30 1 * * *` in `America/Los_Angeles` fires at both `08:30Z` and `09:30Z` on November 1, 2026. Each instant has its own occurrence identity.

## Pause and resume

```bash
wakeplane schedule pause <id>
wakeplane schedule resume <id>
```

Or via HTTP:

```bash
POST /v1/schedules/{id}/pause
POST /v1/schedules/{id}/resume
```

**Pause** sets `enabled=false` and records `paused_at`. The planner stops materializing new occurrences. Existing pending or running runs are not affected.

**Resume** sets `enabled=true`, clears `paused_at`, and recomputes `next_run_at` from the current time. It does not catch up occurrences missed while paused. Misfire policy applies when an enabled schedule's persisted next occurrence becomes overdue.

## Trigger-now

```bash
wakeplane schedule trigger <id>
```

Creates a manual run immediately. The normal schedule cadence is unaffected - `next_run_at` is not changed. The manual run has a `manual:{run_id}` occurrence key separate from any scheduled occurrences.

Trigger requires a reason:

```bash
# via HTTP
POST /v1/schedules/{id}/trigger
{"reason": "manual smoke test"}
```

## Full replacement vs partial update

- `PUT /v1/schedules/{id}` - replace the complete schedule definition, preserving its ID, creation time, and run history. Omitted optional fields return to their defaults; omitted `enabled` creates a paused definition. Supply the required name, timezone, schedule, and typed target.
- `PATCH /v1/schedules/{id}` - partial update. Only provided fields change. Useful for toggling `enabled` or updating a target URL.

## Target kinds

See [Executors](executors.md) for full executor details. Brief reference:

| Kind       | Required fields | Optional fields               |
| ---------- | --------------- | ----------------------------- |
| `http`     | `url`, `method` | `headers`, `body`, `http_job` |
| `shell`    | `command`       | `args`                        |
| `workflow` | `workflow_id`   | `input`                       |

Timeout and concurrency are controlled by `policy.timeout_seconds` and `policy.max_concurrency`, not by target-specific fields.

`http_job` selects tracked remote execution and requires `method: POST`. Its optional settings are `poll_interval_seconds` (default 5, allowed 1–300) and a same-origin `lookup_url` for recovery by submission key. `overlap: replace` is rejected for this mode. See [Automation](automation.md) for the runner contract.

## Default policy values

When policy or retry fields are omitted, these defaults apply:

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

`max_attempts` counts total attempts including the first. Values `0` and `1` do not schedule another attempt. Use `strategy: exponential` with `max_attempts` greater than 1 to allow retries; `strategy: none` disables them.

See [Policies](policies.md) for full policy semantics.
