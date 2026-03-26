# Install

Use one of these supported install paths for Wakeplane. The canonical repository is [github.com/justyn-clark/wakeplane](https://github.com/justyn-clark/wakeplane).

> **Operator warning:** installability does not change the security model. Wakeplane still has no auth or RBAC. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network.

## Option 1: GitHub Releases

Preferred for operators. Tagged releases publish platform archives and a checksum file on the [GitHub Releases page](https://github.com/justyn-clark/wakeplane/releases).

Supported artifacts per tagged release:

- `wakeplane_<version>_darwin_arm64.tar.gz`
- `wakeplane_<version>_linux_amd64.tar.gz`
- `wakeplane_<version>_linux_arm64.tar.gz`
- `checksums.txt`

Example verification flow:

```bash
curl -fsSLO https://github.com/justyn-clark/wakeplane/releases/download/v0.1.0/wakeplane_0.1.0_linux_amd64.tar.gz
curl -fsSLO https://github.com/justyn-clark/wakeplane/releases/download/v0.1.0/checksums.txt
grep 'wakeplane_0.1.0_linux_amd64.tar.gz' checksums.txt | sha256sum -c -
tar -xzf wakeplane_0.1.0_linux_amd64.tar.gz
./wakeplane help
```

Each archive contains both `wakeplane` and `wakeplaned`.

## Option 2: `go install`

The repo currently declares `go 1.25.0` in `go.mod`.

```bash
go install github.com/justyn-clark/wakeplane/cmd/wakeplane@latest
go install github.com/justyn-clark/wakeplane/cmd/wakeplaned@latest
wakeplane help
```

## Option 3: Source build

```bash
git clone https://github.com/justyn-clark/wakeplane.git
cd wakeplane
go build ./cmd/wakeplane
go build ./cmd/wakeplaned
./wakeplane help
```

## Smoke test after install

```bash
WAKEPLANE_DB_PATH=./wakeplane.db \
WAKEPLANE_HTTP_ADDR=:8080 \
WAKEPLANE_WORKER_ID=wrk_local \
./wakeplane serve
```

In another terminal:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

The health and readiness probes are enough for the initial install smoke test. Create schedules with the API or CLI after you have chosen a working directory and manifest location.
