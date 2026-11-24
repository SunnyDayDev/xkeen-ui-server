#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT
trap 'exit 1' HUP INT TERM
chmod 0755 "$build"

export CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0
go build -trimpath -o "$build/server" ./cmd/xkeen-ui-server
go build -trimpath -o "$build/xkeen-agent-linux-arm64" ./cmd/xkeen-agent
go build -trimpath -o "$build/xkeen-entware-installer-linux-arm64" ./cmd/xkeen-entware-installer
go test -c -o "$build/installation.test" ./cmd/xkeen-ui-server

image=${XKEEN_LAB_IMAGE:-xkeen-router-lab:dev}
docker run --rm --platform linux/arm64 --network none --cap-add SYS_PTRACE \
  --mount "type=bind,source=$build,target=/proof,readonly" \
  --mount "type=bind,source=$root/scripts,target=/scripts,readonly" \
  -e XKEEN_TEST_SERVER_BINARY=/proof/server -e XKEEN_TEST_ENTWARE_INSTALLER=/scripts/install-entware.sh \
  --entrypoint /proof/installation.test "$image" \
  -test.run '^TestEntware(Bootstrap|Install)' -test.v -test.timeout 1m
