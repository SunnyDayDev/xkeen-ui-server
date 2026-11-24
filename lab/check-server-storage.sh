#!/bin/sh
# Exercise the Linux ARM64 server and storage adapter in a disposable Entware container.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
image=${LAB_IMAGE:-xkeen-router-lab:dev}
work=$(mktemp -d "${TMPDIR:-/tmp}/xkeen-storage.XXXXXX")
trap 'rm -rf "$work"' EXIT
chmod 0755 "$work"
cd "$root"
docker image inspect "$image" >/dev/null
export GOOS=linux GOARCH=arm64 CGO_ENABLED=0
"${GO:-go}" build -trimpath -o "$work/xkeen-ui-server" ./cmd/xkeen-ui-server
"${GO:-go}" test -c -o "$work/storage.test" ./internal/storage/sqlite
"${GO:-go}" test -c -o "$work/server.test" ./cmd/xkeen-ui-server
wc -c < "$work/xkeen-ui-server"
docker run --rm --platform linux/arm64 --network none --read-only \
  --user 65534:65534 --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp:rw,mode=1777 \
  --tmpfs /opt/tmp:rw,mode=1777 \
  --mount "type=bind,src=$work,dst=/probe,readonly" \
  --entrypoint /opt/bin/sh "$image" -ec '
uname -smr
/opt/bin/opkg list-installed busybox
/opt/bin/opkg list-installed libc
export XKEEN_TEST_SERVER_BINARY=/probe/xkeen-ui-server
/probe/storage.test -test.v
/probe/server.test -test.v -test.run "^TestProcessStorage"
'
