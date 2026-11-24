//go:build linux

package localvalidation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"golang.org/x/sys/unix"
)

const ownerFile = ".supervisor-owner"
const socketFile = ".supervisor-socket"
const jobFile = ".supervisor-job"

type Lease struct {
	file  *os.File
	stage string
}

// Acquire reserves one supervisor's protected stage and resolves only its jobs.
func (r Runner) Acquire(ctx context.Context) (*Lease, error) {
	if !privateDirectory(r.StageDir) || !privateDirectory(r.CgroupDir) {
		return nil, preparation.ErrUnavailable
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(r.CgroupDir, &fs); err != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, preparation.ErrUnavailable
	}
	lockPath := filepath.Join(r.StageDir, ".supervisor.lock")
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, preparation.ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), "supervisor lock")
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 || stat.Nlink != 1 || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0777 != 0600 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		f.Close()
		return nil, preparation.ErrUnavailable
	}
	if err := r.establishOwnership(); err != nil {
		f.Close()
		return nil, preparation.ErrUnavailable
	}
	if err := r.cleanStale(ctx); err != nil {
		f.Close()
		return nil, preparation.ErrUnavailable
	}
	return &Lease{file: f, stage: r.StageDir}, nil
}

func (l *Lease) Close() error {
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Lease) Listen(path string, uid, gid uint32) (net.Listener, error) {
	if l.file == nil {
		return nil, preparation.ErrUnavailable
	}
	if info, err := os.Lstat(path); err == nil {
		if !secureSocket(path) {
			return nil, preparation.ErrUnavailable
		}
		owned, err := readMarker(filepath.Join(l.stage, socketFile))
		if err != nil || owned != socketIdentity(path, info) {
			return nil, preparation.ErrUnavailable
		}
		c, err := net.DialTimeout("unix", path, 250*time.Millisecond)
		if err == nil {
			c.Close()
			return nil, preparation.ErrUnavailable
		}
		same, statErr := os.Lstat(path)
		if !errors.Is(err, unix.ECONNREFUSED) || statErr != nil || !os.SameFile(info, same) || os.Remove(path) != nil {
			return nil, preparation.ErrUnavailable
		}
	}
	listener, err := Listen(path, uid, gid)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || writeMarker(l.stage, socketFile, socketIdentity(path, info)) != nil {
		listener.Close()
		return nil, preparation.ErrUnavailable
	}
	return listener, nil
}

func socketIdentity(path string, info os.FileInfo) string {
	if info == nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s\n%d:%d\n", path, stat.Dev, stat.Ino)
}

func (r Runner) establishOwnership() error {
	stat, err := os.Stat(r.CgroupDir)
	if err != nil {
		return err
	}
	identity := socketIdentity(r.CgroupDir, stat)
	path := filepath.Join(r.StageDir, ownerFile)
	owned, err := readMarker(path)
	if err == nil {
		if identity == "" || identity != owned {
			return preparation.ErrUnavailable
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.ReadDir(r.StageDir)
	if err != nil || len(stage) != 1 || stage[0].Name() != ".supervisor.lock" {
		return preparation.ErrUnavailable
	}
	groups, err := os.ReadDir(r.CgroupDir)
	if err != nil {
		return err
	}
	for _, entry := range groups {
		if entry.IsDir() {
			return preparation.ErrUnavailable
		}
	}
	return writeMarker(r.StageDir, ownerFile, identity)
}

func writeMarker(dir, name, body string) error {
	if body == "" {
		return preparation.ErrUnavailable
	}
	f, err := os.CreateTemp(dir, ".marker-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.WriteString(body)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func readMarker(path string) (string, error) {
	f, err := openRegular(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > 8192 {
		return "", preparation.ErrUnavailable
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Nlink != 1 {
		return "", preparation.ErrUnavailable
	}
	content, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || int64(len(content)) != info.Size() {
		return "", preparation.ErrUnavailable
	}
	return string(content), nil
}

func (r Runner) cleanStale(ctx context.Context) error {
	stage, err := os.ReadDir(r.StageDir)
	if err != nil {
		return err
	}
	groups, err := os.ReadDir(r.CgroupDir)
	if err != nil {
		return err
	}
	owned := make(map[string]bool, len(stage))
	for _, entry := range stage {
		switch entry.Name() {
		case ".supervisor.lock", ownerFile, socketFile:
			continue
		}
		path := filepath.Join(r.StageDir, entry.Name())
		if !jobName(entry.Name()) || !privateDirectory(path) {
			return preparation.ErrUnavailable
		}
		marker, err := readMarker(filepath.Join(path, jobFile))
		if err != nil || marker != filepath.Join(r.CgroupDir, entry.Name())+"\n" {
			return preparation.ErrUnavailable
		}
		owned[entry.Name()] = true
	}
	for _, entry := range groups {
		if !entry.IsDir() {
			continue
		}
		if !owned[entry.Name()] || !jobName(entry.Name()) {
			return preparation.ErrUnavailable
		}
	}
	for name := range owned {
		group := filepath.Join(r.CgroupDir, name)
		if _, err := os.Lstat(group); err == nil {
			if !privateDirectory(group) || writeControl(group, "cgroup.kill", "1") != nil || !waitEmpty(ctx, group) || os.Remove(group) != nil {
				return preparation.ErrUnavailable
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return preparation.ErrUnavailable
		}
		if err := os.RemoveAll(filepath.Join(r.StageDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func jobName(name string) bool {
	s, ok := strings.CutPrefix(name, "job-")
	if !ok || s == "" || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func waitEmpty(ctx context.Context, group string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !jobEmpty(group) {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
	return true
}
