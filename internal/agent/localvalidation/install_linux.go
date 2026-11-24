//go:build linux

package localvalidation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"reflect"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/restart"
	"golang.org/x/sys/unix"
)

func (w *RestartWorking) sameDirectory() bool {
	actual, err := os.Stat(w.initial.Directory)
	return err == nil && os.SameFile(w.directory, actual) && privateDirectory(w.initial.Directory)
}

func (w *RestartWorking) openDirectory() (*os.File, error) {
	if !w.sameDirectory() {
		return nil, restart.ErrBaseline
	}
	fd, err := unix.Open(w.initial.Directory, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, restart.ErrBaseline
	}
	file := os.NewFile(uintptr(fd), "working configuration directory")
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, w.directory) {
		file.Close()
		return nil, restart.ErrBaseline
	}
	return file, nil
}

func (w *RestartWorking) exactFiles(expected []preparation.BaselineFile) bool {
	actual, _, err := workingFiles(w.initial.Directory)
	return err == nil && reflect.DeepEqual(expected, actual) && w.sameDirectory()
}

func (w *RestartWorking) installFiles(ctx context.Context, previous, next []preparation.BaselineFile) error {
	dir, err := w.openDirectory()
	if err != nil {
		return err
	}
	defer dir.Close()
	fd := int(dir.Fd())
	target := make(map[string]bool, len(next))
	for _, expected := range next {
		if ctx.Err() != nil || !w.sameDirectory() || !putFile(fd, expected) || dir.Sync() != nil {
			return restart.ErrEffects
		}
		target[expected.Name] = true
	}
	for _, old := range previous {
		if target[old.Name] {
			continue
		}
		if ctx.Err() != nil || !w.sameDirectory() || old.Name != "06_extra.json" ||
			unix.Unlinkat(fd, old.Name, 0) != nil || dir.Sync() != nil {
			return restart.ErrEffects
		}
	}
	return nil
}

func putFile(dir int, expected preparation.BaselineFile) bool {
	var id [16]byte
	rand.Read(id[:])
	temp := ".xkeen-app-" + hex.EncodeToString(id[:])
	fd, err := unix.Openat(dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return false
	}
	defer unix.Unlinkat(dir, temp, 0)
	file := os.NewFile(uintptr(fd), "pending working file")
	n, writeErr := file.Write(expected.Content)
	ok := n == len(expected.Content) && writeErr == nil &&
		unix.Fchown(fd, int(expected.UID), int(expected.GID)) == nil && unix.Fchmod(fd, expected.Mode) == nil &&
		file.Sync() == nil
	return file.Close() == nil && ok && unix.Renameat(dir, temp, dir, expected.Name) == nil
}
