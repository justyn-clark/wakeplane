# Release discipline

## Versioning and public scope

Wakeplane uses Semantic Versioning for the standalone daemon, documented REST API, CLI commands, and schedule manifests defined in [Stable contract](public/stable-contract.md).

- **MAJOR:** incompatible changes to the supported public contract, existing enum/default meanings, or storage upgrades that cannot preserve the documented data.
- **MINOR:** additive features, endpoints, commands, opt-in policy types, and compatible migrations.
- **PATCH:** fixes to the documented behavior, dependency fixes, and documentation corrections.

The stable release line starts with `v1.0.0`. Pre-1.0 releases carry no 1.x compatibility promise. Internal Go packages, human-readable help/error/status wording, console layout, opaque IDs/cursors, and internal SQL layout are not public semver contracts.

## Version source

Update `const version` in both `cmd/wakeplane/main.go` and `cmd/wakeplaned/main.go` together. Keep generated API/CLI/config references, release notes, the installer default and documentation notification banner aligned with the release that is actually published. A candidate source version must not be presented as an available binary before publication.

Version is surfaced by `wakeplane version` and `GET /v1/status`. The embedded source example uses its own `embed-example` identifier and is outside the stable product API.

## Candidate verification

Before tagging the exact final commit:

1. Run `go test -race ./... -count=1`, `go build ./...`, and `go vet ./...`.
2. Run the real Postgres parity suite with `scripts/test-postgres-store.sh`; a suite that skips Postgres without a test URL is not parity evidence.
3. Run `go run ./tools/docsgen --check`, the public example tests in `go test ./...`, `pnpm run check`, and `small check --strict`.
4. Confirm release constants, notes, install instructions, public contract and intended current-status copy agree. Review intentional pre-1.0 policy corrections and upgrade limits.
5. Confirm [Production acceptance](public/production-acceptance.md), native backup/restore and isolated rollback evidence exists for the supported topology. No arbitrary 30-day or weekly wait is required when the gates have passed.
6. Verify the working tree contains only the reviewed release changes, and CI checks the exact candidate commit.

Public documentation is synchronized from `docs/public`; generated references must be regenerated from the versioned code. Verify the actual live site after its deployment, including its banner and install/release links.

## Publish and verify

For the validated final source commit:

```bash
git tag -a v1.0.0 -m "v1.0.0: stable single-operator scheduling control plane"
git push origin v1.0.0
```

The release workflow builds the supported archives and `checksums.txt`. Verify the workflow result, tag target and release metadata rather than inferring publication from a pushed tag.

Download every published archive and `checksums.txt` from the real GitHub release. Verify every SHA-256 entry, archive layout and expected binaries (`wakeplane`, `wakeplaned`, `automation-runner`). Smoke-test native-host CLI/daemon startup and installer against the public downloads. Cross-compiled archive inspection does not establish execution on an unavailable OS/architecture.

Update publication status and matching documentation only after release availability is confirmed. Retain evidence of the exact source/tag, workflow, downloaded hashes and tested install path. A failed or partial publication is not a completed release.

## Upgrade and rollback

Startup applies the store's migrations; there is no automatic downgrade procedure. Stop/drain the old daemon and runner, take a database-native backup with paired runner state, restore/verify it in isolation, and follow the [Stable contract](public/stable-contract.md) before starting the new version. Rollback uses the pre-upgrade data and prior binaries, followed by provider reconciliation. Schedule export/import is a definitions bridge, not a history/checkpoint backup.

## Breaking changes

Examples requiring a new major version after 1.0:

- Removing/renaming an existing REST endpoint, supported command, argument, flag, or manifest field.
- Changing request/response field meaning, existing error codes, run statuses, policy behavior, or documented defaults.
- Requiring previously optional definition fields without a separate versioned surface.
- Losing retained schedule IDs, run evidence, remote checkpoints, or event replay protection during a supported upgrade.

Adding an optional response field does not change the existing contract. Clients must ignore unknown response fields and treat IDs/cursors as opaque.
