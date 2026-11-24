#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
platform=${XKEEN_TEST_SERVER_PLATFORM:-linux/arm64}
fixture=$(mktemp -d)
image=xkeen-server-check:local-$$
name=xkeen-server-check-$$
data=$name-data
certs=$name-certs
password=$name-password

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$data" "$certs" "$password" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf "$fixture"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
umask 077

docker buildx build --platform "$platform" --load --tag "$image" .
test "$(docker image inspect --format '{{.Config.User}}' "$image")" = 65532:65532
docker volume create "$data" >/dev/null
docker volume create "$certs" >/dev/null
docker volume create "$password" >/dev/null

stop_server() {
  docker stop --time 10 "$name" >/dev/null
  test "$(docker inspect --format '{{.State.ExitCode}}' "$name")" = 0
  docker rm "$name" >/dev/null
}

mapped_port() {
  docker port "$name" 8080/tcp | sed -n 's/^127\.0\.0\.1://p'
}

wait_for_health() {
  for attempt in $(seq 1 50); do
    if "$@" >/dev/null 2>&1; then return; fi
    sleep .2
  done
  docker logs "$name" >&2
  return 1
}

docker run -d --platform "$platform" --name "$name" --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges -p 127.0.0.1::8080 \
  --mount "type=volume,source=$data,target=/var/lib/xkeen-ui-server" "$image" >/dev/null
port=$(mapped_port)
test -n "$port"
wait_for_health curl --noproxy '*' --max-time 3 -fsS "http://127.0.0.1:$port/healthz"
stop_server

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=controller.example \
  -addext subjectAltName=DNS:controller.example \
  -out "$fixture/cert.pem" -keyout "$fixture/key.pem" >/dev/null 2>&1
openssl rand -hex 24 > "$fixture/password"
awk 'NR == 1 { printf "{\"password\":\"%s\"}\n", $0 }' "$fixture/password" > "$fixture/login.json"

helper=$(awk '$1 == "FROM" && $NF == "build" { print $(NF-2); exit }' Dockerfile)
test -n "$helper"
COPYFILE_DISABLE=1 tar -C "$fixture" -cf - cert.pem key.pem password | docker run --rm -i --network none \
  --mount "type=volume,source=$certs,target=/tls" \
  --mount "type=volume,source=$password,target=/password" \
  "$helper" sh -ec '
    umask 077
    tmp=$(mktemp -d)
    tar -xf - -C "$tmp"
    install -o 65532 -g 65532 -m 0600 "$tmp/cert.pem" "$tmp/key.pem" /tls/
    install -o 65532 -g 65532 -m 0600 "$tmp/password" /password/password
    chown 65532:65532 /tls /password
    chmod 0700 /tls /password
  '

start_tls() {
  docker run -d --platform "$platform" --name "$name" --read-only --cap-drop=ALL \
    --security-opt=no-new-privileges -p 127.0.0.1::8080 \
    --mount "type=volume,source=$data,target=/var/lib/xkeen-ui-server" \
    --mount "type=volume,source=$certs,target=/tls,readonly" "$image" \
    --listen 0.0.0.0:8080 --tls-cert /tls/cert.pem --tls-key /tls/key.pem \
    --web-origin https://controller.example >/dev/null
  port=$(mapped_port)
  test -n "$port"
  wait_for_health https /healthz
}

https() {
  endpoint=$1
  shift
  curl --noproxy '*' --max-time 3 -fsS --cacert "$fixture/cert.pem" \
    --connect-to "controller.example:443:127.0.0.1:$port" \
    "https://controller.example$endpoint" "$@"
}

start_tls
https /ui/v1/session | grep -q '"state":"setup_required"'
https /ui/ > "$fixture/index.html"
asset=$(sed -n 's@.*src="\(/ui/assets/[^"]*\.js\)".*@\1@p' "$fixture/index.html")
test -n "$asset"
https "$asset" > "$fixture/client.js"
test -s "$fixture/client.js"
stop_server

docker run --rm --platform "$platform" --network none --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --mount "type=volume,source=$data,target=/var/lib/xkeen-ui-server" \
  --mount "type=volume,source=$password,target=/password,readonly" "$image" \
  admin setup --password-file /password/password | grep -q 'administrator password saved'

start_tls
https /ui/v1/login -c "$fixture/cookies" -H 'Origin: https://controller.example' \
  -H 'Content-Type: application/json' --data-binary "@$fixture/login.json" | grep -q '"state":"active"'
stop_server

start_tls
https /ui/v1/session -b "$fixture/cookies" | grep -q '"state":"active"'
https /ui/v1/login -H 'Origin: https://controller.example' \
  -H 'Content-Type: application/json' --data-binary "@$fixture/login.json" | grep -q '"state":"active"'
stop_server

printf 'PASS %s: nonroot, clean SQLite, HTTPS, embedded UI, administrator and session persistence\n' "$platform"
