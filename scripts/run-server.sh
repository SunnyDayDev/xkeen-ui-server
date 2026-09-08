#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
mkdir -p .build
CGO_ENABLED=0 go build -trimpath -o .build/xkeen-ui-server ./cmd/xkeen-ui-server
exec ./.build/xkeen-ui-server "$@"
