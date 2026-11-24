//go:build linux

package localvalidation

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/agent/preparation"
	"golang.org/x/sys/unix"
)

func newTransport(path string) (*http.Transport, error) {
	if !secureSocket(path) {
		return nil, preparation.ErrUnavailable
	}
	return &http.Transport{DisableCompression: true, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if !secureSocket(path) {
			return nil, preparation.ErrUnavailable
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
		if err != nil {
			return nil, preparation.ErrUnavailable
		}
		if !peerIs(conn, 0) {
			conn.Close()
			return nil, preparation.ErrUnavailable
		}
		return conn, nil
	}}, nil
}

func secureSocket(path string) bool {
	if filepath.Clean(path) != path || !privateDirectory(filepath.Dir(path)) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0007 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}

type peerListener struct {
	*net.UnixListener
	uid uint32
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if peerIs(c, l.uid) {
			return c, nil
		}
		c.Close()
	}
}

func Listen(path string, agentUID, agentGID uint32) (net.Listener, error) {
	if os.Geteuid() != 0 || filepath.Clean(path) != path || !privateDirectory(filepath.Dir(path)) {
		return nil, preparation.ErrUnavailable
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, preparation.ErrUnavailable
	}
	if os.Chmod(path, 0660) != nil || os.Chown(path, 0, int(agentGID)) != nil {
		listener.Close()
		return nil, preparation.ErrUnavailable
	}
	return &peerListener{listener, agentUID}, nil
}

func peerIs(conn net.Conn, uid uint32) bool {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	var peer *unix.Ucred
	var lookup error
	err = raw.Control(func(fd uintptr) { peer, lookup = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	return err == nil && lookup == nil && peer != nil && peer.Uid == uid
}
