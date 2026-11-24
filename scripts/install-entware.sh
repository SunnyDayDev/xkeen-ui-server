#!/opt/bin/sh
set -eu

PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH LC_ALL=C
unset CDPATH CURL_CA_BUNDLE SSL_CERT_FILE SSL_CERT_DIR
umask 077

refuse() {
  printf '%s\n' "$1" >&2
  exit 1
}

action=${1:-}
case "$action" in install|update) ;; *) refuse 'expected install or update command' ;; esac
shift
role='' version='' manifest='' run_as='' server='' server_ca='' invite='' mirror='' mirror_ca='' listen='' web_origin='' tls_cert='' tls_key='' local_cfg='' app_cfg=''
agent_origin='' validator_origin='' validator_ca='' validator_cert='' validator_key=''
while [ "$#" -gt 0 ]; do
  [ "$#" -ge 2 ] && [ -n "$2" ] || refuse 'missing installation option value'
  case "$1" in
    --role) [ -z "$role" ] || refuse 'duplicate option'; role=$2 ;;
    --version) [ -z "$version" ] || refuse 'duplicate option'; version=$2 ;;
    --manifest-sha256) [ -z "$manifest" ] || refuse 'duplicate option'; manifest=$2 ;;
    --run-as) [ -z "$run_as" ] || refuse 'duplicate option'; run_as=$2 ;;
    --server) [ -z "$server" ] || refuse 'duplicate option'; server=$2 ;;
    --ca-file) [ -z "$server_ca" ] || refuse 'duplicate option'; server_ca=$2 ;;
    --invitation-file) [ -z "$invite" ] || refuse 'duplicate option'; invite=$2 ;;
    --listen) [ -z "$listen" ] || refuse 'duplicate option'; listen=$2 ;;
    --web-origin) [ -z "$web_origin" ] || refuse 'duplicate option'; web_origin=$2 ;;
    --tls-cert) [ -z "$tls_cert" ] || refuse 'duplicate option'; tls_cert=$2 ;;
    --tls-key) [ -z "$tls_key" ] || refuse 'duplicate option'; tls_key=$2 ;;
    --local-config) [ -z "$local_cfg" ] || refuse 'duplicate option'; local_cfg=$2 ;;
    --application-config) [ -z "$app_cfg" ] || refuse 'duplicate option'; app_cfg=$2 ;;
    --agent-origin) [ -z "$agent_origin" ] || refuse 'duplicate option'; agent_origin=$2 ;;
    --validator-origin) [ -z "$validator_origin" ] || refuse 'duplicate option'; validator_origin=$2 ;;
    --validator-ca) [ -z "$validator_ca" ] || refuse 'duplicate option'; validator_ca=$2 ;;
    --validator-client-cert) [ -z "$validator_cert" ] || refuse 'duplicate option'; validator_cert=$2 ;;
    --validator-client-key) [ -z "$validator_key" ] || refuse 'duplicate option'; validator_key=$2 ;;
    --release-mirror) [ -z "$mirror" ] || refuse 'duplicate option'; mirror=$2 ;;
    --mirror-ca-file) [ -z "$mirror_ca" ] || refuse 'duplicate option'; mirror_ca=$2 ;;
    *) refuse 'unknown installation option' ;;
  esac
  shift 2
done

[ "$(/opt/bin/id -u)" = 0 ] || refuse 'installation requires root'
[ "$(/opt/bin/uname -sm)" = 'Linux aarch64' ] || refuse 'unsupported Entware architecture'
printf '%s\n' "$version" | /opt/bin/grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
  refuse 'invalid release tag'
[ "${#version}" -le 40 ] || refuse 'invalid release tag'
case "$version" in *[!v.0-9]*) refuse 'invalid release tag' ;; esac
case "$manifest" in ''|*[!0-9a-f]*) refuse 'invalid manifest SHA-256' ;; esac
[ "${#manifest}" -eq 64 ] || refuse 'invalid manifest SHA-256'
if [ "$role" != server ] && [ -n "$agent_origin$validator_origin$validator_ca$validator_cert$validator_key" ]; then
  refuse 'external validation and agent origin apply to the server role'
fi
if [ -n "$validator_origin$validator_ca$validator_cert$validator_key" ]; then
  [ -n "$validator_origin" ] && [ -n "$validator_ca" ] &&
    [ -n "$validator_cert" ] && [ -n "$validator_key" ] ||
    refuse 'complete external validator configuration is required'
fi

case "$action:$role" in
  install:agent-transport)
    [ -n "$run_as" ] && [ -n "$server" ] && [ -z "$listen$web_origin$tls_cert$tls_key$local_cfg$app_cfg" ] ||
      refuse 'service identity and HTTPS origin are required' ;;
  update:agent-transport)
    [ -z "$run_as$server$server_ca$invite$listen$web_origin$tls_cert$tls_key$local_cfg$app_cfg" ] ||
      refuse 'update uses the installed service profile' ;;
  install:server)
    [ -n "$run_as" ] && [ -n "$listen" ] && [ -n "$web_origin" ] && [ -n "$tls_cert" ] &&
      [ -n "$tls_key" ] && [ -z "$server$server_ca$invite$local_cfg$app_cfg" ] ||
      refuse 'server installation requires identity, listen, origin and both TLS files' ;;
  install:agent-application)
    [ -n "$run_as" ] && [ -n "$server" ] && [ -n "$local_cfg" ] && [ -n "$app_cfg" ] &&
      [ -z "$server_ca$listen$web_origin$tls_cert$tls_key" ] ||
      refuse 'unsupported Entware installation role without a managed application profile' ;;
  *) refuse 'unsupported Entware installation role' ;;
esac

base=https://github.com/SunnyDayDev/xkeen-ui-server
if [ -n "$mirror" ]; then
  case "$mirror" in https://?*) ;; *) refuse 'mirror must use HTTPS' ;; esac
  [ -n "$mirror_ca" ] || refuse 'mirror requires an explicit CA file'
  origin=${mirror%/}
  case "${origin#https://}" in
    ''|*[!0-9A-Za-z.:-]*) refuse 'invalid HTTPS mirror origin' ;;
  esac
  base=$origin
elif [ -n "$mirror_ca" ]; then
  refuse 'mirror CA requires an explicit HTTPS mirror'
fi

# An unprivileged user must not be able to replace the private staging path.
for directory in /opt /opt/tmp; do
  [ -d "$directory" ] && [ ! -L "$directory" ] && [ -O "$directory" ] ||
    refuse 'unsafe Entware staging directory'
  permissions=$(/opt/bin/ls -ld -- "$directory") || refuse 'cannot inspect Entware staging directory'
  case "$permissions" in
    ?????w*|????????w*)
      [ "$directory" = /opt/tmp ] && [ -k "$directory" ] || refuse 'unsafe Entware staging directory' ;;
  esac
done
stage=$(/opt/bin/mktemp -d /opt/tmp/xkeen-install.XXXXXX) || refuse 'cannot create private staging'
trap '/opt/bin/rm -rf -- "$stage"' EXIT
trap 'exit 1' HUP INT TERM
download() {
  name=$1
  limit=$2
  shift 2
  /opt/bin/curl -q --fail --silent --globoff --location --noproxy '*' \
    --proto '=https' --proto-redir '=https' --max-redirs 5 --connect-timeout 15 --max-time 180 \
    --max-filesize "$limit" --output "$stage/$name" "$@" -- "$base/releases/download/$version/$name" ||
    refuse 'cannot download selected release asset'
}
fetch() {
  if [ -n "$mirror_ca" ]; then
    download "$1" "$2" --cacert "$mirror_ca"
  else
    download "$1" "$2"
  fi
}
verify() {
  printf '%s  %s\n' "$1" "$2" | /opt/bin/sha256sum -c - >/dev/null 2>&1 ||
    refuse 'release checksum mismatch'
}
asset_digest() {
  /opt/bin/awk -v name="$1" '
    $2 == name {
      if ($0 != ($1 "  " name) || length($1) != 64 || $1 !~ /^[0-9a-f]+$/) { invalid = 1; exit 1 }
      count++
      digest = $1
    }
    END {
      if (invalid || count != 1) exit 1
      print digest
    }
  ' "$stage/SHA256SUMS"
}

fetch SHA256SUMS 65536
verify "$manifest" "$stage/SHA256SUMS"
helper=xkeen-entware-installer-linux-arm64
asset=xkeen-agent-linux-arm64
[ "$role" != server ] || asset=xkeen-ui-server-linux-arm64
set -- "$helper" "$asset"
[ "$role" != agent-application ] || set -- "$@" xkeen-local-validator-linux-arm64
for name do
  digest=$(asset_digest "$name") || refuse 'release manifest does not describe required assets exactly once'
  fetch "$name" 67108864
  verify "$digest" "$stage/$name"
done

/opt/bin/chmod 0700 "$stage/$helper"
if [ "$action" = update ]; then
  format=agent-state-format.txt
  if /opt/bin/awk -v name="$format" '$2 == name { found=1 } END { exit !found }' "$stage/SHA256SUMS"; then
    digest=$(asset_digest "$format") || refuse 'invalid state format declaration in release manifest'
    fetch "$format" 256
    verify "$digest" "$stage/$format"
  fi
  "$stage/$helper" update-stage "$stage" "$version" "$manifest"
elif [ "$role" = server ]; then
  "$stage/$helper" install-server-stage "$stage" "$version" "$manifest" "$run_as" "$listen" "$web_origin" "$tls_cert" "$tls_key" \
    "$agent_origin" "$validator_origin" "$validator_ca" "$validator_cert" "$validator_key"
elif [ "$role" = agent-application ]; then
  "$stage/$helper" install-application-stage "$stage" "$version" "$manifest" "$run_as" "$server" "$invite" "$local_cfg" "$app_cfg"
else
  "$stage/$helper" install-stage "$stage" "$version" "$manifest" "$run_as" "$server" "$server_ca" "$invite"
fi
