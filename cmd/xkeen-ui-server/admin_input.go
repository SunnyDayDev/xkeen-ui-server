package main

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/webadmin"
	"golang.org/x/sys/unix"
)

var errPasswordFile = errors.New("password file must be a regular, singly linked file owned by the operator with mode 0600")

func readAdminPasswordFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errPasswordFile
	}
	file := os.NewFile(uintptr(fd), "administrator password file")
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode != unix.S_IFREG|0600 ||
		int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 {
		return nil, errPasswordFile
	}
	password, err := io.ReadAll(io.LimitReader(file, webadmin.MaxPasswordBytes+3))
	if err != nil {
		clear(password)
		return nil, errPasswordFile
	}
	if len(password) > webadmin.MaxPasswordBytes+2 {
		clear(password)
		return nil, webadmin.ErrInvalidPassword
	}
	if bytes.HasSuffix(password, []byte("\n")) {
		password = bytes.TrimSuffix(bytes.TrimSuffix(password, []byte("\n")), []byte("\r"))
	}
	return password, nil
}
