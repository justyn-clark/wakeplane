#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: scripts/build-release-artifacts.sh <version> [output_dir]}"
out_dir="${2:-dist/releases}"

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$out_dir"
rm -f "$out_dir"/checksums.txt

checksum_file="$out_dir/checksums.txt"

checksum() {
  local digest
  if command -v sha256sum >/dev/null 2>&1; then
    digest="$(sha256sum "$1" | awk '{print $1}')"
  else
    digest="$(shasum -a 256 "$1" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "$digest" "$(basename "$1")" >>"$checksum_file"
}

build_target() {
  local goos="$1"
  local goarch="$2"
  local archive="wakeplane_${version#v}_${goos}_${goarch}.tar.gz"
  local stage
  stage="$(mktemp -d)"
  trap 'rm -rf "$stage"' RETURN

  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$stage/wakeplane" ./cmd/wakeplane
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$stage/wakeplaned" ./cmd/wakeplaned

  tar -C "$stage" -czf "$out_dir/$archive" wakeplane wakeplaned
  checksum "$out_dir/$archive"
}

cd "$repo_root"
build_target darwin arm64
build_target linux amd64
build_target linux arm64
