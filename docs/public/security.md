# Security

## Current security posture

**Wakeplane `0.1.x` has no authentication.**

This is a deliberate and explicit alpha constraint, not an oversight. Every operator who deploys Wakeplane must understand what this means:

- Any process that can reach the HTTP port can read all schedules, list all runs, create schedules, trigger runs, delete schedules, and access all run receipts.
- The HTTP API has no API keys, no tokens, no sessions, and no access control.
- There is no multi-tenancy, RBAC, or audit logging at the HTTP layer.

## Required: bind to a trusted network

**Do not expose Wakeplane directly to the public internet or to untrusted networks.**

Acceptable deployment patterns for `0.1.x`:

- **Loopback only.** Bind to `127.0.0.1:8080` and access only from the same host.
- **Internal/private network.** Bind to a private network interface accessible only to trusted services.
- **VPN or overlay network.** Place the daemon behind WireGuard, Tailscale, or an equivalent trusted network boundary.
- **Reverse proxy with auth.** Place nginx, Caddy, or an API gateway in front and enforce authentication at the proxy layer.

Not acceptable:

- Binding to `0.0.0.0` and exposing the port to the internet or any untrusted network
- Deploying without a network boundary and assuming "it's fine because it's internal"

## What is in scope for alpha

The current alpha provides:

- Correct scheduling, dispatch, and run ledger semantics
- Structured logging of all operations
- Prometheus metrics and operational status
- Durable run state with recovery on crash

The current alpha does **not** provide:

- Authentication (API keys, bearer tokens, OAuth, mTLS)
- Authorization (RBAC, per-schedule access control)
- Audit logging at the API layer
- Network-layer encryption (TLS) — this should be provided by a reverse proxy
- Multi-tenancy

## Planned (not shipped)

Authentication and RBAC are planned for a future release. The timeline is not committed. Do not deploy Wakeplane in a context that requires these properties in its current form.

## Responsible disclosure

If you find a security vulnerability in Wakeplane, please report it privately before public disclosure.

Contact: see [SECURITY.md](../../SECURITY.md) at the repo root for the reporting address and process.

Do not open a public GitHub issue for security vulnerabilities.

## Dependency surface

Wakeplane's runtime dependencies:

| Dependency | Purpose |
|---|---|
| `github.com/robfig/cron/v3` | Cron expression parsing and next-fire calculation |
| `modernc.org/sqlite` | Pure-Go SQLite driver (no CGo) |
| `github.com/oklog/ulid/v2` | ULID generation for IDs |
| `github.com/spf13/cobra` | CLI framework |
| `golang.org/x/sync` | `errgroup` for goroutine coordination |

Dependency versions are pinned in `go.sum`. Verify with `go mod verify` before deploying in sensitive environments.

## Summary

| Property | Status |
|---|---|
| Authentication | ❌ Not implemented |
| Authorization / RBAC | ❌ Not implemented |
| TLS (native) | ❌ Not implemented (use reverse proxy) |
| Audit logging | ❌ Not implemented |
| Multi-tenancy | ❌ Not implemented |
| Trusted-network deployment | ✅ Supported and required |
| Reverse proxy pattern | ✅ Recommended |
| Go module integrity | ✅ `go.sum` pinned |
