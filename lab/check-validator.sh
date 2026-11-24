#!/bin/sh
# Synthetic catalogs and ephemeral client identities; no production server or agent.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$root"
work=$(mktemp -d "${TMPDIR:-/tmp}/xkeen-validator.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
engine=$(docker context inspect --format '{{.Endpoints.docker.Host}}')
case "$engine" in unix://*) ;; *) echo 'A local Unix Docker Engine is required' >&2; exit 1;; esac
export XKEEN_TEST_ENGINE=${engine#unix://}
XKEEN_TEST_DOCKER=$(command -v docker)
export XKEEN_TEST_DOCKER
export XKEEN_TEST_ENTWARE_IMAGE=${LAB_IMAGE:-xkeen-router-lab:dev}
docker image inspect "$XKEEN_TEST_ENTWARE_IMAGE" >/dev/null
mkdir "$work/image-context"
arch=$(docker image inspect --format '{{.Architecture}}' "$XKEEN_TEST_ENTWARE_IMAGE")
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 "${GO:-go}" build -trimpath -o "$work/image-context/check" ./cmd/xkeen-validator-run
docker build --platform "linux/$arch" --network=none --pull=false --build-arg "XRAY_SOURCE=$XKEEN_TEST_ENTWARE_IMAGE" --iidfile "$work/image" -f lab/validator.Dockerfile "$work/image-context"
XKEEN_TEST_VALIDATOR_IMAGE=$(cat "$work/image")
export XKEEN_TEST_VALIDATOR_IMAGE XKEEN_TEST_FIXTURES="$work" XKEEN_TEST_ENTWARE_BINARY="$work/client.test"
mkdir "$work/positive" "$work/negative"
"${GO:-go}" build -o "$work/demo" ./cmd/xkeen-config-demo
"$work/demo" -dir "$work/positive"
"$work/demo" -dir "$work/negative" -invalid-protocol
# The parent is private. The synthetic read-only child mounted into the client is traversable.
chmod 0755 "$work/positive"
chmod 0444 "$work/positive/"*.json
case "$(docker info --format '{{.OperatingSystem}}')" in *Desktop*) ;; *) export XKEEN_TEST_CLIENT_HOST_NET=1;; esac
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 "${GO:-go}" test -c -o "$work/client.test" ./internal/validation
"${GO:-go}" test -count=1 -run '^TestDockerRealCatalog$' -v ./internal/validation
