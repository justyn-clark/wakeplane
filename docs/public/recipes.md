# Runnable automation recipes

These examples require the `v0.3.0-beta.1` source line or a matching release. Wakeplane owns cadence, policy, durable run history, and remote tracking. The example runner owns source access, report generation, and notification delivery.

## Start the separate runner

From this repository:

```bash
go run ./examples/automation-runner
```

It binds to `127.0.0.1:8091`, persists jobs under `examples/automation-runner/state`, and exposes `POST /jobs`, `GET /jobs/{id}`, `GET /jobs/{id}/report`, and `GET /jobs/lookup` with an `Idempotency-Key` header. The manifests include this lookup address so a lost submission response remains recoverable. Keep that state directory across restarts: deleting it destroys the runner's idempotency history. Only one runner process can own a state directory. Its retained job capacity is 500; capacity exhaustion fails explicitly rather than silently removing replay protection.

In another terminal, start Wakeplane on localhost and open the console:

```bash
WAKEPLANE_HTTP_ADDR=127.0.0.1:8080 wakeplane serve
```

Choose **Create automation**, select a recipe, supply the runner URL, review timezone and timing preview, then save the paused draft. Enable it when ready. A manual trigger is also available for inspecting a first run. Watch its remote progress, open the result, and inspect receipts.

## Developer: repository activity

`examples/developer-repository-watch.yaml` calls the runner each weekday. It reads repository metadata, the latest published stable release when available, and up to five recently updated open pull requests. It produces source links and activity counts. GitHub's latest-release endpoint excludes prereleases; a beta-only repository can correctly report no stable release.

```bash
wakeplane schedule create -f examples/developer-repository-watch.yaml
```

Change `body.repository` to an `owner/name` you can access and choose your timezone. For private repositories or higher API limits, supply the existing provider token through `AUTOMATION_RUNNER_GITHUB_TOKEN` in the runner's environment. The adapter makes read-only GitHub calls; it does not edit code, open PRs, or merge work. A provider or rate-limit failure becomes an explicit failed job.

An authenticated event adapter can trigger this same enabled schedule immediately using a stable source delivery ID. Normal cadence continues independently. Verify provider signatures in that adapter before calling Wakeplane's event endpoint; see [Automation](automation.md).

## Personal: weekly reading list

`examples/personal-weekly-summary.yaml` collects up to twenty recent entries from up to five RSS or Atom feeds each Monday. Replace the sample feed URLs with your chosen sources and choose your timezone.

```bash
wakeplane schedule create -f examples/personal-weekly-summary.yaml
```

The report contains feed excerpts and links. It does not claim to have read full articles or used an AI model. An external runner can add a model-generated summary while retaining this job contract; manage its credentials, budget, and evaluation in that runner.

## Notifications

Set `body.notify_url` to a trusted receiver to send the report after collection. The example posts JSON with `job_id`, `task`, and a `report` containing `title`, `generated_at`, `summary`, and `items`. It retries delivery up to three times using one stable `Idempotency-Key`. The receiver must durably honor that key to prevent duplicate messages after a lost response or restart.

The completed result records `delivery.status` and attempt count. A collected report can succeed while its notification is visibly failed; check that field rather than treating collection success as proof of delivery. Delivery attempts and pending delivery survive runner restart.

This generic JSON payload is suitable for an adapter in n8n, Make, Zapier, or your own service. Slack, Discord, email, and other providers need an adapter that maps it to their API format and owns account credentials; their native webhook formats are not automatically compatible.

## Configuration and limits

| Runner setting                    | Purpose                                                                             |
| --------------------------------- | ----------------------------------------------------------------------------------- |
| `AUTOMATION_RUNNER_ADDR`          | Listen address; default `127.0.0.1:8091`                                            |
| `AUTOMATION_RUNNER_PUBLIC_URL`    | Absolute origin reachable from Wakeplane; must match the submit origin for tracking |
| `AUTOMATION_RUNNER_STATE_DIR`     | Durable job/idempotency state directory                                             |
| `AUTOMATION_RUNNER_TOKEN`         | Optional bearer token protecting job and report routes                              |
| `AUTOMATION_RUNNER_GITHUB_URL`    | GitHub API base; default public API, also useful for local fixtures                 |
| `AUTOMATION_RUNNER_GITHUB_TOKEN`  | Existing provider credential for GitHub source requests                             |
| `AUTOMATION_RUNNER_NOTIFY_TOKEN`  | Optional notification bearer token                                                  |
| `AUTOMATION_RUNNER_NOTIFY_ORIGIN` | Required matching origin when a notification token is configured                    |

If runner authentication is enabled, configure the matching static HTTP authorization header in the schedule. The console artifact link opens a separate browser tab; it does not attach custom runner bearer headers. The result remains inspectable in Wakeplane, or fetch the runner report with an authenticated client.

This bounded single-process example has no account onboarding, provider secret store, model spending controls, remote cancellation, automatic history pruning, or multi-instance state coordination. Source requests reject redirects, responses are bounded, and credentials are scoped to their configured source/origin. Use a production adapter for workloads needing stronger connector guarantees.
