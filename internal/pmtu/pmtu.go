// Package pmtu opens short-lived UDP sockets that forbid IP fragmentation, so
// a path-MTU probe can tell "this datagram arrived" apart from "the kernel
// silently split it".
package pmtu

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/Utsav-X-bit/skein/internal/link"
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
	network, wildcard := "udp4", "0.0.0.0:0"
	if addr.Addr().Is6() {
		network, wildcard = "udp6", "[::]:0"
	}
	// The probe goes to a peer's underlay address, which is exactly the traffic
	// Android's VPN captures, so the socket is created through the shared
	// underlay hook (nil off Android, where it changes nothing).
	cfg := net.ListenConfig{Control: link.UnderlayControl()}
	pc, err := cfg.ListenPacket(context.Background(), network, wildcard)
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, fmt.Errorf("pmtu: %s listener returned %T, want *net.UDPConn", network, pc)
	}
	if err := setDontFragment(conn, addr.Addr().Is6()); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
