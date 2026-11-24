#!/bin/sh
set -eu

# Run the fixture on private loopback. No host firewall, mounts or published ports.
lab_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
docker run --rm -i --platform linux/arm64 --network none --read-only --tmpfs /tmp \
    --cap-drop ALL --cap-add NET_ADMIN --cap-add NET_RAW --security-opt no-new-privileges \
    --entrypoint /bin/sh xkeen-router-lab:dev < "$lab_dir/probe-exclusion-traffic.sh"
