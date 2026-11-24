#!/bin/sh
# Exercise synthetic local administration and HTTPS on capped Entware ARM64.
set -eu

build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
chmod 0755 "$build_dir"
export CGO_ENABLED=0 GOOS=linux GOARCH=arm64
go build -trimpath -o "$build_dir/xkeen-server" ./cmd/xkeen-ui-server
go test -trimpath -c -o "$build_dir/process.test" ./cmd/xkeen-ui-server

docker run --rm --platform linux/arm64 --cgroupns=private --network=none --read-only \
  --user 65534:65534 --memory=256m --memory-swap=256m --pids-limit=64 \
  --cap-drop=ALL --security-opt=no-new-privileges --tmpfs /tmp:rw,nosuid,nodev \
  --mount "type=bind,src=$build_dir,dst=/proof,readonly" \
  --env XKEEN_TEST_SERVER_BINARY=/proof/xkeen-server --entrypoint /bin/sh \
  "${XKEEN_LAB_IMAGE:-xkeen-router-lab:dev}" -ec '
uname -smr
grep -E "^Entware=" /usr/local/share/router-lab/versions.txt
test "$(cat /sys/fs/cgroup/memory.max)" = 268435456
/proof/process.test -test.run "^(TestProcessWeb|TestAdminPrivatePasswordFile)" -test.v
printf "cgroup memory peak (bytes): "
cat /sys/fs/cgroup/memory.peak
grep "^oom_kill 0$" /sys/fs/cgroup/memory.events
'
