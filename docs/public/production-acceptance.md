# Production acceptance

This page records completed single-operator acceptance evidence and the procedure for repeating it. It does not claim every hosting adapter or future workload is production-accepted. Release compatibility and final publication checks remain separate; see [Stable contract](stable-contract.md).

## Completed acceptance: October 9, 2026

| Gate                         | Evidence                                                                                                                          | Boundary                                                                                                         |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Automatic native Discord     | Three one-minute occurrences; distinct durable run/job/provider identities; actual messages verified                              | Tested private channel and repository-watch workload                                                             |
| Automatic native Gmail       | Three one-minute occurrences; distinct durable run/job/provider identities; actual INBOX and SENT receipts verified               | Provider acceptance was checked separately from inbox placement                                                  |
| Human receipt/readability    | Operator confirmed all three emails and Discord messages, and improved email formatting                                           | All six notifications verified as ASCII; email has plain/HTML alternatives                                       |
| Bounded automatic run window | Six occurrences, one attempt each, no observed retries or duplicate submissions, all completed within 4.2 seconds of nominal time | Test copies exhausted their finite window and were paused; regular definitions preserved                         |
| Recovery and fault handling  | Restart/remote identity, outages, rate limits and ambiguous native-send checks recorded                                           | Ambiguous sends remain unconfirmed and are not blindly resent                                                    |
| Restore and rollback         | SQLite/Postgres restore, actual paired production Postgres/runner-state restore, and previous-version rollback checks recorded    | Paired capture tested while idle; restores isolated with delivery disabled                                       |
| Hosted topology              | Authenticated/private Railway daemon, separate persistent runner and Postgres                                                     | AWS/GCP adapters and experimental Cloudflare supervisor have configuration/packaging checks, not live acceptance |

The earlier October 9 weekday Discord occurrence was a real skipped delivery caused by the beta `skip` behavior. It remains in the ledger and was not replayed. Both regular delivery schedules were corrected to `run_once_if_late`; the new automatic tests prove the corrected path. Historical oversized-feed onboarding and local probe-parser failures are distinct from this acceptance exercise.

No mandatory 30-day, weekly, or other calendar wait remains. Short automatic schedules established real delivery; recovery/restore checks established specific fault behavior. Normal recurring schedules remain useful monitoring, rather than a prerequisite to declaring the tested gates complete. Each release must additionally pass its code, documentation, packaging and public archive checks together.

## Repeatable procedure

Begin only after protected networking, durable storage and real provider configuration are verified. Use enough automatic occurrences to demonstrate the workload and examine the evidence, rather than choosing an arbitrary number of days. Temporary short-cadence copies should retain the real timezone, target and policy, have an explicit end bound, and be paused afterward. Do not rewrite the regular schedule or replay failed historical work merely to make the record green.

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

Record each scheduled versus observed occurrence, terminal states, missed/late runs and reasons, retry/dead-letter counts, unconfirmed deliveries, restart/outage events, and backup health. Daily summaries may aggregate those records; do not discard individual identities or historical failures. Check retention and the runner's 500-job capacity. Explain every discrepancy; resolve any unexplained duplicate submission, missing follow-up, lost ledger state, or secret exposure before promotion.

Acceptance requires real automatic scheduled work, both chosen delivery paths demonstrated, recovery/restore evidence for the supported backends, and an explicit account of limitations. Repeated occurrences should establish that cadence, identities, and delivery behave as expected. Short drills establish specific fault behavior; they do not substitute for scheduled delivery evidence. There is no mandatory calendar waiting period once the required evidence is complete. Stable 1.0 additionally requires the contract decisions in [Status](status.md).
