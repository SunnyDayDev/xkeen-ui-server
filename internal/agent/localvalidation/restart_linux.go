//go:build linux

package localvalidation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"golang.org/x/sys/unix"
)

// RestartWorking manages a dedicated direct Xray service. The owning supervisor
// holds the persistent scope lock for all supported starts and mutations.
type RestartWorking struct {
	hot            *HotConfig
	guardedRuntime preparation.Runtime
	guardedPolicy  string
	scope          preparation.Scope
	initial        Working
	binary, bootID string
	uid, gid       uint32
	directory      os.FileInfo
	lease          *os.File
}

func NewRestartWorking(cfg Config, uid, gid uint32) (*RestartWorking, error) {
	noNewPrivileges, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || noNewPrivileges != 1 {
		return nil, restart.ErrBaseline
	}
	if uid == 0 || gid == 0 || uid == cfg.AgentUID || uid == cfg.WorkerUID ||
		gid == cfg.AgentGID || gid == cfg.WorkerGID || !privateDirectory(cfg.WorkingDirectory) {
		return nil, restart.ErrBaseline
	}
	fd, err := unix.Open(cfg.WorkingDirectory, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, restart.ErrBaseline
	}
	lease := os.NewFile(uintptr(fd), "working directory lease")
	dir, err := lease.Stat()
	if err != nil || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		lease.Close()
		return nil, restart.ErrBaseline
	}
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || len(strings.TrimSpace(string(raw))) != 36 {
		lease.Close()
		return nil, restart.ErrBaseline
	}
	return &RestartWorking{hot: cfg.Hot, scope: cfg.Scope, initial: cfg.Observer(), binary: cfg.Xray,
		bootID: strings.TrimSpace(string(raw)), uid: uid, gid: gid, directory: dir, lease: lease}, nil
}

func (w *RestartWorking) Close() error { return w.lease.Close() }

func (w *RestartWorking) Observe(ctx context.Context, current *restart.Confirmed) (preparation.Baseline, error) {
	probe := w.initial
	if current != nil {
		rt := current.Baseline.Runtime
		if rt.BootID != w.bootID || !w.matchesReceiptDirectory(rt) {
			return preparation.Baseline{}, restart.ErrBaseline
		}
		probe.PID, probe.StartTick = rt.PID, rt.StartTick
		expected, err := workingManifest(current.Baseline.Files)
		if err != nil {
			return preparation.Baseline{}, restart.ErrBaseline
		}
		probe.Manifest = expected
	}
	observed, err := w.observe(ctx, probe)
	if err == nil && current != nil && current.Hot != nil && !current.Hot.Matches(w.RuleEvidence(ctx, current)) {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	return observed, err
}

func (w *RestartWorking) observe(ctx context.Context, probe Working) (preparation.Baseline, error) {
	if !w.sameDirectory() || !ownsListener(probe.PID, probe.SOCKS) {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	before, err := probe.Observe(ctx)
	if err != nil || !w.sameDirectory() || !ownsListener(probe.PID, probe.SOCKS) {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	before.Runtime = w.fixedRuntime(before.Runtime.PID, before.Runtime.StartTick)
	return before, nil
}

func (w *RestartWorking) Preflight(_ context.Context, previous preparation.Baseline, desired restart.Desired) ([]preparation.BaselineFile, error) {
	fd, err := w.openDirectory()
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	if fd.Sync() != nil {
		return nil, restart.ErrBaseline
	}
	binary, err := w.workingBinary()
	if err != nil {
		return nil, err
	}
	binary.Close()
	pidfd, err := unix.PidfdOpen(previous.Runtime.PID, 0)
	if err != nil {
		return nil, restart.ErrBaseline
	}
	unix.Close(pidfd)
	return restart.PlanWorkingGeneration(w.scope, previous, desired.Snapshot, w.gid)
}

func (w *RestartWorking) Install(ctx context.Context, intent restart.Intent, checkpoint func(restart.Phase, *preparation.Runtime) error) (preparation.Baseline, error) {
	previous := intent.Previous
	expected := w.initial
	expected.PID, expected.StartTick = previous.Runtime.PID, previous.Runtime.StartTick
	expected.Manifest, _ = workingManifest(previous.Files)
	prior, err := w.observe(ctx, expected)
	if err != nil || !reflect.DeepEqual(prior, previous) || ctx.Err() != nil {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	if err := stopExact(ctx, expected); err != nil {
		return preparation.Baseline{}, err
	}
	if err := checkpoint(restart.Stopped, nil); err != nil {
		return preparation.Baseline{}, err
	}
	if !w.exactFiles(previous.Files) || ctx.Err() != nil {
		return preparation.Baseline{}, restart.ErrBaseline
	}
	if err := w.installFiles(ctx, previous.Files, intent.TargetFiles); err != nil {
		return preparation.Baseline{}, err
	}
	if !w.exactFiles(intent.TargetFiles) {
		return preparation.Baseline{}, restart.ErrEffects
	}
	if err := checkpoint(restart.FilesVerified, nil); err != nil {
		return preparation.Baseline{}, err
	}
	return w.launch(ctx, intent.TargetFiles, func(runtime preparation.Runtime) error {
		return checkpoint(restart.RuntimeStarted, &runtime)
	})
}

// StartCommitted authorizes the ordinary trusted startup path only with a
// complete committed generation and no unresolved intent.
func (w *RestartWorking) StartCommitted(ctx context.Context, journal restart.Journal) error {
	record, err := journal.Load()
	switch {
	case err != nil:
		return err
	case record.Pending != nil:
		if record.Pending.Rollback != nil && record.Pending.Rollback.State == restart.RollbackFailed {
			return restart.ErrRollbackFailed
		}
		return restart.ErrPending
	}
	current := record.Working()
	if current == nil {
		_, err := w.Observe(ctx, nil)
		return err
	}
	if observed, err := w.Observe(ctx, current); err == nil && reflect.DeepEqual(observed, current.Baseline) {
		return nil
	}
	if !w.matchesReceiptDirectory(current.Baseline.Runtime) || !w.exactFiles(current.Baseline.Files) || w.scopeHasLiveProcess() {
		return restart.ErrBaseline
	}
	runtime := current.Baseline.Runtime
	if runtime.BootID == w.bootID {
		if tick, err := ProcessStartTick(runtime.PID); err == nil && tick == runtime.StartTick {
			return restart.ErrBaseline
		}
	}
	observed, err := w.launch(ctx, current.Baseline.Files, func(preparation.Runtime) error { return nil })
	if err != nil {
		return err
	}
	if record.Current != nil {
		current.Baseline = observed
		current.Guard = w.LaunchGuard(observed.Runtime)
		if current.Hot != nil {
			if current.Guard != current.Hot.Guard || !current.Hot.Matches(w.RuleEvidence(ctx, current)) {
				return restart.ErrBaseline
			}
		}
	} else {
		record.LocalBaseline = &observed
	}
	return journal.Save(record)
}

func (w *RestartWorking) launch(ctx context.Context, files []preparation.BaselineFile, started func(preparation.Runtime) error) (preparation.Baseline, error) {
	if ctx.Err() != nil || !w.exactFiles(files) {
		return preparation.Baseline{}, restart.ErrEffects
	}
	binary, err := w.workingBinary()
	if err != nil {
		return preparation.Baseline{}, err
	}
	defer binary.Close()
	command := exec.Command("/proc/self/fd/3", "run", "-confdir", w.initial.Directory)
	command.ExtraFiles = []*os.File{binary}
	command.Env = []string{"PATH=/usr/bin:/bin", "XRAY_LOCATION_ASSET=/opt/etc/xray/dat"}
	command.Dir = "/"
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: w.uid, Gid: w.gid}}
	guard := w.policy(ctx)
	if err := command.Start(); err != nil {
		return preparation.Baseline{}, restart.ErrEffects
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	abort := func() {
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
	tick, err := ProcessStartTick(command.Process.Pid)
	if err != nil {
		abort()
		return preparation.Baseline{}, restart.ErrEffects
	}
	runtime := w.fixedRuntime(command.Process.Pid, tick)
	w.guardedRuntime, w.guardedPolicy = runtime, guard
	if err := started(runtime); err != nil {
		abort()
		return preparation.Baseline{}, err
	}
	probe := w.initial
	probe.PID, probe.StartTick = runtime.PID, runtime.StartTick
	probe.Manifest, _ = workingManifest(files)
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if confirmed, err := w.observe(deadline, probe); err == nil && reflect.DeepEqual(confirmed.Files, files) {
			return confirmed, nil
		}
		select {
		case <-deadline.Done():
			abort()
			return preparation.Baseline{}, restart.ErrEffects
		case <-done:
			return preparation.Baseline{}, restart.ErrEffects
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (w *RestartWorking) fixedRuntime(pid int, tick uint64) preparation.Runtime {
	rt := preparation.Runtime{PID: pid, StartTick: tick, VerifierID: w.scope.VerifierID, BootID: w.bootID}
	stat := w.directory.Sys().(*syscall.Stat_t)
	rt.DirectoryDevice, rt.DirectoryInode = uint64(stat.Dev), stat.Ino
	return rt
}

func (w *RestartWorking) matchesReceiptDirectory(rt preparation.Runtime) bool {
	stat := w.directory.Sys().(*syscall.Stat_t)
	return rt.DirectoryDevice == uint64(stat.Dev) && rt.DirectoryInode == stat.Ino && w.sameDirectory()
}

func (w *RestartWorking) scopeHasLiveProcess() bool {
	ids, err := w.scopedProcessIDs()
	return err != nil || len(ids) != 0
}

func (w *RestartWorking) scopedProcessIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid < 1 {
			continue
		}
		raw, err := readStaged(procPath(pid, "cmdline"), 4096)
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		claimed := false
		for _, arg := range args[1:] {
			if arg == w.initial.Directory || arg == "-confdir="+w.initial.Directory || arg == "--confdir="+w.initial.Directory {
				claimed = true
				break
			}
		}
		env, err := readStaged(procPath(pid, "environ"), 64<<10)
		if claimed || (err == nil && workingArguments(args[1:], env, w.initial.Directory)) {
			ids = append(ids, pid)
		}
	}
	return ids, nil
}

func (w *RestartWorking) workingBinary() (*os.File, error) {
	if !privateDirectory(filepath.Dir(w.binary)) {
		return nil, restart.ErrBaseline
	}
	binary, err := openRegular(w.binary)
	if err != nil {
		return nil, restart.ErrBaseline
	}
	fail := func() (*os.File, error) {
		binary.Close()
		return nil, restart.ErrBaseline
	}
	const limit = 64 << 20
	var before, after unix.Stat_t
	fd := int(binary.Fd())
	if unix.Fstat(fd, &before) != nil || before.Uid != 0 || before.Nlink != 1 ||
		before.Mode&07022 != 0 || before.Mode&0001 == 0 || before.Size <= 0 || before.Size > limit {
		return fail()
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(binary, limit+1))
	if err != nil || n != before.Size || unix.Fstat(fd, &after) != nil || before.Mtim != after.Mtim ||
		before.Ctim != after.Ctim || !bytes.Equal(digest.Sum(nil), w.initial.BinaryDigest[:]) {
		return fail()
	}
	return binary, nil
}

func stopExact(ctx context.Context, expected Working) error {
	fd, err := unix.PidfdOpen(expected.PID, 0)
	if err != nil {
		return restart.ErrEffects
	}
	defer unix.Close(fd)
	if ctx.Err() != nil || !expected.identifyProcess() || unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0) != nil {
		return restart.ErrEffects
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for deadline.Err() == nil {
		count, err := unix.Poll(poll, 50)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return restart.ErrEffects
		}
		if count > 0 && poll[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			return nil
		}
	}
	return restart.ErrEffects
}
