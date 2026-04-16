# Deployment Notes — wakeplane.dev

This doc covers the deployment topology for the Wakeplane public site (`wakeplane.dev`).

## Deployment pattern

Wakeplane follows the same infrastructure pattern established for `smallprotocol.dev` and `musketeer.dev`:

- **DNS**: AWS Route53 managed zone, provisioned via Terraform
- **Hosting**: Vercel (static site / SSR)
- **TLS**: Vercel-managed, auto-provisioned after DNS verification
- **CI**: GitHub Actions (format / test / build on push and PR)
- **Deployment trigger**: Vercel Git integration on push to `main`

Infrastructure is in `infra/terraform/`. Site source is in a separate `wakeplane.dev` repo (Astro).

## Terraform scope

The `infra/terraform/` directory manages:

- Route53 hosted zone for `wakeplane.dev`
- Apex A record → Vercel ingress (`216.198.79.1`)
- `www` CNAME → Vercel project-specific DNS target

The Terraform scope is DNS only. Vercel project creation, domain linking, and deployments are not managed in Terraform (consistent with the existing pattern across JCN sites).

See [infra/terraform/README.md](../infra/terraform/README.md) for usage.

## CI

GitHub Actions runs on every push and PR to `main`:

1. Go formatting check
2. Full test suite (`go test ./... -count=1`)
3. Binary build check (`cmd/wakeplane` and `cmd/wakeplaned`)

See [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).

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
- SQLite database file (no external DB required for current release)
- No secrets or API keys required for basic operation
- No external dependencies beyond the Go standard library and `go.sum`-pinned modules

## Security posture for deployment

**v0.1.x has no auth.** See [SECURITY.md](../SECURITY.md).

Recommended deployment topologies:

- Local only: bind to `127.0.0.1:8080`
- Internal network: bind to private interface, protect with VPN or firewall rules
- Multi-user / networked: reverse proxy (nginx, Caddy, Traefik) that enforces auth and TLS

Do not expose the Wakeplane HTTP port directly to the public internet.
