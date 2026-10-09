# Stable contract

This page defines the compatibility contract for Wakeplane `v1.0.0` and the 1.x stable release line. See [Status](status.md) for supported scope and evidence, and [Install](install.md) for versioned downloads and checksum verification.

## Supported deployment

The stable product is the standalone `wakeplaned` daemon, the `wakeplane` operator CLI, and the documented `/v1/...` REST API and MCP tools on a trusted single-operator installation. Use one daemon replica in one region, with SQLite on durable local storage or Postgres. Keep the API protected by a configured bearer token and private networking or an authenticated TLS gateway. See [Security](security.md) and [Hosting](hosting.md).

The console is an operator interface over the same API. Its visual layout, internal JavaScript functions, and HTML structure are not automation contracts. Go packages under `internal/...` and the embedding example remain source-level integrations outside the public semver boundary. There is no stable public Go library API in 1.0.

This supported topology has bounded single-operator validation, not an arbitrary-workload throughput or latency SLA. Measure capacity with your expected cadence, execution time and retained history before increasing load.

The separately packaged automation runner is a bounded reference adapter with a 500-job retained-state limit, one replica, and separately persisted state. Its documented HTTP job protocol is the integration contract; it is not a general connector service or a provider SLA. Gmail/Discord credentials, quotas, and availability remain provider/operator responsibilities.

## Compatibility within 1.x

| Surface         | Promise                                                                                                                           | Client responsibility                                                                                                        |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| REST routes     | Existing documented methods, paths, request fields, response-field meanings, and error codes remain compatible                    | Handle non-JSON HTTP errors and ignore additional response fields                                                            |
| MCP tools       | Existing documented tool names, input-field types/meaning, and structured-result meaning remain compatible                        | Discover schemas with `tools/list`; distinguish protocol errors from `isError` tool results                                  |
| Configuration   | Documented daemon flags and environment-variable meanings remain supported                                                        | Use explicit values for security, persistence and operator identity; follow the generated configuration reference            |
| CLI             | Documented command names, argument meaning, flags, and successful/non-successful exit behavior remain compatible                  | Parse JSON output for automation; human status/help/error wording and whitespace may change                                  |
| Manifests       | Existing documented YAML/JSON field names, types, enum meanings, and creation defaults remain accepted                            | Specify a timezone and typed target; reject unexpected fields in your own tooling rather than relying on permissive decoding |
| Run ledger      | Documented status meanings, attempt numbering, occurrence identity, and retry/reconciliation behavior remain compatible           | Preserve identities returned by the server; distinguish local outcome, remote status, provider acceptance, and receipt       |
| Storage upgrade | Supported forward upgrades preserve existing schedule IDs and retained ledger/checkpoint/event data through documented migrations | Take a native database backup and paired runner-state backup; do not run an older binary on a migrated database              |

The initial supported upgrade baseline is the final `v0.3.0-beta.3` schema and its paired runner state into `v1.0.0`, followed by documented forward 1.x upgrades. Earlier beta migrations must be verified separately before use; retaining their old binaries is not permission to downgrade a migrated store.

A 1.x minor release may add opt-in endpoints, commands, flags, fields, metrics, or policies. New fields must not change the meaning of an existing request. Changing existing enum semantics, defaults, required fields, CLI usage, or supported manifest fields requires a major release or an explicitly separate versioned surface. Patch releases correct behavior to the documented contract; the release notes identify any operational effect.

JSON object order and pretty printing are not guaranteed. IDs, occurrence keys, worker IDs, cursor encoding, internal SQL tables, log messages, and database implementation details are opaque. Do not derive a timestamp or provider identity by splitting an identifier. Timestamp fields use RFC3339 UTC instants; input timestamps include an offset. Preserve server-provided occurrence keys as idempotency keys.

Paginated response `items` is always an array, including `[]` for an empty result. Pagination is newest-first by creation time and ID. A cursor is for the same endpoint/filter set and does not freeze a snapshot. Clients handle new response fields and unknown future values defensively; existing 1.0 enum meanings will not be repurposed.

## Assistant tool contract

`POST /v1/mcp` is stateless Streamable HTTP with request/response tools, shared bearer auth, and request auditing. The supported protocol revisions are `2025-11-25`, `2025-06-18`, and `2025-03-26`. Clients initialize/negotiate and discover typed schemas. GET/DELETE return `405` in this stateless transport; server push, persistent sessions and MCP Tasks are not supported. An Origin header, when present, must match the endpoint origin.

The stable tool names are:

- `wakeplane_status`, `wakeplane_list_schedules`, `wakeplane_get_schedule`
- `wakeplane_create_schedule`, `wakeplane_update_schedule`, `wakeplane_pause_schedule`, `wakeplane_resume_schedule`, `wakeplane_trigger_schedule`
- `wakeplane_list_runs`, `wakeplane_get_run`, `wakeplane_preview_schedule`, `wakeplane_send_event`

Their request fields and results follow the [Assistant integration](automation.md) and discoverable schemas. Existing argument types and meanings remain compatible; descriptive text, titles and schema formatting are not machine contracts. Application failures return a tool result with `isError: true` and a structured error category; protocol/transport errors remain separate. Preview and inspection do not execute work. Creation/manual trigger are not idempotent: inspect current state before retrying an uncertain mutation.

New schedules are disabled by default. Tool annotations/instructions do not enforce human authorization at the server: the assistant/client must obtain authorization before creating, replacing, enabling, triggering or delivering events. The server enforces the configured bearer boundary and scheduling policy, not per-user roles.

## Schedule and execution guarantees

- Cadence kinds are `cron` (five fields), `interval` (positive seconds), and `once` (absolute timestamp). Every schedule requires an explicit IANA timezone; `Local` is rejected. Cron evaluates local time: spring-forward gaps omit nonexistent slots and fall-back can produce two distinct UTC occurrences.
- The durable run ledger precedes execution. Scheduled occurrences have one identity; retries are separate attempt records under that identity, with no concurrent attempts for one occurrence.
- Run statuses are `pending`, `claimed`, `running`, `succeeded`, `failed`, `retry_scheduled`, `dead_lettered`, `cancelled`, and `skipped`. Terminal attempts are preserved; reconciliation can append remote evidence without changing the original outcome.
- Defaults are overlap `forbid`, misfire `run_once_if_late`, timeout `300` seconds, concurrency `1`, and exponential retry with `max_attempts: 0`, initial delay `30` seconds and maximum delay `900` seconds. Attempts `0`/`1` mean no retries; a larger maximum counts the first attempt.
- `skip` permits lateness up to one configured planner polling interval, including its exact boundary (default five seconds). Older occurrences are durably skipped. This intentionally corrects the pre-1.0 bug that skipped even an occurrence due exactly at the planner tick. `run_once_if_late` remains the default. See [Policies](policies.md) for catch-up limits and overlap behavior.
- Planner/dispatcher polling, workload execution, storage latency, and provider availability can delay work. The nominal timestamp is not an exact dispatch deadline or hard real-time guarantee.
- Pause affects future planning; it does not cancel accepted work. Resume does not replay the paused period. Delete removes local history/deduplication for that schedule and does not cancel remote work.
- HTTP job integrations must deduplicate by `Idempotency-Key` and retain lookup/status evidence. Retries resume the same remote identity. The control plane cannot guarantee exactly-once external side effects; ordinary HTTP/shell work may have completed before a lost response or crash. Design targets for idempotency.
- Native Discord/Gmail delivery with an ambiguous send remains visibly unconfirmed and is not automatically resent. Provider acceptance and actual inbox/channel receipt are separate facts.

## Upgrade, restore, and rollback

1. Read release notes and confirm supported versions, topology, free storage, and runner capacity. Review policy corrections before enabling old definitions on a new release.
2. Pause new planning and drain/reconcile active work. Stop the old daemon and runner before replacement; do not overlap replicas.
3. Capture a database-native backup and a matching runner-state backup on the quiescent installation. Schedule exports contain static authorization headers and are sensitive. Encrypt/restrict backups; keep tokens out of source, logs, and published receipts.
4. Restore the backups to an isolated destination and verify retained schedules, IDs, runs/attempts, receipts, audit rows, remote checkpoints, and event replay protection. Keep schedules/provider delivery disabled during the drill.
5. Start the new version against its supported store. Startup applies migrations. Check readiness, authenticated status, execution policy, and remote identity recovery before resuming.
6. For rollback, stop the upgraded processes and restore the pre-upgrade database and paired runner state before starting the previous binaries. Reconcile any provider work that happened after capture; do not blindly replay it.

SQLite backup uses its backup API or a stopped database; copying a live WAL database file is not a complete backup. Postgres uses its native backup/restore tools. A quiescent paired capture is the tested procedure; an atomic snapshot across actively changing database and runner state is not promised.

Schedule export/import transfers definitions only, creates new IDs, and does not preserve runs, audit history, external checkpoints, or event deduplication. Import creates schedules sequentially and is not an all-or-nothing restore; a later failure can leave earlier definitions imported. An exported enabled definition can run on import: make a paused copy and review destinations before moving hosts. See [Storage](storage.md).

No automatic downgrade migration, arbitrary old-beta upgrade, distributed HA, public multi-tenant hosting, RBAC, provider account onboarding, or general workflow/DAG engine is part of the 1.0 promise.

## Evidence and release decision

The October 9, 2026 acceptance exercise ran three automatic one-minute occurrences for each native Discord/Gmail path. All six completed with distinct durable occurrence/job/provider identities, one attempt each, and no observed duplicate submissions. Actual Discord messages and Gmail INBOX receipts were verified, and the operator confirmed all three emails and Discord delivery/readability. The temporary schedules were bounded and paused afterward; regular schedules were preserved.

Recovery/outage/rate-limit/ambiguous-send, SQLite/Postgres restore, actual paired production backup/restore, and previous-version rollback checks have recorded evidence. This establishes the tested supported topology and workloads, not every future workload or cloud target. Railway has live operational evidence; AWS/GCP manifests and the experimental Cloudflare supervisor have packaging/configuration checks only. Continued normal-cadence observation is useful but there is no mandatory 30-day, weekly, or other calendar wait before release once the required checks pass.

Each stable release must pass code, real Postgres parity, generated/public documentation and packaging checks together. Its public archives, checksums and install paths are verified against the exact source tag; see [Release discipline](releasing.md).
