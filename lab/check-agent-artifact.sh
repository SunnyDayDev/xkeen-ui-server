#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
trap 'exit 1' HUP INT TERM
chmod 0755 "$build_dir"

sh scripts/build-agent.sh "$build_dir"
agent=xkeen-agent-linux-arm64
supervisor=xkeen-local-validator-linux-arm64
(
  cd "$build_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 -c SHA256SUMS
  fi
)
for binary in "$agent" "$supervisor"; do
  file "$build_dir/$binary" | grep -Eq 'ELF 64-bit.*(ARM aarch64|ARM64).*statically linked'
  go version -m "$build_dir/$binary" > "$build_dir/metadata"
  expected=$(awk '$1 == "toolchain" { print $2 }' go.mod)
  grep -q ": $expected\$" "$build_dir/metadata"
  grep -q 'GOOS=linux' "$build_dir/metadata"
  grep -q 'GOARCH=arm64' "$build_dir/metadata"
  grep -q 'GOARM64=v8.0' "$build_dir/metadata"
  grep -q 'CGO_ENABLED=0' "$build_dir/metadata"
done

export CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0
go build -trimpath -o "$build_dir/server" ./cmd/xkeen-ui-server
go test -c -o "$build_dir/enrollment.test" ./cmd/xkeen-ui-server
image=${XKEEN_LAB_IMAGE:-xkeen-router-lab:dev}
mount="type=bind,source=$build_dir,target=/proof,readonly"
docker run --rm --platform linux/arm64 --network none --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges --user 65532:65532 \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,mode=1777 --mount "$mount" \
  -e XKEEN_TEST_SERVER_BINARY=/proof/server -e "XKEEN_TEST_AGENT_BINARY=/proof/$agent" \
  --entrypoint /proof/enrollment.test "$image" \
  -test.run '^TestProcessRouterAgentEnrollRestartAndRecover$' -test.v -test.timeout 1m
tick=$(docker run --rm --platform linux/arm64 --network none --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges --user 0:0 --mount "$mount" \
  --entrypoint "/proof/$supervisor" "$image" --working-process 1)
case $tick in
  ""|*[!0-9]*|0) exit 1 ;;
esac
printf '%s\n' 'PASS: static ARM64 agent and supervisor, verified hashes, Entware TLS enrollment and restart'
