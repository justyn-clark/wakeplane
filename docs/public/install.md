# Install

Use one of these supported install paths for Wakeplane. The canonical repository is [github.com/justyn-clark/wakeplane](https://github.com/justyn-clark/wakeplane).

> **Operator warning:** the published `v0.2.0-beta.1` has no built-in authentication. Bearer authentication is available in the `v0.3.0-beta.1` source line, with no RBAC or multi-tenancy. For either version, bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network.

The release-download and `go install` commands below pin the last published release, `v0.2.0-beta.1`. Built-in bearer authentication, Postgres, the console, and the new automation capabilities are in the `v0.3.0-beta.1` source line; use the source-build option until a matching release is published. Verify the resulting version before following the current API and operator guides.

Other later source capabilities include request audit, configurable receipt limits, terminal-run retention, the `status` command, and schedule update/export/import commands. These are absent from the legacy published binaries. Environment settings for those controls do not add them to an older binary.

## Option 1: GitHub Releases

Preferred for operators. Tagged releases publish platform archives and a checksum file on the [GitHub Releases page](https://github.com/justyn-clark/wakeplane/releases).

For the fastest path on macOS or Linux:

```bash
curl -fsSL https://wakeplane.dev/install.sh | sh
```

By default, this installs `wakeplane` and `wakeplaned` to `~/.local/bin`. Override with `INSTALL_DIR=/usr/local/bin` or pin a version with `WAKEPLANE_VERSION=v0.2.0-beta.1`.

Published for `v0.2.0-beta.1`:

- `wakeplane_0.2.0-beta.1_darwin_arm64.tar.gz`
- `wakeplane_0.2.0-beta.1_linux_amd64.tar.gz`
- `wakeplane_0.2.0-beta.1_linux_arm64.tar.gz`
- `checksums.txt`

Example verification flow:

```bash
curl -fsSLO https://github.com/justyn-clark/wakeplane/releases/download/v0.2.0-beta.1/wakeplane_0.2.0-beta.1_linux_amd64.tar.gz
curl -fsSLO https://github.com/justyn-clark/wakeplane/releases/download/v0.2.0-beta.1/checksums.txt
grep 'wakeplane_0.2.0-beta.1_linux_amd64.tar.gz' checksums.txt | sha256sum -c -
tar -xzf wakeplane_0.2.0-beta.1_linux_amd64.tar.gz
./wakeplane version
```

Each archive contains both `wakeplane` and `wakeplaned`.

## Option 2: `go install`

The repo currently declares `go 1.25.0` in `go.mod`.

```bash
go install github.com/justyn-clark/wakeplane/cmd/wakeplane@v0.2.0-beta.1
go install github.com/justyn-clark/wakeplane/cmd/wakeplaned@v0.2.0-beta.1
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
