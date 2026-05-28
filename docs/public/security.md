# Security

## Current security posture

**Wakeplane supports single-operator bearer authentication when configured, but it does not provide RBAC or multi-tenant access control.**

This is a deliberate and explicit constraint in the current release line. Every operator who deploys Wakeplane must understand what this means:

- If `WAKEPLANE_AUTH_TOKEN` is unset, any process that can reach the HTTP port can read all schedules, list all runs, create schedules, trigger runs, delete schedules, access all run receipts, and use the operator console.
- If `WAKEPLANE_AUTH_TOKEN` is set, `/v1/...` routes require `Authorization: Bearer <token>`.
- There are no sessions, users, roles, or per-schedule permissions.
- HTTP request audit logging is enabled by default and can be disabled with `WAKEPLANE_REQUEST_AUDIT=false`.

## Required: bind to a trusted network

**Do not expose Wakeplane directly to the public internet or to untrusted networks.**

Acceptable deployment patterns right now:

- **Loopback only.** Bind to `127.0.0.1:8080` and access only from the same host.
- **Trusted subnet.** Bind to a private network interface accessible only to trusted services and operators.
- **VPN or overlay network.** Place the daemon behind WireGuard, Tailscale, or an equivalent trusted network boundary.
- **Reverse-proxied private network.** Place nginx, Caddy, or an API gateway in front and enforce authentication at the proxy layer.

Not acceptable:

- Binding to `0.0.0.0` and exposing the port to the internet or any untrusted network
- Deploying without a network boundary and assuming "it's fine because it's internal"

## Intended use right now

Wakeplane is intended for:

- embedded or internal operator-controlled systems
- private control planes
- trusted environments where network access is already constrained

The current release provides:

- Correct scheduling, dispatch, and run ledger semantics
- Structured logging of all operations
- Prometheus metrics and operational status
- Static console assets at `/console/`; console data and actions still use `/v1/...`
- Durable run state with recovery on crash

The current release does **not** provide:

- Multi-user authentication (OAuth, mTLS)
- Authorization (RBAC, per-schedule access control)
- Network-layer encryption (TLS) - this should be provided by a reverse proxy
- Multi-tenancy

## Planned (not shipped)

RBAC, mTLS, OAuth, and multi-tenant security are not committed for the current release line. Do not deploy Wakeplane in a context that requires those properties in the current form.

## Responsible disclosure

If you find a security vulnerability in Wakeplane, please report it privately before public disclosure.

Contact: see [SECURITY.md](../../SECURITY.md) at the repo root for the reporting address and process.

Do not open a public GitHub issue for security vulnerabilities.

## Dependency surface

Wakeplane's runtime dependencies:

| Dependency                  | Purpose                                           |
| --------------------------- | ------------------------------------------------- |
| `github.com/robfig/cron/v3` | Cron expression parsing and next-fire calculation |
| `modernc.org/sqlite`        | Pure-Go SQLite driver (no CGo)                    |
| `github.com/oklog/ulid/v2`  | ULID generation for IDs                           |
| `github.com/spf13/cobra`    | CLI framework                                     |
| `golang.org/x/sync`         | `errgroup` for goroutine coordination             |

Dependency versions are pinned in `go.sum`. Verify with `go mod verify` before deploying in sensitive environments.

## Summary

| Property                   | Status                                       |
| -------------------------- | -------------------------------------------- |
| Authentication             | Single-operator bearer token when configured |
| Authorization / RBAC       | No - not implemented                         |
| TLS (native)               | No - not implemented (use reverse proxy)     |
| Audit logging              | Yes - HTTP control-plane request audit       |
| Multi-tenancy              | No - not implemented                         |
| Trusted-network deployment | Yes - supported and required                 |
| Reverse proxy pattern      | Yes - recommended                            |
| Go module integrity        | Yes - `go.sum` pinned                        |
