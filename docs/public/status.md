# Status

This page defines what Wakeplane means by alpha, beta, and 1.0. It is intentionally operational, not promotional.

## Current public state

The published beta is `v0.3.0-beta.3`. Tagged archives and checksums include the notification formatting and deployment additions described here. Wakeplane remains pre-stable and intended for a single operator on trusted infrastructure.

The legacy `v0.2.0-beta.1` has no built-in authentication, request audit, configurable receipt limits, retention, Postgres backend, operator console, status command, or schedule update/export/import commands. Those features and the new automation interfaces are available in `v0.3.0-beta.3`. Setting `WAKEPLANE_AUTH_TOKEN` does not protect an older binary that lacks authentication support.

The beta gate is now satisfied:

- the repository is public at `https://github.com/justyn-clark/wakeplane`
- trust files are present
- public docs are code-verified
- release binaries and `checksums.txt` are published
- install paths were verified against the real public release
- CI is green for the release cut

## What beta means here

Beta means:

- the public GitHub path resolves and is the canonical source
- trust files exist and describe how the project operates
- public docs identify their source version and distinguish its capabilities from published binary releases
- release binaries and checksums are published from tags
- security posture is explicit on the site and in the repo
- CI validates code, generated docs, and public-doc examples

Beta does **not** mean:

- stable semver guarantees
- RBAC or multi-user auth
- distributed coordination
- OAuth account onboarding or provider spending enforcement

## Beta gate

Wakeplane is allowed to claim beta only when all of these are true:

- GitHub link resolves publicly at `https://github.com/justyn-clark/wakeplane`
- `LICENSE`, `SECURITY.md`, and `CONTRIBUTING.md` exist
- public docs match the current repo exactly
- install docs cover release downloads, `go install`, and source builds
- release notes are structured and versioned
- CI validates builds, tests, generated docs, and public-doc examples
- at least one smoke-tested tagged release is publicly consumable

## 1.0 gate

Wakeplane should not be labeled stable until all of these are true:

- CLI surface is intentionally defined and stable enough for semver promises
- API and run-status model are intentionally defined and stable enough for semver promises
- docs are generated or verified directly from code paths
- upgrade and migration expectations are documented
- release publishing is routine and reproducible
- at least one real internal production use case has run long enough to justify the claim
- security posture is explicit and defensible for the intended deployment model

## Post-beta implementation track

The most practical path beyond beta is:

1. Validate complete operator workloads through guided creation, tracked external completion, and notification delivery; publish matching binaries and documentation.
2. Extend soak, restart-recovery, and backup/restore verification with longer windows and real operator workloads before claiming 1.0; follow [Production Acceptance](production-acceptance.md) and [Daemon Hosting](hosting.md).
3. Decide whether the embedding surface becomes a stable public Go package or remains source-level for the current release line.

## Explicitly out of scope today

- public multi-tenant SaaS scheduling
- auth-heavy enterprise control plane deployments
- distributed orchestration or DAG workflow systems
- plugin loading or dynamic workflow discovery
