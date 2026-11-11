// Package pmtu opens short-lived UDP sockets that forbid IP fragmentation, so
// a path-MTU probe can tell "this datagram arrived" apart from "the kernel
// silently split it".
package pmtu

import (
	"errors"
	"net"
	"net/netip"
)

// ErrUnsupported reports that this platform cannot mark datagrams
// don't-fragment. Callers must then keep their conservative MTU.
var ErrUnsupported = errors.New("pmtu: don't-fragment is unsupported on this platform")

// Open binds an ephemeral UDP socket of addr's address family with the
// don't-fragment bit set. The caller closes it.
func Open(addr netip.AddrPort) (*net.UDPConn, error) {
	if !addr.IsValid() || addr.Port() == 0 {
		return nil, errors.New("pmtu: need a concrete destination address and port")
	}
	network := "udp4"
	if addr.Addr().Is6() {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, err
	}
	if err := setDontFragment(conn, addr.Addr().Is6()); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
