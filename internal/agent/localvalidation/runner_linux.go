//go:build linux

package localvalidation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentrevision"
	"golang.org/x/sys/unix"
)

const WorkerFlag = "--e02-isolated-worker"
const jobDeadline = 12 * time.Second

type Runner struct {
	StageDir, CgroupDir, Binary string
	Digest                      [32]byte
	UID, GID                    uint32
}

func (r Runner) Run(ctx context.Context, id string, snapshot agentrevision.Snapshot) (outcome error) {
	if ctx.Err() != nil || r.UID == 0 || r.GID == 0 || r.Digest == ([32]byte{}) ||
		!privateDirectory(r.StageDir) || !privateDirectory(r.CgroupDir) {
		return preparation.ErrUnavailable
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(r.CgroupDir, &fs); err != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return preparation.ErrUnavailable
	}
	tag, err := agentrevision.Tag(id, snapshot)
	if err != nil {
		return preparation.ErrUnavailable
	}
	stage, err := os.MkdirTemp(r.StageDir, "job-")
	if err != nil {
		return preparation.ErrUnavailable
	}
	retain := false
	defer func() {
		if !retain && os.RemoveAll(stage) != nil {
			outcome = preparation.ErrUnavailable
		}
	}()
	group := filepath.Join(r.CgroupDir, filepath.Base(stage))
	if err := writeMarker(stage, jobFile, group+"\n"); err != nil {
		return preparation.ErrUnavailable
	}
	if err := os.Mkdir(group, 0700); err != nil {
		return preparation.ErrUnavailable
	}
	defer func() {
		if !jobEmpty(group) || os.Remove(group) != nil {
			retain = true
			outcome = preparation.ErrUnavailable
		}
	}()
	for key, value := range map[string]string{
		"memory.max": "134217728", "memory.swap.max": "0", "memory.oom.group": "1",
		"pids.max": "48", "cpu.max": "50000 100000",
	} {
		if err := writeControl(group, key, value); err != nil {
			return preparation.ErrUnavailable
		}
	}
	control, err := os.OpenFile(filepath.Join(group, "cgroup.kill"), os.O_WRONLY, 0)
	if err != nil {
		return preparation.ErrUnavailable
	}
	defer control.Close()
	jail := filepath.Join(stage, "root")
	if err := r.stage(jail, snapshot); err != nil {
		return preparation.ErrUnavailable
	}
	return r.runIsolated(ctx, group, control, jail, id, snapshot, tag)
}

func privateDirectory(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for first := true; ; first = false {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || (info.Mode().Perm()&0022 != 0 && (first || info.Mode()&os.ModeSticky == 0)) {
			return false
		}
		parent := filepath.Dir(path)
		if parent == path {
			return true
		}
		path = parent
	}
}

func writeControl(group, name, value string) error {
	file, err := os.OpenFile(filepath.Join(group, name), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(value)
	return err
}

func (r Runner) stage(jail string, snapshot agentrevision.Snapshot) error {
	for _, path := range []string{"candidate", "opt/etc/xray/dat", "opt/var/log/xray"} {
		if err := os.MkdirAll(filepath.Join(jail, path), 0755); err != nil {
			return err
		}
	}
	for _, file := range snapshot.Files {
		if err := os.WriteFile(filepath.Join(jail, "candidate", file.Name), file.Content, 0444); err != nil {
			return err
		}
	}
	binary, err := openRegular(r.Binary)
	if err != nil {
		return err
	}
	defer binary.Close()
	out, err := os.OpenFile(filepath.Join(jail, "xray"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0555)
	if err != nil {
		return err
	}
	h := sha256.New()
	const maxBinary = 64 << 20
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(binary, maxBinary+1))
	if err := out.Close(); err != nil || copyErr != nil || n <= 0 || n > maxBinary ||
		hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(r.Digest[:]) {
		return preparation.ErrUnavailable
	}
	return nil
}

func openRegular(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, preparation.ErrUnavailable
	}
	return f, nil
}

func (r Runner) runIsolated(ctx context.Context, group string, kill *os.File, jail, id string, snapshot agentrevision.Snapshot, tag string) error {
	groupFD, err := os.Open(group)
	if err != nil {
		return preparation.ErrUnavailable
	}
	defer groupFD.Close()
	cmd := exec.Command("/proc/self/exe", WorkerFlag, jail, id, strconv.FormatInt(snapshot.Number, 10), snapshot.Profile, tag,
		strconv.FormatUint(uint64(r.UID), 10), strconv.FormatUint(uint64(r.GID), 10), hex.EncodeToString(r.Digest[:]))
	cmd.Env = []string{}
	var diagnostic boundedDiagnostic
	cmd.Stdout, cmd.Stderr = &diagnostic, &diagnostic
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNET | syscall.CLONE_NEWIPC,
		Pdeathsig:  syscall.SIGKILL, UseCgroupFD: true, CgroupFD: int(groupFD.Fd()),
	}
	if err := cmd.Start(); err != nil {
		return preparation.ErrUnavailable
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(jobDeadline)
	defer deadline.Stop()
	var result error
	select {
	case result = <-done:
	case <-deadline.C:
		return stopJob(cmd, kill, done)
	case <-ctx.Done():
		return stopJob(cmd, kill, done)
	}
	if !jobEmpty(group) {
		_, _ = kill.WriteString("1")
		return preparation.ErrUnavailable
	}
	if !cleanEvents(group, "memory.events", "oom", "oom_kill") || !cleanEvents(group, "pids.events", "max") {
		return preparation.ErrUnavailable
	}
	if result == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(result, &exit) && exit.ExitCode() == 23 && diagnostic.configurationRejection() {
		return preparation.ErrRejected
	}
	return preparation.ErrUnavailable
}

func stopJob(cmd *exec.Cmd, kill *os.File, done <-chan error) error {
	if _, err := kill.WriteString("1"); err != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	return preparation.ErrUnavailable
}

func jobEmpty(group string) bool {
	return cleanEvents(group, "cgroup.events", "populated")
}

func cleanEvents(group, name string, keys ...string) bool {
	data, err := os.ReadFile(filepath.Join(group, name))
	if err != nil {
		return false
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			values[f[0]] = f[1]
		}
	}
	for _, key := range keys {
		if values[key] != "0" {
			return false
		}
	}
	return true
}

// Keep diagnostics only in memory. An indeterminate or resource-related
// rejection is unavailable rather than an actionable configuration verdict.
type boundedDiagnostic struct {
	data      bytes.Buffer
	truncated bool
}

func (d *boundedDiagnostic) Write(p []byte) (int, error) {
	const limit = 16 << 10
	room := limit - d.data.Len()
	if room < len(p) {
		d.truncated = true
	}
	_, _ = d.data.Write(p[:min(len(p), room)])
	return len(p), nil
}

func (d *boundedDiagnostic) configurationRejection() bool {
	if d.truncated || d.data.Len() == 0 {
		return false
	}
	s := strings.ToLower(d.data.String())
	for _, unavailable := range []string{
		"no such file or directory", "permission denied", "operation not permitted",
		"no space left on device", "too many open files", "cannot allocate memory",
		"file size limit exceeded", "input/output error", "read-only file system",
	} {
		if strings.Contains(s, unavailable) {
			return false
		}
	}
	return strings.Contains(s, "failed to load config") || strings.Contains(s, "failed to create server")
}
