#!/bin/sh
set -eu
dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project=xkeen-e06-check-$$
dc() { docker compose -p "$project" -f "$dir/compose.yaml" "$@"; }
trap 'dc down --volumes >/dev/null' EXIT
dc up -d --no-build --wait --wait-timeout 40 router
dc exec -T router /bin/bash -s < "$dir/probe-hot-rules-inner.sh"
echo 'PASS: ordered runtime routes, direct and proxied traffic, stable process'
