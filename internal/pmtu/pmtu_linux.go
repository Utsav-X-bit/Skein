//go:build linux

package pmtu

import (
	"net"

	"golang.org/x/sys/unix"
)

// setDontFragment asks the kernel to report an oversized datagram as an error
// (and to honour ICMP "fragmentation needed") instead of splitting it.
func setDontFragment(conn *net.UDPConn, v6 bool) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := raw.Control(func(fd uintptr) {
		if v6 {
			setErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_MTU_DISCOVER, unix.IPV6_PMTUDISC_DO)
			return
		}
		setErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
	}); err != nil {
		return err
	}
	return setErr
}
