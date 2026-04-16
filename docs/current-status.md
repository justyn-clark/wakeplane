# Wakeplane Current Status

As of 2026-04-15, Wakeplane is a coherent public-beta scheduling control plane with working planner, dispatcher, durable run ledger, typed executors, HTTP API, CLI, and embedding surface.

## What it is

Wakeplane is the control plane above cron-like cadence definitions. It decides when work is due, materializes each occurrence as a durable run record, claims execution through a dispatcher, enforces policy, and preserves append-only attempt history plus executor receipts.

This repository is not a reminder app, not a thin cron wrapper, and not a general workflow orchestrator. The current product boundary in code still matches the intended boundary:

- scheduler and planner materialize due work
- dispatcher claims and runs work
- the store owns durability and lease semantics
- policy is enforced before execution
- operator surfaces are HTTP and CLI

## Current deployments

Wakeplane was born in the OpenCLAW ACE Hermes personal-agent environment on a Mac Mini. That environment is still a real operational consumer and an important validation surface for single-machine scheduling, typed execution, and operator visibility.

Wakeplane is not documented as an ACE-only subsystem. The same product is expected to run:

- on the Mac Mini where that environment lives
- on a local machine such as a MacBook Air for personal scheduling and agent support
- as a standalone control plane other operators can run in their own environments

That means local-system safety matters. Regressing the single-node local deployment model would be a product regression, not an acceptable trade for future scale.

## How to use it today

Run from source:

```bash
go run ./cmd/wakeplane serve
```

Or build binaries:

```bash
go build -o dist/wakeplane ./cmd/wakeplane
go build -o dist/wakeplaned ./cmd/wakeplaned
```

Then:

1. Start the daemon with `WAKEPLANE_DB_PATH`, `WAKEPLANE_HTTP_ADDR`, and `WAKEPLANE_WORKER_ID`.
2. Create a schedule from YAML with `wakeplane schedule create -f <file>`.
3. Inspect schedules, runs, receipts, status, and metrics through the CLI or `/v1/...` API.
4. If using workflow targets, register handlers explicitly through `app.NewWithOptions(..., app.WithWorkflowHandler(...))`.

## Intent and Implementation Coherence

The code and product intent are aligned on the important boundaries:

- Durable-first execution: the dispatcher only starts work after `ClaimRun`.
- Duplicate protection: scheduled occurrence identity is deterministic and retries reuse `occurrence_key` with incremented attempts.
- Typed execution: targets are constrained to `http`, `shell`, and `workflow`.
- Timezone discipline: timezone is required and validated.
- Append-only audit shape: retries create new run rows and receipts are attached as separate artifacts.
- Operator legibility: health, readiness, status, metrics, receipts, and structured shutdown logging are present.

Documentation drift found in this audit was concentrated in the public docs, not the runtime:

- `once` schedules were documented as `schedule.run_at`, but the actual field is `schedule.at`.
- shell targets were documented with `env`, which is not implemented.
- one concepts page described attempts as 0-indexed, but the code starts at attempt `1`.
- some docs described `claimed_at`, but the runtime currently exposes claim ownership and lease expiry instead.

## Coherency Gaps

The main gaps are structural, not semantic:

- There are two doc surfaces, `docs/` and `docs/public/`, which increases drift risk.
- The public docs had examples that overstated target capabilities compared with the actual typed schema.
- The current repo explains alpha constraints across several files, but the hardening and scale path was not centralized before this audit.

## Hardening Opportunities

- Add authentication, authorization, and API-layer audit logging before any broader network exposure.
- Introduce retention and archival policy for runs, receipts, and dead letters so the append-only history remains operationally sustainable.
- Put explicit size limits and truncation rules around receipt payloads, especially shell stdout/stderr and workflow results.
- Expand store-level and lifecycle testing from correctness into load, long-run soak, and backup/restore verification.
- Tighten operator ergonomics around schedule mutation and export/import so the CLI is useful beyond basic create/list/get/trigger flows.

## Paths for Future Scale

- Add a Postgres dialect behind the existing store seam first. That is the narrowest scale lever already designed into the repo.
- After Postgres, move toward multi-process coordination only if lease, claim, and recovery semantics remain the source of truth in storage.
- Keep the executor boundary typed. If out-of-process workers are added, preserve the dispatcher and ledger model rather than collapsing into arbitrary blobs.
- Add retention, partitioning, and archival before chasing higher run volume so the ledger remains legible and bounded.
- Split daemon and operator binaries only when there is a real operational need, not pre-emptively.

## Recommended Next Steps

1. Finish documentation convergence: treat `docs/public` as the operator-facing source and keep internal design notes narrowly scoped.
2. Add receipt retention and size-bound behavior with tests.
3. Implement authn/authz and request audit logging ahead of any trusted-network expansion.
4. Build the Postgres backend at the existing store seam, then verify claim and retry behavior against a real Postgres instance.
5. Add scale-oriented verification: concurrency stress, restart recovery soak tests, and backup/restore drills.
