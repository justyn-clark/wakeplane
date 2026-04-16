# Policies

Policies govern how Wakeplane handles concurrent execution, missed runs, timeouts, and failures. They are defined per schedule and apply to every occurrence.

## Overlap policy

The overlap policy controls what happens when a new occurrence becomes due while a previous one is still running.

### `forbid` (default)

The new run is not claimed until the active count drops below `max_concurrency`. The pending run waits in the queue. Nothing is skipped.

Use `forbid` when:

- Concurrent execution of the same schedule would be unsafe
- Runs operate on shared state that must be serialized

### `allow`

New runs start regardless of how many are already active, up to `max_concurrency`. If `max_concurrency` is `1`, this is equivalent to `forbid`.

Use `allow` when:

- Runs are fully independent and concurrent execution is safe
- You want to maximize throughput without queuing

### `queue_latest`

All pending runs except the most recent one are skipped. The active run finishes naturally. Only the latest pending run is dispatched when capacity opens.

Use `queue_latest` when:

- Only the most recent input matters
- You want to discard stale pending work rather than queue everything

### `replace`

Active runs receive a cancellation signal (`ctx.Done()`). All pending runs except the most recent are skipped. The latest pending run is dispatched once the active run exits.

**`replace` is cooperative and best-effort.** Wakeplane cannot force-kill an executor. The actual behavior depends on the executor:

- **HTTP executor**: underlying request is cancelled via context — typically fast
- **Shell executor**: process receives `SIGKILL` via `exec.CommandContext` — reliable
- **Workflow executor**: `ctx.Done()` is closed; the handler must check it and return

If the active executor does not stop promptly:

- The active run retains its `running` status
- The pending run waits until the active run finishes or its lease expires
- Skipped runs have `error_text: "replace overlap downgraded to queued latest until current execution exits"`

Use `replace` when:

- The schedule represents a "latest state" computation
- The executor reliably honors context cancellation
- Degrading to `queue_latest` behavior is acceptable if cancellation is slow

Do not use `replace` when:

- Cancellation of the active run has destructive side effects
- You need a hard guarantee that only one run is ever active
- The executor is known to ignore cancellation

### Comparison

| Policy         | Active run present? | Behavior                                           |
| -------------- | ------------------- | -------------------------------------------------- |
| `allow`        | Ignored             | Start new run up to `max_concurrency`              |
| `forbid`       | Block               | Wait until active count drops                      |
| `queue_latest` | Finish naturally    | Skip all pending except most recent                |
| `replace`      | Cancel signal       | Cancel active, skip all pending except most recent |

## Misfire policy

The misfire policy controls what happens when the scheduler detects that one or more occurrences were missed (e.g., because the daemon was down, or the planner ticked late).

### `run_once_if_late` (default)

Run exactly one occurrence, even if multiple are overdue. The most recent overdue occurrence runs; earlier ones are skipped.

Use when: missing a few runs is acceptable but you want at least one run after an outage.

### `skip`

Skip all overdue occurrences. The schedule resumes from the next future occurrence.

Use when: running stale work would be incorrect or wasteful. Health checks and time-sensitive reports are good examples.

### `catch_up`

Materialize a run for every missed occurrence, up to a configurable limit. Runs are dispatched in order.

Use when: every occurrence must be processed and missing data is not acceptable. Carefully pair this with `forbid` overlap and a reasonable max retry limit to prevent unbounded queuing after a long outage.

## Timeout

`policy.timeout_seconds` sets a deadline for the executor. When the deadline expires:

- The executor's context (`ctx`) has its deadline fired.
- HTTP and workflow executors should observe `ctx.Done()` and stop.
- Shell executors receive `SIGKILL` from `exec.CommandContext`.

Default: `300` seconds (5 minutes).

If a run exceeds its timeout and the executor does not stop, behavior depends on executor cooperation. See [Executors](executors.md).

## Max concurrency

`policy.max_concurrency` sets the maximum number of simultaneously active runs for a schedule. Default: `1`.

The dispatcher checks the count of `claimed` + `running` runs for the schedule before claiming a new one. If the count is at the limit, the run waits (behavior depends on `overlap` policy).

## Retry

Retry settings define what happens when a run finishes with an error.

```yaml
retry:
  max_attempts: 5 # total attempts including the first (0 = no retries)
  strategy: exponential # exponential (only supported strategy currently)
  initial_delay_seconds: 30
  max_delay_seconds: 900
```

**Exponential backoff:** Each retry delay is doubled from the previous, bounded by `max_delay_seconds`.

- Attempt 0: initial execution
- Attempt 1: delay = `initial_delay_seconds` × 2^0 = 30s
- Attempt 2: delay = 30s × 2^1 = 60s
- Attempt 3: delay = 30s × 2^2 = 120s
- ...capped at `max_delay_seconds`

When all attempts are exhausted, the run is dead-lettered. Dead letters are visible at `GET /v1/status` and the metrics endpoint.

**Cancellation is not retried.** If a run is cancelled (shutdown or `replace` overlap), no retry is scheduled.

## Policy interaction example

A schedule with `overlap: forbid`, `misfire: run_once_if_late`, `retry.max_attempts: 3`:

1. Daemon is down for 2 hours. Three occurrences were missed.
2. On restart: planner sees 3 overdue occurrences. `run_once_if_late` materializes exactly one run (skips the earlier two).
3. The run is dispatched and fails.
4. The dispatcher schedules a retry with exponential backoff.
5. After 3 total attempts, if still failing, the run is dead-lettered.
6. Normal cadence resumes from the next future occurrence.
