#!/bin/sh
# Probe Linux ARM64 features with the pinned Xray and disposable lab files.
set -eu

docker run --rm -i --platform linux/arm64 --cgroupns=private \
  --network=none --read-only --memory=512m --memory-swap=512m --pids-limit=128 \
  --cap-drop=ALL --cap-add=SYS_ADMIN --cap-add=SYS_CHROOT \
  --cap-add=SETUID --cap-add=SETGID --cap-add=KILL \
  --security-opt=no-new-privileges --tmpfs /tmp:rw,nosuid,nodev \
  --entrypoint /bin/sh "${XKEEN_LAB_IMAGE:-xkeen-router-lab:dev}" -es <<'SH'
test "$(cat /proc/self/cgroup)" = "0::/"
mkdir /tmp/cg /tmp/jail
mount -t cgroup2 -o nosuid,nodev,noexec none /tmp/cg
test "$(cat /tmp/cg/pids.max)" = 128
test "$(cat /tmp/cg/memory.max)" = 536870912
mkdir /tmp/cg/supervisor /tmp/cg/job
echo $$ >/tmp/cg/supervisor/cgroup.procs
echo '+memory +pids +cpu' >/tmp/cg/cgroup.subtree_control
echo 134217728 >/tmp/cg/job/memory.max
echo 48 >/tmp/cg/job/pids.max
echo '50000 100000' >/tmp/cg/job/cpu.max
echo 1 >/tmp/cg/job/memory.oom.group
echo 0 >/tmp/cg/job/memory.swap.max

mount -t tmpfs -o size=48m,nosuid,nodev tmpfs /tmp/jail
mkdir -p /tmp/jail/bin /tmp/jail/in /tmp/jail/bad /tmp/jail/scratch /tmp/jail/opt/etc/xray/dat
cp /opt/sbin/xray /tmp/jail/bin/
cp /usr/local/share/router-lab/configs/*.json /tmp/jail/in/
cp /usr/local/share/router-lab/configs/*.json /tmp/jail/bad/
printf '%s\n' '{"outbounds":[{"protocol":"invalid-demo-protocol"}]}' >/tmp/jail/bad/04_outbounds.json
mount -o remount,ro,nosuid,nodev /tmp/jail
cat >/tmp/worker <<'INNER'
set -e
mount --make-rprivate /
mount -t tmpfs -o size=1m,nosuid,nodev,noexec,mode=0777 tmpfs /tmp/jail/scratch
ulimit -n 64
ulimit -t 10
exec /usr/sbin/chroot --userspec=65534:65534 --groups=65534 /tmp/jail /bin/xray run -test -format json -confdir "$1"
INNER
cat >/tmp/spawn <<'INNER'
set -e
echo $$ >/tmp/cg/job/cgroup.procs
exec unshare --mount --pid --fork --net --ipc --kill-child=SIGKILL /bin/sh /tmp/worker "$1"
INNER

run_xray() {
  env -i PATH=/usr/sbin:/usr/bin:/bin XRAY_LOCATION_ASSET=/opt/etc/xray/dat \
    timeout -k 1s 12s /bin/sh /tmp/spawn "$1" >/tmp/verdict 2>&1
}
run_xray /in
grep -q 'Configuration OK' /tmp/verdict
echo 'PASS: exact synthetic catalog accepted'
if run_xray /bad; then
  exit 1
fi
grep -q 'invalid-demo-protocol' /tmp/verdict
echo 'PASS: invalid synthetic catalog rejected'
printf 'memory peak (bytes): '
cat /tmp/cg/job/memory.peak
printf 'pid peak: '
cat /tmp/cg/job/pids.peak

echo 4194304 >/tmp/cg/job/memory.max
cat >/tmp/memory-probe <<'INNER'
set -e
echo $$ >/tmp/cg/job/cgroup.procs
cat /proc/self/cgroup
exec /usr/bin/awk 'BEGIN { for (i=0; i<8192; i++) a[i]=sprintf("%4096s",i); print length(a[8191]) }'
INNER
if timeout -k 1s 5s /bin/sh /tmp/memory-probe >/tmp/verdict 2>&1; then
  cat /tmp/verdict
  exit 1
fi
oom_kills=$(awk '$1 == "oom_kill" { print $2 }' /tmp/cg/job/memory.events)
test "$oom_kills" -gt 0
test "$(cat /tmp/cg/job/pids.current)" = 0
rmdir /tmp/cg/job
echo 'PASS: inner memory limit killed the job and left no processes'
SH
