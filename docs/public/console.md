# Operator Console

Wakeplane serves a compact single-operator console from the daemon at `/console/`.

The console supports guided schedule creation and editing alongside inspection and recovery. Start with a recipe, choose an explicit timezone and target, preview the next occurrences, then save. New drafts start paused unless you explicitly enable them. YAML, API, and CLI remain available.

## What it shows

- Run ledger in reverse chronological order
- Filters for run status, schedule, and target kind
- Schedule name, run status, attempt, target kind, worker, timestamps, duration, retry timing, and error preview
- Run inspector with full metadata, timeline, attempt history, receipts, dead-letter details, result preview, and raw JSON
- Schedule inspector with enabled/paused state, timing, target summary, policies, retry strategy, recent runs, and raw JSON
- Daemon status with health, readiness, store backend, scheduler state, workers, run counts, retention, auth, and request audit posture

## Operator actions

The console supports the same safe schedule actions as the API:

- pause
- resume
- trigger now with a reason
- create or edit a schedule with validated timing, target, policy, and retry settings
- preview up to five next-run times without storing or executing the draft
- copy schedule or run IDs

It does not bypass policy enforcement, durable run recording, target typing, auth, or request audit logging.

Tracked HTTP jobs show the runner's durable identity, progress, result, and artifact links. An accepted remote job remains unfinished until the runner reports a terminal status. Timeout stops local tracking; it does not promise remote cancellation. See [Automation](automation.md) and [Recipes](recipes.md).

## Auth boundary

The static console assets are served without authentication so a local browser can load the page. Console data and actions call `/v1/...`, which requires `Authorization: Bearer <token>` when `WAKEPLANE_AUTH_TOKEN` is set.

Use the Token button in the console to store the operator token in browser local storage for that origin.
