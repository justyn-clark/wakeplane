Production phase plan:

1. Postgres dialect behind the current store boundary

Goal: keep Wakeplane SQLite-first for local trusted installs, but add Postgres as a first-class production backend without changing scheduler semantics.

Deliverables:

- Add WAKEPLANE_STORE=sqlite|postgres, default sqlite.
- Add WAKEPLANE_DATABASE_URL for Postgres.
- Move schema setup into dialect-owned migrations.
- Preserve the existing store interface for schedules, runs, receipts, audit logs, leases, status, and retention.
- Add docs for SQLite local mode vs Postgres production mode.
- Add install/runbook copy that stays clear: Postgres is optional for production, not required for local use.

Success gate:

- Existing SQLite tests still pass unchanged.
- Postgres test suite runs against a disposable Postgres instance.
- Same CLI/API behavior works against both backends.

2. Real claim/retry verification

Goal: prove that Wakeplane can safely claim due work, survive concurrent workers, retry failed runs, and avoid duplicate execution under Postgres.

Deliverables:

- Implement Postgres-safe claiming using row locks or equivalent transactional claim semantics.
- Add lease expiry/reclaim tests.
- Add retry attempt accounting tests.
- Add dead-letter/final failure verification.
- Add concurrent worker tests that start multiple claimers against the same due schedule set.
- Add crash-window tests where a worker claims work and exits before completion.

Success gate:

- No duplicate run claim under concurrent workers.
- Expired leases are reclaimable.
- Terminal runs are not retried.
- Retry limits are enforced.
- Receipts and audit logs are consistent after failure and retry.

3. Production migration and operator docs

Goal: make Postgres adoption boring and reversible.

Deliverables:

- Add backup guidance for SQLite and Postgres.
- Add SQLite to Postgres migration/export path if practical in this phase, or explicitly document manual schedule export/import as the bridge.
- Add operational status fields showing active store dialect, database health, retention settings, auth posture, and worker claim state.
- Update /status/, /security/, /operations/runbook/, and CLI reference.

Success gate:

- Fresh Postgres install can be brought up from docs.
- Existing SQLite install remains unaffected.
- wakeplane status and wakeplane status --watch make backend state obvious.

4. Soak drill

Goal: prove the daemon can run continuously with real schedules and bounded storage.

Deliverables:

- Add a repeatable soak script or documented drill.
- Run representative schedules over a sustained window.
- Track run count, failures, retry behavior, receipt truncation, audit growth, retention pruning, and memory/process stability.
- Capture a receipt with exact commit, config, duration, counts, and failures.

Success gate:

- No unbounded receipt growth.
- Retention works during or after the soak.
- Status remains readable while work is running.
- Any failure is either expected/test-induced or filed as a blocker.

5. Restart drill

Goal: prove Wakeplane survives process restarts without losing or duplicating work.

Deliverables:

- Start work, restart wakeplaned, confirm lease behavior.
- Test clean shutdown and forced stop where possible.
- Verify pending, running, failed, and completed states after restart.
- Confirm status --watch shows recovery clearly.

Success gate:

- Completed work is not repeated.
- Claimed-but-unfinished work is either completed or reclaimed according to lease rules.
- Operator can tell what happened from status, run history, receipts, and audit logs.

6. Backup and restore drill

Goal: prove recovery from data loss or host movement.

Deliverables:

- Backup SQLite DB and Postgres DB.
- Restore into a fresh local path or fresh Postgres database.
- Start Wakeplane against restored data.
- Verify schedules, run history, receipts, audit logs, retention settings, and status output.
- Write a restore receipt.

Success gate:

- Restored instance is usable.
- Operator can verify integrity from CLI/API output.
- Docs explain the restore path without hidden local assumptions.

Recommended order:

Postgres dialect -> claim/retry verification -> migration/operator docs -> soak drill -> restart drill -> backup/restore drill.
