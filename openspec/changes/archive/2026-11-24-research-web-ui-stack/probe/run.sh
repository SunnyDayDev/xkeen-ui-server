#!/bin/sh
set -eu
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
cp -R "$source_dir"/react "$source_dir"/svelte "$source_dir"/htmx "$build_dir/"
for file in package.json package-lock.json style.css measure.mjs browser.mjs check-containers.sh; do
  cp "$source_dir/$file" "$build_dir/"
done
cp "$source_dir/main.go.in" "$build_dir/main.go"
cd "$build_dir"
cp style.css react/style.css
cp style.css svelte/style.css
go version
node --version
npm --version
npm ci
mkdir -p htmxdist/assets baselinedist
cp htmx/index.html htmxdist/index.html
cp htmx/client.js style.css node_modules/htmx.org/dist/htmx.min.js htmxdist/assets/
printf '<!doctype html>\n' >baselinedist/index.html
node_modules/.bin/vite build react --outDir ../reactdist --emptyOutDir
node_modules/.bin/vite build svelte --outDir ../sveltedist --emptyOutDir
node measure.mjs
for stack in baseline htmx svelte react; do
  rm -rf assets
  mkdir assets
  cp -R "${stack}dist/." assets/
  CGO_ENABLED=0 go build -trimpath -o "probe-$stack" main.go
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "probe-$stack-arm64" main.go
  printf '%s ELF: ' "$stack"
  wc -c <"probe-$stack-arm64"
done
node browser.mjs

if [ "${RUN_CONTAINERS:-0}" = "1" ]; then
  sh check-containers.sh
fi
