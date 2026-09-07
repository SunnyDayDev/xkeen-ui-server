#!/bin/sh
set -eu
lab_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project="xkeen-router-check-$$"
dc() { docker compose -p "$project" -f "$lab_dir/compose.yaml" "$@"; }
cleanup() {
  result=$?
  trap - EXIT
  if [ "$result" -ne 0 ]; then dc logs --tail 50 router || true; fi
  dc down --volumes >/dev/null 2>&1 || true
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
dc up -d --no-build --wait --wait-timeout 40 router
dc exec -T router /usr/local/lib/router-lab/verify.sh before
if [ "${LAB_RUNTIME:-xkeen}" = xkeen ]; then
  dc exec -T router xkeen -restart
  dc exec -T router /usr/local/lib/router-lab/verify.sh after-service
fi
dc restart router
dc up -d --no-build --wait --wait-timeout 40 router
dc exec -T router /usr/local/lib/router-lab/verify.sh after
echo 'PASS: all router lab checks'
