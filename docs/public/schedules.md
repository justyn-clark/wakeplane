# Schedules

A schedule is the top-level definition in Wakeplane. It combines a cadence (cron, interval, once), a target (what to run), and policies (how to behave on overlap, misfire, failure).

## YAML manifest shape

Schedules can be created from a YAML file using `wakeplane schedule create -f <file>` or via `POST /v1/schedules` with a JSON body.

```yaml
name: nightly-sync           # required, unique identifier for display
enabled: true                # set false to create paused
timezone: America/Los_Angeles

schedule:
  kind: cron                 # cron | interval | once
  expr: "0 2 * * *"         # for cron: standard 5-field cron expression

target:
  kind: workflow             # http | shell | workflow
  workflow_id: sync.customers
  input:
    source: crm

policy:
  overlap: forbid            # allow | forbid | queue_latest | replace
  misfire: run_once_if_late  # skip | run_once_if_late | catch_up
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
  expr: "0 2 * * *"    # 2am daily
```

```yaml
schedule:
  kind: cron
  expr: "*/15 * * * *" # every 15 minutes
```

### interval

Fires every N seconds. The interval is anchored to the previous `next_run_at`, not to wall clock time, so intervals do not drift on restart.

```yaml
schedule:
  kind: interval
  every_seconds: 300   # every 5 minutes
```

### once

Fires once at a specific time and disables itself after firing.

```yaml
schedule:
  kind: once
  at: "2026-06-01T09:00:00Z"
```

The `at` timestamp is stored as an absolute RFC3339 time.

## Timezone behavior

Every schedule has a `timezone` field (IANA timezone string, e.g. `America/Los_Angeles`, `UTC`, `Europe/Berlin`).

- Cron expressions are evaluated in the schedule's timezone.
- The `once.at` timestamp must be supplied as an RFC3339 timestamp.
- Interval schedules use UTC internally; timezone affects only display.
- All `next_run_at` values stored in the database are UTC.

**DST transitions:** When clocks spring forward, occurrences that fall into the gap are skipped. When clocks fall back, the nominal time fires once (not twice). This is consistent with standard cron DST handling.

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

**Resume** sets `enabled=true`, clears `paused_at`, and recomputes `next_run_at` from the current time. The misfire policy governs what happens to any occurrences that were due while the schedule was paused.

## Trigger-now

```bash
wakeplane schedule trigger <id>
```

Creates a manual run immediately. The normal schedule cadence is unaffected — `next_run_at` is not changed. The manual run has a `manual:{run_id}` occurrence key separate from any scheduled occurrences.

Trigger requires a reason:

```bash
# via HTTP
POST /v1/schedules/{id}/trigger
{"reason": "manual smoke test"}
```

## Full replacement vs partial update

- `PUT /v1/schedules/{id}` — full replacement. All fields required. Equivalent to delete + create.
- `PATCH /v1/schedules/{id}` — partial update. Only provided fields change. Useful for toggling `enabled` or updating a target URL.

## Target kinds

See [Executors](executors.md) for full executor details. Brief reference:

| Kind | Required fields | Optional fields |
|---|---|---|
| `http` | `url`, `method` | `headers`, `body` |
| `shell` | `command` | `args` |
| `workflow` | `workflow_id` | `input` |

Timeout and concurrency are controlled by `policy.timeout_seconds` and `policy.max_concurrency`, not by target-specific fields.

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

`max_attempts: 0` means no retries. Set to a positive integer to enable retry behavior.

See [Policies](policies.md) for full policy semantics.
