# Deployment Notes - wakeplane.dev

This doc covers the deployment topology for the Wakeplane public site (`wakeplane.dev`).

## Deployment pattern

Wakeplane follows the same infrastructure pattern established for `smallprotocol.dev` and `musketeer.dev`:

- **DNS**: AWS Route53 managed zone, provisioned via Terraform
- **Hosting**: Vercel (static site / SSR)
- **TLS**: Vercel-managed, auto-provisioned after DNS verification
- **CI**: GitHub Actions on pushes to `main` and pull requests targeting `main`
- **Deployment trigger**: Vercel Git integration on push to `main`

Infrastructure is in `infra/terraform/`. Site source is in a separate `wakeplane.dev` repo (Astro).

## Terraform scope

The `infra/terraform/` directory manages:

- Route53 hosted zone for `wakeplane.dev`
- Apex A record -> Vercel ingress (`216.198.79.1`)
- `www` CNAME -> Vercel project-specific DNS target

The Terraform scope is DNS only. Vercel project creation, domain linking, and deployments are not managed in Terraform (consistent with the existing pattern across JCN sites).

See [infra/terraform/README.md](../infra/terraform/README.md) for usage.

## CI

GitHub Actions runs on pushes to `main` and pull requests targeting `main`:

1. JavaScript lint, Markdown formatting, and console tests with pinned pnpm dependencies
2. Go vet and formatting checks
3. Generated-document verification
4. Full Go race suite (`go test -race ./... -count=1 -timeout 120s`)
5. Real Postgres integration tests against a disposable Postgres 16 service
6. Binary build checks (`cmd/wakeplane` and `cmd/wakeplaned`)

See [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).

## Documentation promotion

Canonical public content lives in `docs/public/`. Correct it and regenerate API/CLI references in this repository first, then merge the verified source change before updating the [site repository](https://github.com/justyn-clark/wakeplane.dev).

From the site checkout, point `WAKEPLANE_SOURCE_ROOT` at a clean application checkout on the current `main` branch. Run `pnpm sync:docs`, `pnpm sync:docs:check`, `pnpm sync:docs:test`, `pnpm check`, `pnpm build`, and `pnpm check:built`. Inspect desktop/mobile pages, search, and release/install guidance before merging the site change. Its CI independently checks parity against application `main`, including coverage of every public source page and rendered internal links.

Vercel's `wakeplane-web` project serves `https://wakeplane.dev` and deploys site `main` through the Git integration. Verify the custom domain after deployment; a successful local build or preview does not confirm a production update. Source-only changes do not trigger site CI, so public-doc changes require this paired site update.

Keep source-version claims separate from published binaries. Do not advance `public/install.sh` or release-download examples until the corresponding tag, archives, and checksums are publicly available. A site deployment does not publish a Wakeplane binary release.

## Release artifact pattern

Wakeplane ships as two Go binaries:

```bash
go build -o dist/wakeplane ./cmd/wakeplane
go build -o dist/wakeplaned ./cmd/wakeplaned
```

Cross-compilation targets:

```bash
GOOS=linux GOARCH=amd64 go build -o dist/wakeplane-linux-amd64 ./cmd/wakeplane
GOOS=darwin GOARCH=arm64 go build -o dist/wakeplane-darwin-arm64 ./cmd/wakeplane
```

See [docs/release.md](release.md) for the full release checklist and versioning policy.

## Runtime requirements

- Single-process Go daemon
- SQLite database file by default, or an operator-provided Postgres database selected with `WAKEPLANE_STORE=postgres` and `WAKEPLANE_DATABASE_URL`
- No secrets or API keys required for basic operation
- Build dependencies use versions declared in `go.mod` and integrity hashes in `go.sum`; ordinary SQLite operation needs no separate database service
- HTTP targets require their configured service; shell targets require the selected host executable; registered workflows run in process

## Security posture for deployment

Wakeplane supports single-operator bearer auth for `/v1/...`, but has no RBAC or multi-tenancy. See [SECURITY.md](../SECURITY.md).

Recommended deployment topologies:

- Local only: bind to `127.0.0.1:8080`
- Internal network: bind to private interface, protect with VPN or firewall rules
- Private networked access: reverse proxy (nginx, Caddy, Traefik) that enforces auth and TLS; this does not add users, roles, or per-schedule permissions to Wakeplane

Do not expose the Wakeplane HTTP port directly to the public internet.
