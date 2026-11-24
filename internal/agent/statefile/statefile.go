// Package statefile holds a private agent state directory under an exclusive lock.
package statefile

import (
	"crypto/rand"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/enrollment"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/agentcredential"
)

const (
	StateName      = "credentials.json"
	InvitationName = "invitation"
	maxRecord      = 128 * 1024
)

type Store struct {
	dir  *os.File
	lock *os.File
}

type record struct {
	Version int              `json:"version"`
	State   enrollment.State `json:"state"`
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, enrollment.ErrState
	}
	path = filepath.Clean(path)
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, enrollment.ErrState
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, enrollment.ErrState
	}
	store := &Store{dir: os.NewFile(uintptr(fd), "agent state directory")}
	if !privateFile(store.dir, unix.S_IFDIR, 0700) {
		store.Close()
		return nil, enrollment.ErrState
	}
	lock, err := store.openPrivate(".lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		store.Close()
		return nil, enrollment.ErrState
	}
	store.lock = lock
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		store.Close()
		return nil, enrollment.ErrState
	}
	return store, nil
}

func (s *Store) Close() error {
	var err error
	if s.lock != nil {
		err = s.lock.Close()
		s.lock = nil
	}
	if s.dir != nil {
		err = errors.Join(err, s.dir.Close())
		s.dir = nil
	}
	if err != nil {
		return enrollment.ErrState
	}
	return nil
}

func (s *Store) Load() (*enrollment.State, error) {
	raw, err := s.readPrivate(StateName, maxRecord)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, enrollment.ErrState
	}
	var saved record
	if jsonv2.Unmarshal(raw, &saved, jsonv2.RejectUnknownMembers(true)) != nil || saved.Version != 1 {
		return nil, enrollment.ErrState
	}
	return &saved.State, nil
}

func (s *Store) Invitation() (string, error) {
	raw, err := s.readPrivate(InvitationName, agentcredential.Size+2)
	if err != nil {
		return "", enrollment.ErrInvitation
	}
	invitation := string(raw)
	if strings.HasSuffix(invitation, "\n") {
		invitation = strings.TrimSuffix(strings.TrimSuffix(invitation, "\n"), "\r")
	}
	return invitation, nil
}

func (s *Store) Save(state enrollment.State) error {
	raw, err := json.Marshal(record{Version: 1, State: state})
	if err != nil || len(raw) > maxRecord {
		return enrollment.ErrState
	}
	fd := int(s.dir.Fd())
	temp := ".credentials-" + rand.Text()
	file, err := s.openPrivate(temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return enrollment.ErrState
	}
	defer unix.Unlinkat(fd, temp, 0)
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return enrollment.ErrState
	}
	if err := unix.Renameat(fd, temp, fd, StateName); err != nil {
		return enrollment.ErrState
	}
	if err := s.dir.Sync(); err != nil {
		return enrollment.ErrState
	}
	return nil
}

func (s *Store) readPrivate(name string, limit int64) ([]byte, error) {
	file, err := s.openPrivate(name, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, enrollment.ErrState
	}
	return raw, nil
}

func (s *Store) openPrivate(name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(s.dir.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if !privateFile(file, unix.S_IFREG, 0600) {
		file.Close()
		return nil, enrollment.ErrState
	}
	return file, nil
}

func privateFile(file *os.File, kind, permissions uint32) bool {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return false
	}
	mode := uint32(stat.Mode)
	return mode&unix.S_IFMT == kind && mode&07777 == permissions &&
		stat.Uid == uint32(os.Geteuid()) && (kind != unix.S_IFREG || stat.Nlink == 1)
}
