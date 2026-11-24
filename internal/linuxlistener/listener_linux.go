//go:build linux

package linuxlistener

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

// Owns attributes a TCP listener at a fixed address to the identified process.
func Owns(pid int, endpoint netip.AddrPort) bool {
	if !endpoint.IsValid() || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" {
		return false
	}
	local, localErr := os.Stat("/proc/self/ns/net")
	remote, remoteErr := os.Stat(processPath(pid, "ns/net"))
	if localErr != nil || remoteErr != nil || !os.SameFile(local, remote) {
		return false
	}
	fdDir := processPath(pid, "fd")
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
		if hasListener(processPath(pid, "net/tcp"), address, sockets) {
			return true
		}
	}
	if ip.Is4() && ip.IsUnspecified() {
		// A dual-stack socket bound to :: also owns the configured IPv4 wildcard.
		ip = netip.IPv6Unspecified()
	}
	raw := ip.As16()
	return hasListener(processPath(pid, "net/tcp6"), fmt.Sprintf("%08X%08X%08X%08X:%04X",
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

func processPath(pid int, name string) string {
	return filepath.Join("/proc", fmt.Sprint(pid), name)
}
