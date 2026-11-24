#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
trap 'exit 1' HUP INT TERM
mkdir "$work/runner" "$work/context" "$work/exchange" "$work/worker"
chmod 0700 "$work/exchange" "$work/worker"
chmod 0755 "$work/runner" "$work/context"
sh scripts/build-entware-release.sh v0.0.141 "$work/release"
GOOS=linux GOARCH=arm64 GOARM64=v8.0 CGO_ENABLED=0 go test -trimpath -c   -o "$work/runner/process.test" ./cmd/xkeen-ui-server
GOOS=linux GOARCH=arm64 GOARM64=v8.0 CGO_ENABLED=0 go build -trimpath   -o "$work/context/check" ./cmd/xkeen-validator-run
chmod 0755 "$work/context/check"
go build -trimpath -o "$work/worker/validator" ./cmd/xkeen-validator
LAB_IMAGE=${XKEEN_LAB_IMAGE:-xkeen-router-lab:dev}
export LAB_IMAGE
docker build --platform linux/arm64 --pull=false --network none \
  --build-arg "XRAY_SOURCE=$LAB_IMAGE" --iidfile "$work/worker/image" \
  -f lab/validator.Dockerfile "$work/context"
XKEEN_G18_DOCKER=$(command -v docker)
XKEEN_G18_ENGINE=$(docker context inspect --format '{{.Endpoints.docker.Host}}')
export XKEEN_G18_DOCKER XKEEN_G18_ENGINE
node web/installed-entware-flow.mjs "$work"
