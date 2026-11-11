//go:build windows

package pmtu

import (
	"net"

	"golang.org/x/sys/windows"
)

// Winsock exposes the same option number (14) at both levels, named
// IP_DONTFRAGMENT for IPv4 and IPV6_DONTFRAG for IPv6. x/sys/windows does not
// declare them, so they are named here.
const (
	ipDontFragment   = 14
	ipv6DontFragment = 14
)

func setDontFragment(conn *net.UDPConn, v6 bool) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := raw.Control(func(fd uintptr) {
		if v6 {
			setErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, ipv6DontFragment, 1)
			return
		}
		setErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipDontFragment, 1)
	}); err != nil {
		return err
	}
	return setErr
}
