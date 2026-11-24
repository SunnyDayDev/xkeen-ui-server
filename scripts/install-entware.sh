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

[ "${1:-}" = install ] || refuse 'expected install command'
shift
role='' version='' manifest='' run_as='' server='' server_ca='' invite='' mirror='' mirror_ca=''
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
    --release-mirror) [ -z "$mirror" ] || refuse 'duplicate option'; mirror=$2 ;;
    --mirror-ca-file) [ -z "$mirror_ca" ] || refuse 'duplicate option'; mirror_ca=$2 ;;
    *) refuse 'unknown installation option' ;;
  esac
  shift 2
done

[ "$(/opt/bin/id -u)" = 0 ] || refuse 'installation requires root'
[ "$(/opt/bin/uname -sm)" = 'Linux aarch64' ] || refuse 'unsupported Entware architecture'
[ "$role" = agent-transport ] || refuse 'unsupported Entware installation role'
printf '%s\n' "$version" | /opt/bin/grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ||
  refuse 'invalid release tag'
[ "${#version}" -le 40 ] || refuse 'invalid release tag'
case "$version" in *[!v.0-9]*) refuse 'invalid release tag' ;; esac
case "$manifest" in ''|*[!0-9a-f]*) refuse 'invalid manifest SHA-256' ;; esac
[ "${#manifest}" -eq 64 ] || refuse 'invalid manifest SHA-256'
[ -n "$run_as" ] && [ -n "$server" ] || refuse 'service identity and HTTPS origin are required'

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
agent=xkeen-agent-linux-arm64
for name in "$helper" "$agent"; do
  digest=$(asset_digest "$name") || refuse 'release manifest does not describe required assets exactly once'
  fetch "$name" 67108864
  verify "$digest" "$stage/$name"
done

/opt/bin/chmod 0700 "$stage/$helper"
"$stage/$helper" install-stage "$stage" "$version" "$manifest" "$run_as" "$server" "$server_ca" "$invite"
