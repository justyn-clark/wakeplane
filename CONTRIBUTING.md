# Contributing

Wakeplane is a pre-stable scheduling control plane in its beta line (`v0.2.0-beta.1`). Contributions are welcome within the project scope defined below.

## Project scope

Wakeplane is:
- a durable scheduling primitive
- a control plane above cron/interval/once
- an embeddable daemon and HTTP/CLI API
- a typed execution layer with operator visibility

Wakeplane is not:
- a DAG orchestrator
- a distributed workflow engine
- a job queue
- a no-code automation tool
- a cluster scheduler

PRs that push the project outside that scope will be declined regardless of implementation quality.

## Before opening a PR

1. **Open an issue first** for non-trivial changes to discuss intent before writing code.
2. Check that the existing tests pass: `go test ./... -count=1`
3. Check that the build succeeds: `go build ./...`
4. Verify generated docs are current: `go run ./tools/docsgen --check`
5. If the repo has SMALL: run `small check --strict` before submitting.

## Code conventions

- Prefer explicit types over loose maps.
- Keep storage Postgres-ready even while SQLite-first.
- Do not bypass policy enforcement for convenience.
- Do not execute work without durable run recording.
- Do not add app-specific behavior to the core domain.
- Do not collapse scheduler and executor into one package.
- Add tests for invariants whenever behavior changes.
- Preserve the append-only run ledger semantics.

## Commits

Write clear, factual commit messages. Present tense. No fluff. Describe what changed and why if it is non-obvious.

## Versioning

Wakeplane follows [Semantic Versioning](https://semver.org/). During `0.x`, minor versions may include breaking changes to the API, CLI, or storage schema. See [docs/release.md](docs/release.md) for release discipline.

## Code of conduct

Treat contributors and maintainers professionally. Issues and PRs are for technical work, not arguments about scope or taste. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

By contributing you agree that your contributions will be licensed under the [MIT License](LICENSE) covering this project.
