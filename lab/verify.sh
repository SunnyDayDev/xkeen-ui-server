#!/bin/sh
set -eu
configs=/opt/etc/xray/configs
api=127.0.0.1:10085
target=http://127.0.0.1:18080/
fingerprint() {
  pid=$(/usr/bin/pgrep -x xray)
  test -n "$pid"
  printf '%s ' "$pid"
  awk '{print $22}' "/proc/$pid/stat"
}
direct_ok() {
  test "$(curl -fsS --noproxy '*' --max-time 3 "$target")" = router-lab-ok
}
proxy_ok() {
  test "$(curl -fsS --noproxy '' --proxy socks5h://127.0.0.1:1080 --max-time 3 "$target")" = router-lab-ok
}
proxy_blocked() {
  direct_ok
  if curl -fsS --noproxy '' --proxy socks5h://127.0.0.1:1080 --max-time 3 "$target" >/dev/null 2>&1; then
    echo 'FAIL: request should be blocked' >&2
    exit 1
  fi
}
case "${1:-before}" in
  before)
    cat /usr/local/share/router-lab/versions.txt
    opkg list-installed | grep -E '^(busybox|entware-opt|opkg|libc) '
    test -d /opt/etc/init.d
    xray run -test -confdir "$configs"
    direct_ok
    proxy_ok
    echo 'PASS: split configuration, API and baseline traffic'
    before=$(fingerprint)
    candidate=$(mktemp -d /opt/tmp/router-lab.XXXXXX)
    trap 'rm -rf "$candidate"' EXIT
    cp "$configs/"*.json "$candidate/"
    jq '.inbounds[0].protocol="invalid-lab-protocol"' "$configs/03_inbounds.json" > "$candidate/03_inbounds.json"
    if xray run -test -confdir "$candidate" > /opt/tmp/router-lab-invalid.log 2>&1; then
      echo 'FAIL: invalid candidate accepted' >&2
      exit 1
    fi
    proxy_ok
    test "$(fingerprint)" = "$before"
    echo 'PASS: invalid full configuration rejected before apply'
    cp "$configs/03_inbounds.json" "$candidate/03_inbounds.json"
    jq '.routing.rules=[{"ruleTag":"lab-block","type":"field","inboundTag":["lab-socks"],"outboundTag":"block"}]' "$configs/05_routing.json" > "$candidate/05_routing.json"
    xray run -test -confdir "$candidate"
    xray api adrules --server="$api" --append=false "$candidate/05_routing.json"
    test "$(fingerprint)" = "$before"
    proxy_blocked
    xray api lsrules --server="$api" > "$candidate/runtime-rules.txt"
    grep -q 'lab-block' "$candidate/runtime-rules.txt"
    if grep -q 'lab-route' "$candidate/runtime-rules.txt"; then
      echo 'FAIL: previous rule was not removed' >&2
      exit 1
    fi
    echo 'PASS: complete rule replacement; traffic blocked; process unchanged'
    cp "$candidate/05_routing.json" "$configs/.routing.next"
    mv "$configs/.routing.next" "$configs/05_routing.json"
    printf '%s\n' "$before" > "$configs/.lab-before"
    echo 'PASS: routing file saved; ready for container restart'
    ;;
  after-service)
    test "$(fingerprint)" != "$(cat "$configs/.lab-before")"
    proxy_blocked
    fingerprint > "$configs/.lab-before"
    echo 'PASS: blocked behavior persists after xkeen -restart'
    ;;
  after)
    test "$(fingerprint)" != "$(cat "$configs/.lab-before")"
    proxy_blocked
    echo 'PASS: blocked behavior persists after restart'
    xray api adrules --server="$api" --append=false /usr/local/share/router-lab/configs/05_routing.json
    proxy_ok
    cp /usr/local/share/router-lab/configs/05_routing.json "$configs/.routing.next"
    mv "$configs/.routing.next" "$configs/05_routing.json"
    echo 'PASS: baseline routing restored through API'
    ;;
  *) echo 'Usage: verify.sh before|after-service|after' >&2; exit 2 ;;
esac
