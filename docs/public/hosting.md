# Hosting the Wakeplane daemon

Wakeplane is deployable as a long-running Go process with durable storage and a protected operator endpoint. Release `v0.3.0-beta.2` includes `Dockerfile`, `railway.json`, platform `PORT` support, and Discord/Gmail runner adapters. No hosted runtime is provisioned merely by adding these files. The volume bootstrap and additional cloud adapters described below are source additions after that binary release; build their reviewed source commit rather than assuming the beta.2 archives include them.

## Railway: recommended first deployment

Railway supports repository/Docker deployments, persistent volumes, and private inter-service networking. Run one always-on daemon and a separate execution adapter in the same project environment. The checked-in legacy configuration selects Docker, one replica, no idle sleeping, `/readyz`, and bounded failure restarts. Railway now recommends Infrastructure as Code; new deployments can apply the same settings through the API/dashboard. Do not apply a whole-project IaC plan without reviewing its resource deletions. Wakeplane reads Railway's `PORT` when `WAKEPLANE_HTTP_ADDR` is unset; an explicit address takes precedence. Do not configure a Railway cron schedule for the daemon: Wakeplane owns scheduling continuously.

Sources: [Railway services](https://docs.railway.com/services), [configuration](https://docs.railway.com/config-as-code/reference), [healthchecks](https://docs.railway.com/deployments/healthchecks), [private networking](https://docs.railway.com/networking/private-networking).

### Daemon service

Create a service from this repository and choose exactly one storage setup:

| Setting                  | Postgres                                         | SQLite                                  |
| ------------------------ | ------------------------------------------------ | --------------------------------------- |
| `WAKEPLANE_STORE`        | `postgres`                                       | `sqlite`                                |
| `WAKEPLANE_DATABASE_URL` | Private reference to the Postgres connection URL | Unset                                   |
| `WAKEPLANE_DB_PATH`      | Unused                                           | `/data/wakeplane.db`                    |
| Persistent daemon volume | Not needed for ledger storage                    | Mount at `/data`, writable by UID 10001 |

Set `WAKEPLANE_AUTH_TOKEN` from your shared vault, use a unique stable worker ID, and retain request auditing. Keep service networking private; access the console/API through an authenticated TLS proxy, VPN, or protected tunnel. A generated public domain alone does not provide the required private deployment boundary. Database backups and restore drills are required for either backend. Back up before upgrades because startup applies additive migrations; an older database backup is the rollback boundary.

Run one replica in one region. During updates, avoid overlapping daemon deployments even with Postgres: the current supported topology is single-process, and independent scheduler/dispatcher replicas are not a supported HA deployment. Stop the old process gracefully before starting its replacement; allow time for dispatcher drain. A SQLite volume prevents simultaneous volume mounts but introduces deployment downtime. Railway's readiness check runs during deployment and is not continuous monitoring.

### Separate runner service

Use the same container image with start command `/usr/local/bin/wakeplane-entrypoint /usr/local/bin/automation-runner`. Use a separate config file path, `infra/railway/runner.json`, because the runner's check is `/healthz`, not `/readyz`. Set `CONTAINER_HEALTH_PATH=/healthz` for the image's healthcheck too.

Set:

- `PORT=8091` and `AUTOMATION_RUNNER_ADDR=[::]:8091`.
- `AUTOMATION_RUNNER_PUBLIC_URL` to its private origin, for example `http://runner.railway.internal:8091`; submit, lookup, status, and artifact URLs must remain on that origin.
- `AUTOMATION_RUNNER_STATE_DIR=/data/runner`; attach a separate persistent volume at `/data`. Railway mounts it as root: set `RAILWAY_RUN_UID=0` and use the startup bootstrap to initialize mount ownership before dropping the application to UID 10001. Restore existing files with that ownership. Postgres for Wakeplane does not persist the example runner's file ledger.
- `AUTOMATION_RUNNER_TOKEN` from the vault. Configure the matching schedule HTTP authorization header through the operator interface. Schedule storage/export contains static headers, so protect backups and exports as credentials.
- Discord/Gmail settings from the vault as described in [Recipes](recipes.md).

Keep one runner replica and preserve its state. It is a bounded example adapter (500 retained jobs), without automatic pruning or a production connector fleet. Establish a retention/capacity plan before a long soak. Native Gmail delivery requires its own offline OAuth grant; the assistant's Gmail plugin credentials are not available to the container.

## Cloudflare

A Worker or Pages deployment cannot run the current daemon unchanged: it needs a long-running native Go process, database connectivity, and executor semantics. Hosting the documentation on Cloudflare is a separate question from hosting the scheduler.

Cloudflare Containers can run a Linux AMD64 image, but container disk is ephemeral and the default Container class sleeps after inactivity. A feasible adaptation uses external Postgres plus explicit single-instance lifecycle/restart management that keeps the scheduler active between incoming requests. The example runner also needs persistent job state outside ephemeral container disk; do not put its `jobs.json` there and claim durability. Durable Object storage or snapshots are not drop-in live SQLite storage for this binary. The repository now contains an experimental API-only daemon supervisor using a singleton Durable Object and durable lifecycle alarms. It requires external Postgres and a separately hosted durable runner reachable over authenticated HTTPS; Railway private DNS is not reachable from Cloudflare. Every adapter request requires a Bearer header, so the normal browser login flow is not provided. Its configuration, Worker bundle and supervisor behavior are checked; live Cloudflare deployment and recovery remain unverified.

Cloudflare can provide DNS/TLS and an authenticated access proxy in front of a daemon hosted elsewhere. See [Containers lifecycle](https://developers.cloudflare.com/containers/concepts/architecture/) and [Containers FAQ](https://developers.cloudflare.com/containers/faq/).

## AWS and Google Cloud

Portable Kubernetes adapters target existing AWS EKS and Google GKE clusters. Both retain one daemon and a separate runner, private ClusterIP services, distinct persistent volumes, UID 10001, secret references and `Recreate` updates. AWS uses the EBS CSI driver with encrypted gp3 disks; Google uses the Persistent Disk CSI driver with pd-balanced disks. Both storage classes retain disks on claim deletion.

These adapters do not provision clusters, managed databases, gateways or credentials. The daemon can instead use its existing Postgres backend with RDS or Cloud SQL, subject to operator networking, TLS and backup configuration. The runner still needs its own durable state. Manifest rendering, schema and safety invariants are validated; live AWS/GCP operation is not yet verified. Resource settings require workload measurement.

See the repository deployment adapter README for commands, prerequisites and verification boundaries. Use stop/drain/backup/restore/start when moving hosts; keep provider delivery disabled during restore drills.

## Verification before production claims

Use [Production Acceptance](production-acceptance.md). Image build, readiness, and green unit tests establish packaging/functional evidence; they do not establish a working provider grant, real notification receipt, database restoration, or a completed production observation window.
