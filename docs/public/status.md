# Status

Wakeplane's release status describes supported scope and evidence, rather than a calendar waiting period.

## Current public state

Wakeplane `v1.0.0` is the stable single-operator release line for the standalone daemon, CLI, REST API, and MCP tools described in [Stable contract](stable-contract.md). It includes tracked automation, native notification formatting, deployment adapters, and the pre-1.0 correctness fixes documented in the [release notes](releases/v1.0.0.md). Use [Install](install.md) for versioned archives and checksums.

The legacy `v0.2.0-beta.1` lacks built-in auth, request audit, receipt/retention controls, Postgres, the operator console, and newer automation/operator routes. Setting `WAKEPLANE_AUTH_TOKEN` does not protect that older binary. Use a current release and follow [Install](install.md) and [Security](security.md).

## What 1.0 supports

- A single operator on trusted infrastructure, one daemon replica, and SQLite or Postgres persistence.
- The documented REST API and MCP tools, CLI commands and schedule manifests, typed targets, durable attempts, execution policy, and remote-job tracking.
- Explicit compatibility, forward-upgrade, backup/restore, rollback, and security expectations in [Stable contract](stable-contract.md).
- A separate bounded automation runner, including native Discord/Gmail delivery with provider acceptance and actual delivery evidence distinguished.

Go `internal/...` embedding remains a source integration outside the stable public API. RBAC, multi-tenancy, distributed HA, automatic provider account onboarding, and a general workflow/DAG engine are outside 1.0.

## Acceptance evidence

On October 9, 2026, three automatic one-minute occurrences per native channel completed successfully: six durable occurrences, six remote identities, six provider identities, one attempt each, and no observed retries or duplicates. Actual Discord messages and Gmail INBOX receipts were verified. The operator confirmed receipt and improved formatting. Temporary test schedules are paused; regular schedules are unchanged.

Recovery, outage/rate-limit/ambiguous-send behavior, SQLite/Postgres restore, a paired actual production backup/restore, and previous-version rollback have recorded checks. The earlier Friday Discord occurrence skipped by the beta `skip` policy remains a historical failure, and was not replayed or relabeled successful. The regular schedules were corrected to `run_once_if_late`; `v1.0.0` additionally corrects `skip` to tolerate one configured planner polling interval.

These results close scheduled-delivery and recovery/restore acceptance for the tested single-operator topology. They do not establish a provider SLA, every workload, or live Cloudflare/AWS/GCP operation. Railway is the verified deployment; the other adapters retain explicit verification limits in [Hosting](hosting.md).

There is no mandatory 30-day or weekly observation wait. Automatic short schedules provide the required delivery evidence; normal recurring observation continues as operational follow-up. See [Production acceptance](production-acceptance.md).

## Stable release discipline

Every stable release follows the same evidence requirements:

1. Preserve the supported public contracts and document intentional behavior corrections.
2. Pass build, race/unit tests, real Postgres parity, formatting/lint, generated-doc and public-example checks on the final source commit.
3. Verify the upgrade/restore/rollback instructions against the supported topology and retain the acceptance receipts.
4. Publish matching source, release notes, documentation, platform archives and checksums; verify the actual downloads and install paths.

Earlier beta tags remain pre-stable. The stable claim is bounded by the 1.x contract and verified release artifacts, not by elapsed calendar duration.
