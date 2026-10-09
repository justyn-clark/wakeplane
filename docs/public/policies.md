# Policies

Policies govern how Wakeplane handles concurrent execution, missed runs, timeouts, and failures. They are defined per schedule and apply to every occurrence.

## Overlap policy

The overlap policy controls what happens when a new occurrence becomes due while a previous one is still running.

### `forbid` (default)

The new occurrence is not claimed while another occurrence of the schedule remains active, even when `max_concurrency` is greater than 1. The pending run waits in the queue. Nothing is skipped by this overlap policy.

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

- **HTTP executor**: underlying request is cancelled via context - typically fast
- **Shell executor**: process receives `SIGKILL` via `exec.CommandContext` - reliable
- **Workflow executor**: `ctx.Done()` is closed; the handler must check it and return

Tracked HTTP jobs reject `replace` because the runner contract has no remote cancellation operation. Editing a schedule does not make previously submitted remote work cancellable; its unresolved checkpoint continues reserving capacity.

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
| `forbid`       | Block               | Wait until other active occurrences finish         |
| `queue_latest` | Finish naturally    | Skip all pending except most recent                |
| `replace`      | Cancel signal       | Cancel active, skip all pending except most recent |

## Misfire policy

The misfire policy controls what happens when the scheduler detects that one or more occurrences are due. A polling scheduler normally discovers a slot slightly after its nominal time; that delay alone must not discard every scheduled run.

### `run_once_if_late` (default)

Run exactly one occurrence, even if multiple are overdue. The most recent overdue occurrence runs; earlier ones are skipped.

Use when: missing a few runs is acceptable but you want at least one run after an outage.

### `skip`

Materialize a pending run for each due occurrence whose lateness is at most one scheduler polling interval. Record older occurrences as `skipped`, then advance to the next future slot. The default tolerance is five seconds, matching the default polling interval. `WAKEPLANE_SCHEDULER_INTERVAL_SECONDS` changes both the poll interval and this tolerance.

The boundary is inclusive: with a five-second interval, a 09:00:00 slot detected at 09:00:05 runs; the same slot detected after 09:00:05 is skipped. The rule also applies to the first tick after a restart, to one-time schedules, and to interval and timezone-based cron schedules. If several short-cadence slots fall within the tolerance, each is materialized; overlap and concurrency policies still govern execution. Reduce the polling interval when a tighter stale-work cutoff is required.

This is a planning freshness rule, not an execution deadline: an accepted run can wait for dispatcher capacity. Schedule start/end bounds limit nominal slots, not the time at which accepted work finishes. Existing run records are never rewritten when the planner sees an occurrence again.

Use when: running stale work would be incorrect or wasteful. Health checks and time-sensitive reports are good examples.

Before 1.0, `skip` incorrectly discarded every due occurrence, including a slot detected exactly on time. Upgrading fixes future planning; historical skipped runs remain unchanged and are not replayed.

### `catch_up`

Materialize a run for every missed occurrence. There is currently no configurable catch-up limit, so a long outage with a short cadence can create a large backlog. Runs are considered in due-time order, subject to overlap and concurrency policy.

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

Unresolved tracked remote jobs also reserve capacity while waiting for a retry or after local tracking has timed out. Only a confirmed terminal remote status releases that reservation. Use [reconciliation](automation.md) to inspect a locally terminal run without resubmitting its work.

## Retry

Retry settings define what happens when a run finishes with an error.

```yaml
retry:
  max_attempts: 5 # total attempts including the first (0 = no retries)
  strategy: exponential # none | exponential
  initial_delay_seconds: 30
  max_delay_seconds: 900
```

**Exponential backoff:** Each retry delay is doubled from the previous, bounded by `max_delay_seconds`.

- Attempt 1: initial execution
- Attempt 2: delay = `initial_delay_seconds` x 2^0 = 30s
- Attempt 3: delay = 30s x 2^1 = 60s
- Attempt 4: delay = 30s x 2^2 = 120s
- ...capped at `max_delay_seconds`

When all attempts are exhausted, the run is dead-lettered. Dead letters are visible at `GET /v1/status` and the metrics endpoint.

`max_attempts` counts total attempts, so values `0` and `1` do not allow a retry. `strategy: none` disables retries regardless of that count. Confirmed terminal remote failures are not resubmitted as fresh jobs.

**Cancellation is not retried.** If a run is cancelled (shutdown or `replace` overlap), no retry is scheduled.

## Policy interaction example

A schedule with `overlap: forbid`, `misfire: run_once_if_late`, `retry.max_attempts: 3`:

1. Daemon is down for 2 hours. Three occurrences were missed.
2. On restart: planner sees 3 overdue occurrences. `run_once_if_late` materializes exactly one run (skips the earlier two).
3. The run is dispatched and fails.
4. The dispatcher schedules a retry with exponential backoff.
5. After 3 total attempts, if still failing, the run is dead-lettered.
6. Normal cadence resumes from the next future occurrence.
