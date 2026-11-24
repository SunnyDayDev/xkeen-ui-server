#!/bin/sh
# Use only one-off laboratory containers and synthetic application files.
set -eu

build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
chmod 0755 "$build_dir"
export CGO_ENABLED=0 GOOS=linux GOARCH=arm64
go build -trimpath -o "$build_dir/xkeen-agent" ./cmd/xkeen-agent
go build -trimpath -o "$build_dir/xkeen-server" ./cmd/xkeen-ui-server
go build -trimpath -o "$build_dir/xkeen-supervisor" ./cmd/xkeen-local-validator
go test -trimpath -c -o "$build_dir/runner.test" ./internal/agent/localvalidation
go test -trimpath -c -o "$build_dir/process.test" ./cmd/xkeen-ui-server

run_lab() {
  docker run --rm --platform linux/arm64 --cgroupns=private --network=none \
    --memory=512m --memory-swap=512m --pids-limit=128 \
    --cap-drop=ALL --cap-add=SYS_ADMIN --cap-add=SYS_CHROOT --cap-add=SETUID \
    --cap-add=SETGID --cap-add=SETPCAP --cap-add=KILL --cap-add=CHOWN --cap-add=DAC_OVERRIDE \
    --security-opt=no-new-privileges --tmpfs /tmp:rw,nosuid,nodev \
    --mount "type=bind,src=$build_dir,dst=/proof,readonly" \
    "$@" --entrypoint /bin/sh "${XKEEN_LAB_IMAGE:-xkeen-router-lab:dev}" -ec '
test "$(cat /proc/self/cgroup)" = "0::/"
mkdir /tmp/validation-cgroup
mount -t cgroup2 -o nosuid,nodev,noexec none /tmp/validation-cgroup
test "$(cat /tmp/validation-cgroup/pids.max)" = 128
test "$(cat /tmp/validation-cgroup/memory.max)" = 536870912
mkdir /tmp/validation-cgroup/supervisor /tmp/validation-cgroup/jobs
echo $$ >/tmp/validation-cgroup/supervisor/cgroup.procs
echo "+memory +pids +cpu" >/tmp/validation-cgroup/cgroup.subtree_control
echo "+memory +pids +cpu" >/tmp/validation-cgroup/jobs/cgroup.subtree_control
export XKEEN_TEST_CGROUP=/tmp/validation-cgroup/jobs
export XKEEN_TEST_XRAY=/opt/sbin/xray
export XKEEN_TEST_CATALOG=/usr/local/share/router-lab/configs
export XKEEN_TEST_LOCAL_VALIDATOR=1
export XKEEN_TEST_SERVER_BINARY=/proof/xkeen-server
export XKEEN_TEST_AGENT_BINARY=/proof/xkeen-agent
export XKEEN_TEST_PREPARATION_BINARY=/proof/xkeen-supervisor
uname -smr
grep -E "^(Xray|Entware)=" /usr/local/share/router-lab/versions.txt
if [ "${XKEEN_TEST_APPLICATION:-0}" = "1" ]; then
  /proof/runner.test -test.run "^TestListenerMustBelongToAttestedPID$" -test.v
  /proof/process.test -test.run "^TestProcessAgent(AppliesTwoControlledGenerations|RollbackOutcomes|RollbackPreservesForeignWorkingData)$" -test.v -test.timeout 2m
else
  if [ "${XKEEN_TEST_PREPARATION_XKEEN:-0}" = "0" ]; then
    /proof/runner.test -test.v
  fi
  /proof/process.test -test.run "^TestProcessAgentPreparesOnlyVerifiedLocalCandidate$" -test.v
fi
test "$(find /tmp/validation-cgroup/jobs -mindepth 1 -type d | wc -l)" = 0
'
}

if [ "${XKEEN_LAB_APPLICATION:-0}" = "1" ]; then
  run_lab --read-only --cap-add=SYS_PTRACE --env XKEEN_TEST_APPLICATION=1
else
  run_lab --read-only
  run_lab --cap-add=NET_ADMIN --env XKEEN_TEST_PREPARATION_XKEEN=1
fi
