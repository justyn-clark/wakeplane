# Replace Overlap Semantics

The `replace` overlap policy is one of four overlap modes in Wakeplane. This document explains its exact behavior, limitations, and degradation path.

## Overview

When a schedule has `policy.overlap: replace`, the intent is: when a new occurrence becomes due while a previous one is still running, cancel the active execution and run the new one.

**In practice, `replace` is cooperative and best-effort.** Wakeplane cannot force-kill an executor. It can only cancel the execution context and wait.

## Exact Behavior

When the dispatcher prepares to claim a new run for a `replace`-policy schedule:

1. **List active runs** for the schedule (status `claimed` or `running`).
2. **Cancel each active run** by calling `cancel()` on its execution context.
3. **Enforce queue policy**: skip all pending runs except the most recent one (same as `queue_latest`).
4. **Proceed to claim** the most recent pending run.

The cancellation at step 2 sends `ctx.Done()` to the executor goroutine. Whether and when the executor actually stops depends on the executor implementation:

- **HTTP executor**: the underlying HTTP request is cancelled via context. Most HTTP clients abort promptly.
- **Shell executor**: the process is sent `SIGKILL` via `exec.CommandContext`. This is relatively reliable.
- **Workflow executor**: the `ctx.Done()` channel is closed. The handler must check it and return. If the handler ignores cancellation, it continues running until it returns on its own or the process exits.

## Degradation

If the active executor does not stop promptly after cancellation:

- The active run retains its `running` status and its worker lease.
- The `max_concurrency` limit is still reached.
- The new occurrence is either:
  - Queued as `pending` if it was not yet claimed.
  - Skipped with reason `"replace overlap downgraded to queued latest until current execution exits"` if there are multiple pending runs and it is not the most recent.

**The practical effect is that `replace` degrades to `queue_latest` behavior** when the active executor is slow or non-cooperative. The most recent pending run will eventually be dispatched when the active run finishes (or its lease expires and recovery handles it).

## What `replace` Does NOT Guarantee

- It does not guarantee the active run will be interrupted before the new run starts.
- It does not guarantee the active run will transition to `cancelled` immediately.
- It does not force-terminate any process or goroutine.
- It does not skip the `max_concurrency` check. If the active run is still counted as active, the new run cannot be claimed until it finishes.

## Operator-Visible Signals

When `replace` degrades:

- Skipped runs will have `error_text` set to `"replace overlap downgraded to queued latest until current execution exits"`.
- The active run continues to appear in `GET /v1/status` under `runs.running`.
- Worker lease heartbeats continue for the active run.

## When to Use `replace`

Use `replace` when:

- The schedule represents a "latest state" computation where only the most recent input matters.
- The executor reliably honors context cancellation.
- You accept that degradation to `queue_latest` is safe for your use case.

Do not use `replace` when:

- Cancellation of the active run has destructive side effects.
- You need a hard guarantee that only one run is ever active.
- The executor is known to ignore cancellation (use `queue_latest` instead and let the active run finish naturally).

## Comparison with Other Overlap Policies

| Policy | Active run exists? | Behavior |
|---|---|---|
| `allow` | Ignored | New run starts regardless of active count (up to `max_concurrency`) |
| `forbid` | Checked | New run is not claimed until active count drops below `max_concurrency` |
| `queue_latest` | Checked | All pending runs except the most recent are skipped. Active runs finish naturally. |
| `replace` | Cancelled | Active runs receive cancellation signal. All pending except most recent are skipped. Degrades to `queue_latest` if cancellation is not honored. |
