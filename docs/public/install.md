# Install

Use one of these supported install paths for Wakeplane. The canonical repository is [github.com/justyn-clark/wakeplane](https://github.com/justyn-clark/wakeplane).

> **Operator warning:** the published `v0.3.0-beta.3` supports single-operator bearer authentication when configured, with no RBAC or multi-tenancy. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network.

The release-download and `go install` commands below pin the published release, `v0.3.0-beta.3`. It includes bearer authentication, Postgres, the console, MCP assistant access, event delivery, and tracked external jobs. Verify the resulting version before following the current API and operator guides.

The legacy `v0.2.0-beta.1` lacks built-in authentication, request audit, configurable receipt limits, terminal-run retention, Postgres, the console, the `status` command, schedule update/export/import, and the new automation interfaces. Environment settings do not add these controls to an older binary. Back up your database before upgrading; see the [release notes](releases/v0.3.0-beta.3.md) for compatibility and rollback limits.

## Option 1: GitHub Releases

Preferred for operators. Tagged releases publish platform archives and a checksum file on the [GitHub Releases page](https://github.com/justyn-clark/wakeplane/releases).

For the fastest path on macOS or Linux:

```bash
curl -fsSL https://wakeplane.dev/install.sh | sh
```

By default, this installs `wakeplane` and `wakeplaned` to `~/.local/bin`. Override with `INSTALL_DIR=/usr/local/bin` or pin a version with `WAKEPLANE_VERSION=v0.3.0-beta.3`.

Published for `v0.3.0-beta.3`:

- `wakeplane_0.3.0-beta.3_darwin_arm64.tar.gz`
- `wakeplane_0.3.0-beta.3_linux_amd64.tar.gz`
- `wakeplane_0.3.0-beta.3_linux_arm64.tar.gz`
- `checksums.txt`

Example verification flow:

```bash
curl -fsSLO https://github.com/justyn-clark/wakeplane/releases/download/v0.3.0-beta.3/wakeplane_0.3.0-beta.3_linux_amd64.tar.gz
curl -fsSLO https://github.com/justyn-clark/wakeplane/releases/download/v0.3.0-beta.3/checksums.txt
grep 'wakeplane_0.3.0-beta.3_linux_amd64.tar.gz' checksums.txt | sha256sum -c -
tar -xzf wakeplane_0.3.0-beta.3_linux_amd64.tar.gz
./wakeplane version
```

Each archive contains `wakeplane`, `wakeplaned`, and the separate example `automation-runner`. The hosted installer installs the CLI and daemon; extract the archive to run `automation-runner`, or use the source command in [Recipes](recipes.md).

## Option 2: `go install`

The repo currently declares `go 1.25.0` in `go.mod`.

```bash
go install github.com/justyn-clark/wakeplane/cmd/wakeplane@v0.3.0-beta.3
go install github.com/justyn-clark/wakeplane/cmd/wakeplaned@v0.3.0-beta.3
wakeplane version
```

## Option 3: Source build

```bash
git clone https://github.com/justyn-clark/wakeplane.git
cd wakeplane
go build ./cmd/wakeplane
go build ./cmd/wakeplaned
./wakeplane version
```

## Smoke test after install

```bash
./wakeplane version
WAKEPLANE_DB_PATH=./wakeplane.db \
WAKEPLANE_HTTP_ADDR=127.0.0.1:8080 \
WAKEPLANE_WORKER_ID=wrk_local \
./wakeplane serve
```

In another terminal:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

The `version`, `healthz`, and `readyz` checks are enough for the initial install smoke test. Create schedules with the API or CLI after you have chosen a working directory and manifest location.
