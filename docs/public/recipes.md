# Runnable automation recipes

These examples require `v0.3.0-beta.3` or its matching source build. Wakeplane owns cadence, policy, durable run history, and remote tracking. The example runner owns source access, report generation, and notification delivery.

## Start the separate runner

Each `v0.3.0-beta.3` archive includes `automation-runner` alongside the CLI and daemon. Run the extracted binary, or use a matching source checkout:

```bash
./automation-runner
# Or, from the repository:
go run ./examples/automation-runner
```

It binds to `127.0.0.1:8091`, persists jobs under `examples/automation-runner/state`, and exposes `POST /jobs`, `GET /jobs/{id}`, `GET /jobs/{id}/report`, and `GET /jobs/lookup` with an `Idempotency-Key` header. The manifests include this lookup address so a lost submission response remains recoverable. Keep that state directory across restarts: deleting it destroys the runner's idempotency history. Only one runner process can own a state directory. Its retained job capacity is 500; capacity exhaustion fails explicitly rather than silently removing replay protection.

In another terminal, start Wakeplane on localhost and open the console:

```bash
WAKEPLANE_HTTP_ADDR=127.0.0.1:8080 wakeplane serve
```

Open `http://127.0.0.1:8080/` (which redirects to `/console/`), select **Schedules**, then choose **Use template** on a recipe card. Supply the runner URL and task details, review timezone and timing preview, then save the paused draft. Enable it when ready. A manual trigger is also available for inspecting a first run. Watch its remote progress, open the result, and inspect receipts.

## Developer: repository activity

`examples/developer-repository-watch.yaml` calls the runner each weekday. It reads repository metadata, the latest published stable release when available, and up to five recently updated open pull requests. It produces source links and activity counts. GitHub's latest-release endpoint excludes prereleases; a beta-only repository can correctly report no stable release.

```bash
wakeplane schedule create -f examples/developer-repository-watch.yaml
```

Change `body.repository` to an `owner/name` you can access and choose your timezone. For private repositories or higher API limits, supply the existing provider token through `AUTOMATION_RUNNER_GITHUB_TOKEN` in the runner's environment. The adapter makes read-only GitHub calls; it does not edit code, open PRs, or merge work. A provider or rate-limit failure becomes an explicit failed job.

An authenticated event adapter can trigger this same enabled schedule immediately using a stable source delivery ID. Normal cadence continues independently. Verify provider signatures in that adapter before calling Wakeplane's event endpoint; see [Automation](automation.md).

## Personal: weekly reading list

`examples/personal-weekly-summary.yaml` collects up to twenty recent entries from up to five RSS or Atom feeds each Monday. The current examples default to the Wakeplane release feed used in delivery verification. Replace it with your chosen sources and choose your timezone; each feed must fit the runner's 256 KiB response limit.

```bash
wakeplane schedule create -f examples/personal-weekly-summary.yaml
```

The report contains feed excerpts and links. It does not claim to have read full articles or used an AI model. An external runner can add a model-generated summary while retaining this job contract; manage its credentials, budget, and evaluation in that runner.

## Notifications

Set `body.notify_url` to a trusted receiver to send the report after collection. The example posts JSON with `job_id`, `task`, and a `report` containing `title`, `generated_at`, `summary`, and `items`. It retries delivery up to three times using one stable `Idempotency-Key`. The receiver must durably honor that key to prevent duplicate messages after a lost response or restart.

The completed result records `delivery.status` and attempt count. A collected report can succeed while its notification is visibly failed; check that field rather than treating collection success as proof of delivery. Delivery attempts and pending delivery survive runner restart.

This generic JSON payload is suitable for an adapter in n8n, Make, Zapier, or your own service. Native Discord and Gmail delivery is included in `v0.3.0-beta.2`; `v0.3.0-beta.1` requires an external adapter for those channels. Other providers still need an adapter.

## Discord and email delivery

Set `body.notify_channel` to `discord` or `email`, and omit `notify_url`. The destination and provider credentials are configured on the runner, never in schedule payloads. Each job selects one delivery channel; create separate schedules when both are needed.

- `examples/developer-repository-watch-discord.yaml`: paused weekday repository watch delivered to a configured Discord channel.
- `examples/personal-weekly-summary-email.yaml`: paused weekly digest delivered to a configured email recipient.

For Discord, configure `AUTOMATION_RUNNER_DISCORD_WEBHOOK_URL` from your secret store. The runner accepts only an HTTPS `discord.com` API webhook URL, requests server confirmation with `wait=true`, suppresses mentions, and bounds message length. Full reports remain available as runner artifacts.

For Gmail, configure `AUTOMATION_RUNNER_GMAIL_FROM`, `AUTOMATION_RUNNER_GMAIL_TO`, `AUTOMATION_RUNNER_GMAIL_CLIENT_ID`, `AUTOMATION_RUNNER_GMAIL_CLIENT_SECRET`, and `AUTOMATION_RUNNER_GMAIL_REFRESH_TOKEN`. FROM and TO each accept one plain email address. Authorize the Gmail account using a server-side OAuth flow with offline access and the `https://www.googleapis.com/auth/gmail.send` scope. Refresh credentials are sent only to Google's token endpoint; email requests go only to the Gmail API. Keep the refresh token and client secret in your shared vault or hosting secret variables. Connected Gmail access in an assistant does not provision credentials for this process. See [Google's authorization guide](https://developers.google.com/workspace/gmail/api/auth/web-server).

Release `v0.3.0-beta.3` and matching source builds send Gmail reports as a responsive HTML digest with a separate plain-text alternative. Notification copy uses ASCII characters: punctuation is normalized, Latin accents are simplified, and unsupported characters are omitted. Source links retain their destinations through URL encoding; the original structured report remains available unchanged. Discord messages also use ASCII copy. The older `v0.3.0-beta.2` archives send plain-text email.

Confirmed native sends persist a `delivery.provider_message_id`. Discord and Gmail do not promise deduplication for these requests. A transport error, server error, missing confirmation, or restart during a send produces `delivery.status = "unknown"` and is not automatically resent. Investigate the provider before arranging a new delivery. Explicit rate-limit rejections can retry within the existing three-attempt limit. The collected report can still succeed while delivery is `failed` or `unknown`; collection success does not prove receipt or inbox placement. Generic webhook receivers retain the original idempotency-key contract.

The Discord and email schedule examples use `run_once_if_late` so a normal planner delay still runs the latest report. Older overdue reports are skipped. With `skip`, every occurrence already due when the planner checks is discarded, including a report picked up only one second late.

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
