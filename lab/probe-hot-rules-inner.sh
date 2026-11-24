#!/bin/bash
set -eu
f=/opt/etc/xray/configs/05_routing.json
api() { xray api "$1" --server=127.0.0.1:10085 "${@:2}"; }
fp() { p=$(pgrep -x xray); echo "$p $(awk '{print $22}' /proc/$p/stat)"; }
before=$(fp)
for port in 18081 18082; do
  /bin/busybox httpd -p "127.0.0.1:$port" -h /usr/local/share/router-lab/www
done
fetch() { curl -fsS --max-time 3 "$@" ; }
for target in block direct; do
  jq --arg t "$target" '.routing.rules=[
    {ruleTag:"health-deny",type:"field",inboundTag:["lab-socks"],port:"18082",outboundTag:"block"},
    {ruleTag:"switch",type:"field",inboundTag:["lab-socks"],port:"18080",outboundTag:$t,_xkeenUiId:"00000000-0000-4000-8000-000000000001"},
    {ruleTag:"default",type:"field",inboundTag:["lab-socks"],outboundTag:"direct"}]' "$f" >/tmp/set.json
  api adrules --append=false /tmp/set.json >/dev/null
  api lsrules | jq -e --arg t "$target" '.rules == [{ruleTag:"health-deny",tag:"block"},{ruleTag:"switch",tag:$t},{ruleTag:"default",tag:"direct"}]' >/dev/null
  for port in 18080 18081 18082; do
    url="http://127.0.0.1:$port/"
    test "$(fetch --noproxy '*' "$url")" = router-lab-ok
    if result=$(fetch --noproxy '' --proxy socks5h://127.0.0.1:1080 "$url" 2>/dev/null); then
      test "$result" = router-lab-ok
      test "$port" = 18081 || { test "$port" = 18080 && test "$target" = direct; }
    else
      test "$port" = 18082 || { test "$port" = 18080 && test "$target" = block; }
    fi
  done
  test "$(fp)" = "$before"
done
