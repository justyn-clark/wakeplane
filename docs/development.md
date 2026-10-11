# Development

For installing and operating Wakeplane, use the [public documentation](public/index.md). This guide covers working on the repository.

## Build from source

```sh
go build -o dist/wakeplane ./cmd/wakeplane
go build -o dist/wakeplaned ./cmd/wakeplaned
```

Run `./dist/wakeplane serve`, or `go run ./cmd/wakeplane serve` without building first. See [Install](public/install.md) for Go requirements and installation options.

## Develop the operator console

```sh
make console
# or
just console
```

Open `http://127.0.0.1:8080/console/`. Override the address or development database with `make console ADDR=127.0.0.1:18080 DB=./tmp/wakeplane.db` or `just console 127.0.0.1:18080 ./tmp/wakeplane.db`.

## Verify changes

Follow the checks and conventions in [Contributing](../CONTRIBUTING.md). Behavior changes require tests for the affected invariants. Keep generated references current with `go run ./tools/docsgen --check`.

## Agent execution state

Runtime and build execution state is tracked through SMALL:

- Use `small plan`, `small checkpoint`, `small handoff`, and `small check --strict` for agent-owned state.
- Use `small draft` and `small accept` for human-owned `.small` artifacts.
- Use `small apply --task ... --cmd ...` for build, test, and verification commands.

## Engineering references

- [Architecture](architecture.md) and [current status](current-status.md)
- [API contract](api-contract.md) and [run states](run-states.md)
- [Embedding contract](embedding.md) and [replace semantics](replace-semantics.md)
- [Storage interface](storage-interface.md), [portability](storage-portability.md), and [SQLite audit](sqlite-audit.md)
- [Release discipline](release.md), [deployment notes](deployment.md), and [production acceptance](production-acceptance.md)

Configuration, installation, storage transfer, policy details, and operating procedures live in their dedicated guides rather than the repository introduction.
