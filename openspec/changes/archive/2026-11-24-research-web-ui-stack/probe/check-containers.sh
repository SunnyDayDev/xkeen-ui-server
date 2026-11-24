#!/bin/sh
set -eu
cd "$(dirname "$0")"
for image in alpine:3.24.2 xkeen-router-lab:dev; do
  for stack in htmx svelte react; do
    docker run --rm --platform linux/arm64 --network none --read-only \
      --cap-drop ALL --security-opt no-new-privileges \
      --mount "type=bind,src=$PWD/probe-$stack-arm64,dst=/ui-probe,readonly" \
      --entrypoint /bin/sh "$image" -ec '
      if command -v node >/dev/null 2>&1; then exit 1; fi
      /ui-probe -addr 127.0.0.1:8080 &
      pid=$!
      trap "kill $pid 2>/dev/null || true" EXIT
      if [ -x /opt/bin/busybox ]; then busybox=/opt/bin/busybox; else busybox=/bin/busybox; fi
      for attempt in 1 2 3 4 5; do
        if html=$("$busybox" wget -qO- http://127.0.0.1:8080/ 2>/dev/null); then break; fi
        sleep 1
      done
      printf "%s" "$html" | grep -q "UI stack probe"
      js=$(printf "%s\n" "$html" | sed -n '\''s/.*src="\(\/assets\/[^"]*\.js\)".*/\1/p'\'' | head -n 1)
      [ -n "$js" ]
      content=$("$busybox" wget -qO- "http://127.0.0.1:8080$js")
      [ -n "$content" ]
      text=$("$busybox" wget -qO- http://127.0.0.1:8080/status-text)
      fragment=$("$busybox" wget -qO- http://127.0.0.1:8080/status-fragment)
      [ "$text" = Ready ]
      [ "$fragment" = "<span>Ready</span>" ]
      if "$busybox" wget -qO- http://127.0.0.1:8080/missing.js 2>/dev/null; then exit 1; fi
      '
    printf '%s / %s: PASS HTML, JS, status, unknown resource, no Node\n' "$image" "$stack"
  done
done
