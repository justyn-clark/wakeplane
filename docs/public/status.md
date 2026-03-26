# Status

This page defines what Wakeplane means by alpha, beta, and 1.0. It is intentionally operational, not promotional.

## Current public state

Wakeplane is still publicly labeled `0.1.x` until the beta gate is satisfied by a tagged release with published artifacts and verified docs. The codebase is being hardened toward beta, but the label should not move early.

## What beta means here

Beta means:

- the public GitHub path resolves and is the canonical source
- trust files exist and describe how the project operates
- public docs match shipped code exactly
- release binaries and checksums are published from tags
- security posture is explicit on the site and in the repo
- CI validates code, generated docs, and public-doc examples

Beta does **not** mean:

- stable semver guarantees
- auth or RBAC
- distributed coordination
- a web UI

## Beta gate

Wakeplane can be labeled beta when all of these are true:

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

## Explicitly out of scope today

- public multi-tenant SaaS scheduling
- auth-heavy enterprise control plane deployments
- distributed orchestration or DAG workflow systems
- plugin loading or dynamic workflow discovery
