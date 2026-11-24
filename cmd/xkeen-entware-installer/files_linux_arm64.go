package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func owned(info os.FileInfo, uid uint32, kind, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uid && info.Mode().Type() == kind && (mode == 0 || info.Mode().Perm() == mode) && info.Mode().Perm()&0022 == 0
}

func checkedFile(path string, uid uint32, mode os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !owned(info, uid, 0, mode) || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		file.Close()
		return nil, errSafety
	}
	return file, nil
}

func ensureRootDirs(paths ...string) error {
	for _, path := range paths {
		dir := "/"
		for _, name := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			dir = filepath.Join(dir, name)
			err := os.Mkdir(dir, 0755)
			if err != nil && !errors.Is(err, os.ErrExist) {
				return errSafety
			}
			info, statErr := os.Lstat(dir)
			if statErr != nil || !owned(info, 0, os.ModeDir, 0) {
				return errSafety
			}
			if err == nil {
				if os.Chmod(dir, 0755) != nil || syncDir(filepath.Dir(dir)) != nil {
					return errSafety
				}
			}
		}
	}
	return nil
}

func lockService() (*os.File, error) {
	if err := ensureRootDirs(control); err != nil {
		return nil, err
	}
	path := control + "/agent-transport.lock"
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, errSafety
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !owned(info, 0, 0, 0600) || info.Sys().(*syscall.Stat_t).Nlink != 1 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		file.Close()
		return nil, errors.New("agent service is busy or its lock is unavailable")
	}
	return file, nil
}

func writeFile(path string, raw []byte, mode os.FileMode, uid, gid int) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".xkeen-install-")
	if err != nil {
		return errSafety
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chown(uid, gid); err != nil {
		return errSafety
	}
	if err := file.Chmod(mode); err != nil {
		return errSafety
	}
	if _, err := file.Write(raw); err != nil || file.Sync() != nil || file.Close() != nil {
		return errSafety
	}
	if err := os.Rename(file.Name(), path); err != nil || syncDir(dir) != nil {
		return errSafety
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func removePublished(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errSafety
	}
	return syncDir(filepath.Dir(path))
}
