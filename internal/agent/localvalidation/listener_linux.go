//go:build linux

package localvalidation

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// ownsListener attributes the probe socket to the process being attested.
func ownsListener(pid int, address string) bool {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil || endpoint.Port() == 0 || !endpoint.Addr().IsLoopback() {
		return false
	}
	local, localErr := os.Stat("/proc/self/ns/net")
	remote, remoteErr := os.Stat(procPath(pid, "ns/net"))
	if localErr != nil || remoteErr != nil || !os.SameFile(local, remote) {
		return false
	}
	fdDir := procPath(pid, "fd")
	descriptors, err := os.ReadDir(fdDir)
	if err != nil || len(descriptors) > 4096 {
		return false
	}
	sockets := make(map[string]bool)
	for _, entry := range descriptors {
		target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") && strings.HasSuffix(target, "]") {
			sockets[target[8:len(target)-1]] = true
		}
	}
	ip := endpoint.Addr()
	if ip.Is4() {
		raw := ip.As4()
		address := fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(raw[:]), endpoint.Port())
		if hasListener(procPath(pid, "net/tcp"), address, sockets) {
			return true
		}
	}
	raw := ip.As16()
	return hasListener(procPath(pid, "net/tcp6"), fmt.Sprintf("%08X%08X%08X%08X:%04X",
		binary.NativeEndian.Uint32(raw[:4]), binary.NativeEndian.Uint32(raw[4:8]),
		binary.NativeEndian.Uint32(raw[8:12]), binary.NativeEndian.Uint32(raw[12:]), endpoint.Port()), sockets)
}

func hasListener(path, address string, sockets map[string]bool) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 10 && fields[1] == address && fields[3] == "0A" && sockets[fields[9]] {
			return true
		}
	}
	return false
}
