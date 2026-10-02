#!/bin/sh
# Contract tests for validation orchestration. Real Xray is checked separately.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/xkeen-catalog-contract.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir "$work/bin" "$work/tmp"
cat > "$work/bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$CATALOG_DOCKER_LOG"
if [ "${CATALOG_DOCKER_MODE:-}" = unavailable ]; then exit 125; fi
case "$*" in
  *version*)
    if [ "${CATALOG_DOCKER_MODE:-}" = wrong-version ]; then echo 'Xray 99.1.1'; else echo 'Xray 26.3.27'; fi ;;
  *negative*) echo 'unknown protocol: invalid-demo-protocol'; exit 23 ;;
  *-test*) echo 'Configuration OK.' ;;
esac
SH
chmod +x "$work/bin/docker"
export CATALOG_DOCKER_LOG="$work/docker.log"
export PATH="$work/bin:$PATH"
TMPDIR="$work/tmp" sh "$root/lab/check-catalog.sh" > "$work/result" 2>&1
rg -q 'positive: PASS' "$work/result"
rg -q 'negative: rejected' "$work/result"
rg -q -- '--network none' "$work/docker.log"
rg -q -- '--read-only' "$work/docker.log"
rg -q -- 'readonly' "$work/docker.log"
rg -q -- '--entrypoint /opt/sbin/xray' "$work/docker.log"
for mode in wrong-version unavailable; do
  if CATALOG_DOCKER_MODE="$mode" TMPDIR="$work/tmp" sh "$root/lab/check-catalog.sh" > "$work/$mode" 2>&1; then
    echo "false success: $mode" >&2; exit 1
  fi
done
# Compiler tools may keep their own cache in TMPDIR. Only script-owned resources
# are required to disappear; unrelated cache files must not be deleted.
for path in "$work/tmp"/xkeen-catalog.*; do [ ! -e "$path" ]; done
echo 'catalog orchestration contracts: PASS'
