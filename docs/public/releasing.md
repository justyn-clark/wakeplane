# Releasing

Release conventions for Wakeplane. This page covers versioning policy, artifact expectations, docs verification, and what constitutes a breaking change.

## Versioning

Wakeplane follows [Semantic Versioning](https://semver.org/):

- **MAJOR** — breaking changes to API contract, CLI interface, or storage schema
- **MINOR** — new features, new endpoints, new policy types, backwards-compatible schema migrations
- **PATCH** — bug fixes, test improvements, documentation updates

**Pre-stable notice:** The current version is `0.x.y`. During `0.x`, minor versions may include breaking changes without a MAJOR bump. The API and CLI surface are not yet guaranteed stable.

## Version source

Version is defined as a constant in both entry points:

- `cmd/wakeplane/main.go` — `const version = "0.2.0-beta.1"`
- `cmd/wakeplaned/main.go` — `const version = "0.2.0-beta.1"`

Both must be updated in lockstep before tagging. The version is surfaced in:

- `GET /v1/status` → `version` field
- Embedded applications pass their own version string to `config.FromEnv`

## Release checklist

Before tagging a release:

1. **All tests pass**: `go test ./... -count=1`
2. **Build succeeds**: `go build ./...`
3. **SMALL strict check passes**: `small check --strict`
4. **Generated docs are current**: `go run ./tools/docsgen --check`
5. **Public docs validation passes**: docs tests reject unsupported fields, fantasy commands, and unknown API paths
6. **Version constants updated** in both `cmd/wakeplane/main.go` and `cmd/wakeplaned/main.go`
7. **Install and release notes updated** under `docs/public/`
8. **No uncommitted changes**: `git status` is clean

## Release artifacts

Tagged releases must publish:

- `wakeplane_<version>_darwin_arm64.tar.gz`
- `wakeplane_<version>_linux_amd64.tar.gz`
- `wakeplane_<version>_linux_arm64.tar.gz`
- `checksums.txt`

Each archive includes both `wakeplane` and `wakeplaned`.

The release workflow builds artifacts with `scripts/build-release-artifacts.sh` and attaches them to the GitHub release for tagged versions.

## Release notes

Each release page should include:

- version
- date
- what changed
- breaking changes
- upgrade notes
- known limitations

Current release notes:

- [v0.1.0](releases/v0.1.0.md)
- [v0.2.0-beta.1 (planned beta tag)](releases/v0.2.0-beta.1.md)

## What constitutes a breaking change

The following require a MAJOR version bump after `1.0.0` (or a clear release note during `0.x`):

- Removing or renaming an HTTP API endpoint
- Changing the error envelope shape (fields, codes, HTTP status mapping)
- Changing run status values or transition semantics
- Changing the schedule YAML manifest schema (required fields, field names, value semantics)
- Changing the storage schema in a non-migratable way
- Changing CLI command names or required flags
- Removing a policy type or changing its default behavior
- Removing a supported executor kind

Adding new optional fields, new endpoints, new policy types, or new executor kinds is not a breaking change.

## Two binaries: `wakeplane` and `wakeplaned`

Both binaries are identical in `0.1.x`. They share the same command surface and configuration.

`wakeplaned` follows Unix daemon naming conventions (`sshd`, `httpd`) for process listing, packaging disambiguation, and future deployment tooling. The split into two entry points is intentional and forward-looking — they may diverge if the daemon gains additional OS-level integration (systemd notify, privilege dropping, PID file management).

Do not treat `wakeplaned` as deprecated. Both binaries are maintained.
