#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  printf '%s\n' 'Usage: sh scripts/build-entware-release.sh RELEASE_TAG OUTPUT_DIRECTORY' >&2
  exit 2
fi
version=$1
case "$version" in
  ''|*[!v.0-9]*) printf '%s\n' 'invalid release tag' >&2; exit 2 ;;
esac
if [ "${#version}" -gt 40 ] || ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  printf '%s\n' 'invalid release tag' >&2
  exit 2
fi

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$2"
output=$(CDPATH= cd -- "$2" && pwd)
[ ! -e "$output/$version" ] && [ ! -L "$output/$version" ] || {
  printf '%s\n' 'release output already exists' >&2
  exit 1
}
stage=$(mktemp -d "$output/.entware-release.XXXXXX")
lock="$output/.entware-release-$version.lock"
if ! mkdir "$lock"; then
  rmdir "$stage"
  printf '%s\n' 'another local build owns this version' >&2
  exit 1
fi
trap 'rm -rf "$stage"; rmdir "$lock"' EXIT
trap 'exit 1' HUP INT TERM
# Build the exact tracked snapshot named by owner-verification.txt.
commit=$(git -C "$root" rev-parse --verify HEAD)
git -C "$root" archive --format=tar --output="$stage/source.tar" "$commit"
mkdir "$stage/source" "$stage/assets"
tar -xf "$stage/source.tar" -C "$stage/source"
cd "$stage/source"
assets="$stage/assets"
sh scripts/build-agent.sh "$assets"
export GOTOOLCHAIN=local GOFLAGS= GOEXPERIMENT= CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0
installer=xkeen-entware-installer-linux-arm64
server=xkeen-ui-server-linux-arm64
go build -mod=readonly -trimpath -buildvcs=false -o "$assets/$installer" ./cmd/xkeen-entware-installer
go build -mod=readonly -trimpath -buildvcs=false -o "$assets/$server" ./cmd/xkeen-ui-server
cp scripts/install-entware.sh "$assets/install-entware.sh"

hash() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}
verify_hashes() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 -c SHA256SUMS
  fi
}
(
  cd "$assets"
  set -- xkeen-agent-linux-arm64 xkeen-local-validator-linux-arm64 "$installer" "$server"
  for binary do
    file "$binary" | grep -Eq 'ELF 64-bit.*(ARM aarch64|ARM64).*statically linked'
    metadata=$(go version -m "$binary")
    for setting in 'GOOS=linux' 'GOARCH=arm64' 'GOARM64=v8.0' 'CGO_ENABLED=0'; do
      printf '%s\n' "$metadata" | grep -Fq "$setting"
    done
  done
  printf '%s\n' 'agent-transport-state-v1' > agent-state-format.txt
  hash "$@" agent-state-format.txt > SHA256SUMS
  verify_hashes
  installer_line=$(hash install-entware.sh)
  manifest_line=$(hash SHA256SUMS)
  printf 'version %s\ninstaller_commit %s\ninstaller_sha256 %s\nmanifest_sha256 %s\n' \
    "$version" "$commit" "${installer_line%% *}" "${manifest_line%% *}" > owner-verification.txt
)
if [ -e "$output/$version" ] || [ -L "$output/$version" ]; then
  printf '%s\n' 'release output already exists' >&2
  exit 1
fi
mv "$assets" "$output/$version"
printf '%s\n' "$output/$version"
