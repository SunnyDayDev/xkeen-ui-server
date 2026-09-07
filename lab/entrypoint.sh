#!/bin/sh
set -eu
configs=/opt/etc/xray/configs
mkdir -p "$configs"
if [ -z "$(ls -A "$configs")" ]; then
  cp /usr/local/share/router-lab/configs/*.json "$configs/"
fi
start_target() {
  /bin/busybox httpd -p 127.0.0.1:18080 -h /usr/local/share/router-lab/www
}
case "${1:-xkeen}" in
  xray)
    xray run -test -confdir "$configs"
    start_target
    exec xray run -confdir "$configs"
    ;;
  xkeen)
    xray run -test -confdir "$configs"
    start_target
    /opt/sbin/xkeen -start
    /usr/bin/pgrep -x xray >/dev/null
    stop_xkeen() { /opt/sbin/xkeen -stop; exit 0; }
    trap stop_xkeen INT TERM
    while :; do sleep 3600 & wait "$!" || true; done
    ;;
  xkeen-probe)
    cat /usr/local/share/router-lab/versions.txt
    start_target
    /usr/bin/timeout 45 /opt/sbin/xkeen -start
    /opt/sbin/xkeen -status
    xray api statsquery --server=127.0.0.1:10085
    curl -fsS --noproxy '' --proxy socks5h://127.0.0.1:1080 --max-time 3 http://127.0.0.1:18080/
    ;;
  *) exec "$@" ;;
esac
