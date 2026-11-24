#!/bin/sh
set -eu

if [ "$#" -gt 1 ] || [ "${1:-}" = "--help" ]; then
  printf '%s\n' 'Usage: sh scripts/build-agent.sh [output-directory]'
  [ "$#" -le 1 ]
  exit
fi

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=${1:-"$root/.build"}
export GOTOOLCHAIN=local
expected=$(awk '$1 == "toolchain" { print $2 }' "$root/go.mod")
actual=$(go env GOVERSION)
if [ "$actual" != "$expected" ]; then
  printf 'Go version mismatch: got %s, want %s\n' "$actual" "$expected" >&2
  exit 1
fi

mkdir -p "$output"
output=$(CDPATH= cd -- "$output" && pwd)
build=$(mktemp -d "$output/.agent-build.XXXXXX")
trap 'rm -rf "$build"' EXIT
trap 'exit 1' HUP INT TERM
cd "$root"

export CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0
export GOFLAGS= GOEXPERIMENT=
agent=xkeen-agent-linux-arm64
supervisor=xkeen-local-validator-linux-arm64
go build -mod=readonly -trimpath -buildvcs=false -o "$build/$agent" ./cmd/xkeen-agent
go build -mod=readonly -trimpath -buildvcs=false -o "$build/$supervisor" ./cmd/xkeen-local-validator
(
  cd "$build"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$agent" "$supervisor"
  else
    shasum -a 256 "$agent" "$supervisor"
  fi
) > "$build/SHA256SUMS"

mv -f "$build/$agent" "$build/$supervisor" "$output/"
mv -f "$build/SHA256SUMS" "$output/"
printf '%s\n' "$output/$agent" "$output/$supervisor" "$output/SHA256SUMS"
