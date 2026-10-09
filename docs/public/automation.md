# Assistant and external automation

Wakeplane keeps scheduling, policy, durable occurrence identity, and operator history separate from the service that executes the work. These capabilities are available in the published `v0.3.0-beta.3` beta.

## Assistant access

Connect a Streamable HTTP MCP client to your daemon's `/v1/mcp` endpoint. Supply `Authorization: Bearer <operator-token>` when `WAKEPLANE_AUTH_TOKEN` is configured. Use the client's header or secret configuration; do not place credentials in a committed configuration file.

The MCP surface exposes status, schedule listing and inspection, draft preview, create, full replacement update, pause, resume, manual trigger, run listing/inspection, and deduplicated event delivery. It uses the same application service, validation, policy, authentication, and request auditing as REST. It does not run shell commands directly or bypass a durable run record.

New schedules default paused. Require an explicit IANA timezone such as `America/Los_Angeles`. Inspect the run after triggering to see its final outcome. Create and manual trigger are not idempotent; inspect current state before repeating a mutation after a lost response. Use event delivery when a source supplies a stable event ID.

Tested protocol revisions are `2025-11-25`, `2025-06-18`, and `2025-03-26`, using the official Go MCP client and server SDK. This is a stateless request/response endpoint without server push, experimental MCP Tasks, or an OAuth authorization server. Native clients with configurable bearer headers are the supported integration path; a particular hosted assistant may require additional OAuth infrastructure.

## Tracked remote jobs

An HTTP target with `http_job` uses a durable runner contract. Plain HTTP targets retain their existing response-status semantics.

```yaml
target:
  kind: http
  method: POST
  url: http://127.0.0.1:8091/jobs
  http_job:
    poll_interval_seconds: 5
    lookup_url: http://127.0.0.1:8091/jobs/lookup
  body:
    task: repository-watch
    repository: justyn-clark/wakeplane
```

Wakeplane records submission intent, an immutable target snapshot, and an absolute deadline before sending a request. It sends `Idempotency-Key: <occurrence_key>`. The runner must durably deduplicate that key. A process can crash after the remote side accepts work but before Wakeplane stores the response; the stable key closes that replay window when the runner honors the contract.

The submit response must include a non-empty `job_id`, an absolute same-origin `status_url`, and an explicit `status` (`queued`, `running`, `succeeded`, `failed`, or `cancelled`). GET on `status_url` returns the same identity plus status, optional progress, result, artifacts, and error. A `202` or other successful HTTP status does not mean the job succeeded.

```json
{
  "job_id": "job-123",
  "status_url": "http://127.0.0.1:8091/jobs/job-123",
  "status": "running",
  "progress": { "percent": 40, "message": "Collecting repository activity" }
}
```

The executor rejects redirects and cross-origin status URLs, bounds runner responses, and checkpoints accepted identity and progress. After restart or lease recovery, it polls the persisted identity instead of creating fresh work. Retries retain the original deadline and target. `GET /v1/runs/{id}` exposes `external_job`; the console shows progress and final outputs.

`overlap=replace` is rejected for tracked remote jobs because this contract has no remote cancellation operation. A timeout or tracking failure does not imply that the remote job stopped. There is no built-in checkpointing of an agent's internal steps; use the runner's workflow/agent engine for that responsibility.

Unresolved remote jobs continue counting toward overlap and concurrency limits, including during polling retries and after local timeout. For a locally terminal run, `POST /v1/runs/{id}/reconcile` performs one status observation without submission, appends a receipt, and releases capacity only when the runner confirms a terminal status. The original local run outcome stays unchanged.

Retention keeps unresolved attempts inspectable for recovery. Once every attempt for a confirmed terminal job is pruned, its checkpoint becomes a compact identity/status/deadline tombstone with target headers, body, results, artifacts, and progress removed.

If acceptance happens but the submission response is lost, bounded retries use the same submission key. Configure optional `http_job.lookup_url` before the first submission to support later observation by key when no job identity was returned. Reconciliation sends a GET with `Idempotency-Key`; the lookup must return the same job response shape. A 404 remains unresolved and retains the overlap slot. Without a lookup capability, exhausted submission retries can leave an unknown job requiring runner-side investigation. No recovery action silently resubmits that work.

## Event delivery

POST to `/v1/schedules/{id}/events` with a stable ID and source:

```json
{
  "id": "delivery-unique-123",
  "source": "github",
  "data": { "action": "opened", "pull_request_number": 42 }
}
```

The first delivery returns `201` with `{run,created:true}`. An identical delivery returns `200` and the original run. Changed data under the same identity returns `409`. New events require an enabled schedule; existing redelivery remains inspectable after pause. Events do not change normal cadence, and dispatch still enforces overlap, timeout, and concurrency policy.

Event identity is scoped to schedule, source, and ID. A retained deduplication record prevents a delayed delivery from executing again after run pruning; the API returns `410` when the original history is gone. These records remain until schedule deletion, so operators should account for their storage separately from terminal-run retention.

For tracked HTTP jobs, the body includes a reserved `_wakeplane_event` envelope. HTTP execution also supplies reserved event identity headers. Configure application adapters to validate provider signatures and translate events into this authenticated API. This endpoint is not a public GitHub webhook receiver and does not claim provider signature validation.

See [Recipes](recipes.md) for complete runnable examples and notification boundaries.
