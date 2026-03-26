# Release Discipline

## Versioning

Wakeplane follows [Semantic Versioning](https://semver.org/):

- **MAJOR**: Breaking changes to API contract, CLI interface, or storage schema.
- **MINOR**: New features, new endpoints, new policy types, backwards-compatible schema migrations.
- **PATCH**: Bug fixes, test improvements, documentation updates.

The current version is `0.x.y`, indicating pre-stable. During `0.x`, minor versions may include breaking changes.

## Version Source

Version is defined as a constant in both entry points:

- `cmd/wakeplane/main.go` — `const version = "0.2.0-beta.1"`
- `cmd/wakeplaned/main.go` — `const version = "0.2.0-beta.1"`

Both must be updated in lockstep. The version is surfaced in:

- `GET /v1/status` → `version` field
- Embedded example passes `"embed-example"` as version

## Release Checklist

Before tagging a release:

1. **All tests pass**: `go test ./... -count=1`
2. **Build succeeds**: `go build ./...`
3. **SMALL strict check passes**: `small check --strict`
4. **Version constants updated** in both `cmd/wakeplane/main.go` and `cmd/wakeplaned/main.go`
5. **README "Current status" section** reflects any new capabilities
6. **No uncommitted changes**: `git status` is clean
7. **Documentation current**: docs/ files reflect actual behavior

## Tagging

```
git tag -a v0.2.0 -m "v0.2.0: <summary>"
git push origin v0.2.0
```

## Binary Artifacts

Build both binaries:

```
go build -o dist/wakeplane ./cmd/wakeplane
go build -o dist/wakeplaned ./cmd/wakeplaned
```

For cross-compilation:

```
GOOS=linux GOARCH=amd64 go build -o dist/wakeplane-linux-amd64 ./cmd/wakeplane
GOOS=darwin GOARCH=arm64 go build -o dist/wakeplane-darwin-arm64 ./cmd/wakeplane
```

## What Constitutes a Breaking Change

- Removing or renaming an API endpoint
- Changing the error envelope shape
- Changing run status values or transition semantics
- Changing the schedule YAML manifest schema
- Changing the storage schema in a non-migratable way
- Changing CLI command names or required flags
- Removing a policy type or changing its default behavior
