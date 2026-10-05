# Wakeplane Current Status

As of 2026-10-05, the source version is `0.3.0-beta.1`. The last published release remains `v0.2.0-beta.1` until the matching new tag and archives are published. Source capability and public availability are separate claims.

## Implemented product surface

Wakeplane preserves its scheduling-control-plane boundary: the planner materializes occurrences, the dispatcher enforces policy, and storage records ownership, attempts, receipts, and recovery before execution. SQLite and Postgres are supported. HTTP, shell, and explicitly registered Go workflow targets remain typed.

The source adds:

- Guided schedule creation and editing, paused drafts, explicit timezones, recipes, and non-mutating timing previews.
- A stateless MCP endpoint that shares REST authentication, auditing, validation, and application operations.
- Opt-in tracked HTTP jobs with persisted identity, submission intent, deadlines, progress, results, artifacts, fenced ownership, and recovery.
- Deduplicated events whose identity survives run retention; event payloads reach HTTP adapters without losing large JSON numbers.
- A separate example runner for repository activity and weekly RSS/Atom reading lists, with durable deduplication and optional notification delivery.
- An operator reconciliation action for unresolved remote work after local tracking has ended.

The May backend improvements are included: optional single-operator bearer authentication, request audit, bounded receipts, terminal run retention, export/import, and an embedded console.

## Operational boundaries

A successful submit response is not a completed job. Remote side effects require the runner to honor the stable idempotency key. Tracking timeout and daemon shutdown do not cancel remote work. Unresolved jobs continue consuming overlap capacity until authoritative completion is observed. Operators can reconcile them without resubmitting or rewriting the original outcome.

Terminal external checkpoints are compacted when their run history is pruned. Unresolved jobs retain the data needed for recovery; event identity tombstones survive until schedule deletion. Back up before upgrades and retain an older-version backup for rollback.

The product remains beta. It has no RBAC, multi-tenancy, OAuth account onboarding, provider cost enforcement, distributed coordination, DAG engine, or remote cancellation contract. Hosted assistants that require OAuth need additional infrastructure. The example runner is a bounded single-process demonstration, not a replacement for a production connector or agent engine.

## Validation and release discipline

CI and the release workflow require formatting/lint, console tests, Go vet, race tests, Postgres integration, generated documentation, and version-matched archive smoke checks. Local evidence is recorded in `.small/progress.small.yml`; historical deployment receipts are not evidence of the new features being deployed.

See [Automation](public/automation.md), [Recipes](public/recipes.md), [Console](public/console.md), and the [release notes](public/releases/v0.3.0-beta.1.md). Longer operator soak, a published release, and hosted-documentation synchronization remain distinct promotion steps; beta is not a 1.0 guarantee.
