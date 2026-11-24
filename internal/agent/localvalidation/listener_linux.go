//go:build linux

package localvalidation

import (
	"net/netip"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/linuxlistener"
)

func ownsListener(pid int, address string) bool {
	endpoint, err := netip.ParseAddrPort(address)
	return err == nil && endpoint.Addr().IsLoopback() && linuxlistener.Owns(pid, endpoint)
}
