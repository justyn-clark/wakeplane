# Security Policy

## Alpha status and no-auth warning

**Wakeplane v0.1.x has no authentication, authorization, or RBAC.**

Any process that can reach the HTTP port can create, modify, delete, trigger, or pause any schedule. Any process that can reach the port can read all run history and receipts.

**Do not expose Wakeplane directly to the public internet.**

Bind it to a trusted network boundary. Acceptable deployment models for the current release:

- `127.0.0.1:8080` for local development
- Internal host or container network behind a reverse proxy that enforces auth
- VPN-protected internal network segment
- Private Kubernetes cluster network with pod-level network policy

A reverse proxy or VPN gateway that enforces auth/TLS is the recommended pattern for any multi-user or network-accessible deployment.

## Current scope

The following are **out of scope** in the current release and are not planned for v0.1.x:

- HTTP authentication (Bearer tokens, API keys, basic auth)
- Role-based access control
- Multi-tenant separation
- TLS termination in the daemon itself (use a reverse proxy)
- Audit logging of API calls (run ledger is append-only but not access-logged)
- Rate limiting
- Secret injection for HTTP or shell targets

These are planned features for future minor/major versions.

## Reporting a security issue

If you find a security issue in Wakeplane, please report it privately rather than filing a public issue.

Contact: **security@justynclark.com**

Include:
- A description of the issue
- Steps to reproduce
- Your assessment of impact
- Affected version(s)

Expected response time: 5 business days for acknowledgement. Fixes will be released as patch versions and disclosed after a fix is available.

## Supply chain

Wakeplane is a Go module. Dependencies are pinned in `go.sum`. Review the dependency list in `go.mod` before deploying in sensitive environments. There are no optional C extensions. The SQLite driver (`modernc.org/sqlite`) is a pure-Go port without CGo.
