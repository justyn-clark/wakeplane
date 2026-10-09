# Production acceptance

This is an execution plan, not completed production evidence. Start the observation clock only after a protected hosted daemon, persistent runner storage, and real provider delivery have been verified. Use an acceptance window sized to the actual workload cadence, rather than a fixed number of days. Require automatic scheduled occurrences for both chosen delivery paths and complete the recovery, restore, and contract checks below. Duration alone does not establish readiness.

## Operator workloads

1. Weekday repository watch for `justyn-clark/wakeplane`, delivered to a configured Discord channel. Start with `examples/developer-repository-watch-discord.yaml`.
2. Weekly reading digest from selected RSS/Atom sources, delivered to the configured Gmail recipient. Start with `examples/personal-weekly-summary-email.yaml`.

Keep drafts paused until provider configuration and the first manual run are reviewed. Each schedule must have an explicit timezone and approved source/destination. Use a private authenticated runner origin. Neither workload writes to a repository or runs agent-generated commands.

## Initial acceptance

- Record deployment commit, image identity, database version/backend, platform service IDs, backup location, and restore procedure. Store only vault record names, never secret values, in the receipt.
- Verify unauthenticated operator requests fail, authenticated status succeeds, readiness responds, and shutdown drains workers.
- Trigger each workload, inspect Wakeplane's occurrence key and durable run/remote job identity, check progress and report artifacts, and record the provider message ID plus actual Discord message/email receipt. A Gmail API send confirmation proves provider acceptance; inbox placement is a separate observation.
- Restart both processes during collection and during polling. Confirm the remote job identity is preserved and no new submission is created for the same occurrence.
- Interrupt a native send with a lost response or restart: delivery must become visibly unconfirmed without automatically resending. Investigate provider history before creating another delivery.
- Exercise runner outages and rate limits. Confirm policy-backed retries, eventual recovery, and visible failures. Account for retries as attempts under the same occurrence, not duplicate scheduled work.
- Restore SQLite and Postgres backups into disposable environments. Compare schedules, runs, audit evidence, unresolved remote checkpoints, and runner state. Keep provider delivery disabled while inspecting a restored environment.
- Rehearse an upgrade with a pre-upgrade backup and rollback into an isolated environment. Do not test restore or schema rollback against the live database.

## Observation record

Record daily scheduled versus observed occurrences, terminal states, missed/late runs and reasons, retry/dead-letter counts, unconfirmed deliveries, restart/outage events, and backup health. Check retention and the runner's 500-job capacity. Explain every discrepancy; resolve any unexplained duplicate submission, missing follow-up, lost ledger state, or secret exposure before promotion.

Acceptance requires real automatic scheduled work, both chosen delivery paths demonstrated, recovery/restore evidence for the supported backends, and an explicit account of limitations. Repeated occurrences should establish that cadence, identities, and delivery behave as expected. Short drills establish specific fault behavior; they do not substitute for scheduled delivery evidence. There is no mandatory calendar waiting period once the required evidence is complete. Stable 1.0 additionally requires the contract decisions in [Status](status.md).
