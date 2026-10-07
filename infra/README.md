# Deployment adapters

Railway is the primary deployment target. The same Go container runs on Kubernetes with AWS EKS and Google GKE storage overlays. The Cloudflare adapter is an experimental, API-only daemon supervisor; it requires external Postgres and a separately hosted durable runner.

| Target                | Adapter                                                   | Verification boundary                                                                                                                        |
| --------------------- | --------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Railway               | `Dockerfile`, `railway.json`, `infra/railway/runner.json` | Container build, UID 10001, root-mounted runner volume and persistent restart are checked in CI. Live infrastructure acceptance is separate. |
| AWS EKS               | `infra/kubernetes/aws`                                    | Rendered manifests, Kubernetes schema and singleton/storage/security invariants. No AWS account resources are created by CI.                 |
| Google GKE            | `infra/kubernetes/google-cloud`                           | Same checks with Persistent Disk CSI. No Google Cloud resources are created by CI.                                                           |
| Cloudflare Containers | `infra/cloudflare`                                        | Worker bundle/configuration and supervisor tests. No live Cloudflare durability, timing, resource or recovery claim.                         |

All adapters preserve separate daemon and runner processes, durable occurrence recording, policy enforcement and explicit timezones. These are single-operator deployments, not multi-replica HA configurations. Resource requests and limits are starting points requiring workload measurement.

## Kubernetes: AWS and Google Cloud

Use an existing supported cluster; these adapters do not create clusters, databases, accounts, gateways or billable resources until an operator applies them. Build and publish the image to your registry. Pin an immutable tag or digest rather than `latest`.

```sh
docker build -t YOUR_REGISTRY/wakeplane:YOUR_COMMIT .
docker push YOUR_REGISTRY/wakeplane:YOUR_COMMIT
cd infra/kubernetes/aws # or ../google-cloud
kubectl kustomize .
kustomize edit set image wakeplane:local=YOUR_REGISTRY/wakeplane:YOUR_COMMIT
```

Install the AWS EBS CSI driver and its IAM permissions for EKS, or enable the GKE Persistent Disk CSI driver. The overlays use encrypted AWS gp3 volumes or GCP pd-balanced disks, delayed binding and `Retain` reclamation. A default `ReadWriteOnce` claim alone does not prohibit two pods on the same node: `replicas: 1` and `Recreate` are also required. Do not add an HPA, increase replicas or change to rolling updates.

Provision `daemon-secrets` and `runner-secrets` in namespace `wakeplane` through your approved secret manager. The daemon secret requires `WAKEPLANE_AUTH_TOKEN`; the runner secret requires `AUTOMATION_RUNNER_TOKEN`. Optional provider settings belong only in the runner secret. The manifests deliberately contain no Secret values and fail to start when required Secret objects are absent.

```sh
kubectl create namespace wakeplane # once, before provisioning secrets
kubectl apply -k infra/kubernetes/aws # from repository root
kubectl -n wakeplane rollout status deployment/daemon
kubectl -n wakeplane rollout status deployment/runner
kubectl -n wakeplane port-forward service/daemon 8080:8080
```

Both services remain ClusterIP. Use local port forwarding, a VPN or an authenticated TLS gateway for operator access. Keep the runner private. Only the runner's own persistent volume stores its jobs; the daemon's volume stores SQLite. Postgres is already supported by the daemon: an operator can set `WAKEPLANE_STORE=postgres` and `WAKEPLANE_DATABASE_URL` from a Secret, using RDS or Cloud SQL with appropriate TLS, networking and backup configuration. Postgres does not remove the runner-volume requirement. This adapter does not configure RDS or Cloud SQL automatically.

Before a host migration: pause schedules, drain work, back up both ledgers, stop the old processes, restore into the destination and verify identities before resuming. Never run the old and new schedulers against the same workload. Rehearse restores into isolated storage with provider delivery disabled. Retained disks survive claim deletion, but require explicit recovery and billing management.

Sources: [EKS EBS CSI](https://docs.aws.amazon.com/eks/latest/userguide/ebs-csi.html), [GKE persistent volumes](https://docs.cloud.google.com/kubernetes-engine/docs/concepts/persistent-volumes), [Kubernetes deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/).

## Cloudflare Containers: experimental daemon adapter

This is an API-only supervisor for the Go daemon. It does not port the core scheduler into a Worker and does not run the file-backed automation runner on ephemeral container disk. Host the runner on durable storage elsewhere, and give HTTP targets a protected, authenticated HTTPS runner origin reachable from Cloudflare. Railway private DNS is not reachable from Cloudflare. Protect the database and runner connectivity separately.

Requirements: Workers Paid and Containers access, externally reachable Postgres with appropriate TLS, a durable runner, and an operator-controlled route. `workers_dev` is disabled. Configure a route/custom domain in Wrangler and provision `WAKEPLANE_AUTH_TOKEN` and `WAKEPLANE_DATABASE_URL` with `wrangler secret put --config infra/cloudflare/wrangler.json`; never put them in `vars` or Git. The two tokens for daemon and runner remain separate.

```sh
pnpm install --frozen-lockfile
pnpm run check:cloudflare # dry-run; builds image; requires Docker
pnpm exec wrangler deploy --config infra/cloudflare/wrangler.json
```

Every request requires the daemon Bearer token, including lifecycle operations. Use an HTTP client with an Authorization header for `POST /_adapter/start`, then poll `/readyz` and `/v1/status`. The adapter starts disabled, records activation durably, and renews a 30-second Durable Object alarm before startup attempts. That alarm supervises the singleton container even without incoming requests. It uses external Postgres, resets the inactivity timeout after Durable Object rehydration, and returns 503 during startup or failure. Cloudflare host restarts and delayed alarms can still delay scheduling; live testing must establish acceptable recovery timing.

`POST /_adapter/stop` durably disables the watchdog and waits for graceful process exit. Stop before upgrading the Worker/image or migrating hosts, then deploy the replacement and explicitly activate it. Do not run another Worker/container application against the same database. An interrupted stop may require operator inspection; do not assume a successful shutdown from a disconnected request.

The adapter intentionally requires a Bearer header throughout, so it does not provide the browser console's usual login flow. Browser access through a separately authenticated gateway is future work. Its lite instance is a sizing candidate, not a validated capacity guarantee. Do not use filesystem snapshots or R2/FUSE as a substitute for the live Go ledger's durability semantics.

Sources: [Container API](https://developers.cloudflare.com/containers/api/durable-object-container/), [Wrangler configuration](https://developers.cloudflare.com/containers/configuration/wrangler/), [lifecycle/storage FAQ](https://developers.cloudflare.com/containers/faq/).

## Railway operational configuration

The checked-in JSON files remain legacy packaging references. Railway now recommends [Infrastructure as Code](https://docs.railway.com/infrastructure-as-code); new deployments can use service configuration through the API/dashboard. This repository does not automatically apply a project-wide IaC plan or delete resources omitted from one.

Configure one always-on daemon and one runner. Use the runner start command from `infra/railway/runner.json`; mount `/data`, set `RAILWAY_RUN_UID=0`, and let the entrypoint initialize the mount then drop to UID 10001. The app itself must remain unprivileged. The bootstrap changes ownership only of the mount directory, not an existing tree; restore files with UID 10001 ownership. The Postgres daemon requires no local ledger volume.

Zero overlap seconds does not prevent the new daemon from starting before the old one stops. Pin deployment commits and perform stop/drain/start maintenance rather than relying on automatic GitHub redeploys for this single-process topology. Back up before upgrades. Verify provider delivery, restore and outage behavior using the public production acceptance guide before starting the observation period.
