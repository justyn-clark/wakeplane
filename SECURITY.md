# Security Policy

## Current security posture

**Wakeplane supports single-operator bearer authentication when configured, but it does not provide authorization, RBAC, or multi-tenant access control.**

If `WAKEPLANE_AUTH_TOKEN` is unset, any process that can reach the HTTP port can create, modify, delete, trigger, or pause any schedule. Any process that can reach the port can read all run history and receipts and use the operator console.

If `WAKEPLANE_AUTH_TOKEN` is set, `/v1/...` routes require `Authorization: Bearer <token>`. The static console assets at `/console/` are public so a local browser can load the page; console data and actions still call `/v1/...` and use the configured bearer token.

**Do not expose Wakeplane directly to the public internet.**

Bind it to a trusted network boundary. Acceptable deployment models for the current release:

- `127.0.0.1:8080` for local development
- Internal host or trusted subnet behind a reverse proxy that enforces auth
- VPN-protected or Tailscale-protected internal network segment
- Private Kubernetes cluster network with pod-level network policy

A reverse proxy or VPN gateway that enforces auth/TLS is the recommended pattern for any multi-user or network-accessible deployment.

## Current scope

The following are **out of scope** in the current beta release line:

- Role-based access control
- Multi-tenant separation
- TLS termination in the daemon itself (use a reverse proxy)
- Rate limiting
- Secret injection for HTTP or shell targets

These may be added in future minor/major versions.

## Intended use

Wakeplane is intended for embedded or internal operator-controlled systems on private networks. It is not intended for direct public-internet exposure in the current release line.

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
