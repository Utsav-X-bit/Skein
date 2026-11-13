//go:build android

package link

import (
	"log"
	"net"
	"syscall"
)

// UnderlayControl returns the Control hook that exempts a socket from the
// Android VPN. It is exported for the other sockets the daemon dials from — the
// path-MTU probe and the STUN probe — so every underlay socket is protected the
// same way. The hook checks ProtectSocket when the socket is created, so it is
// still safe to hand out before the VpnService is running.
func UnderlayControl() func(network, address string, c syscall.RawConn) error {
	return underlayControl
}

// underlayControl protects a socket from the VPN. It runs while the socket is
// being created, so the descriptor is protected before it can send: an
// unprotected underlay socket has its packets routed into the TUN whenever the
// destination falls inside a captured route (the peer's LAN address inside the
// overlay prefix, or any default route), and the peer is never reached.
func underlayControl(_, _ string, c syscall.RawConn) error {
	if ProtectSocket == nil {
		return nil
	}
	return c.Control(func(fd uintptr) {
		if !ProtectSocket(int(fd)) {
			log.Printf("skein: android: protect socket fd=%d: no VPN service running; dial may loop into the TUN", fd)
		}
	})
}

// udpListenConfig protects the underlay UDP sockets.
func udpListenConfig() net.ListenConfig {
	return net.ListenConfig{Control: underlayControl}
}

// tcpListenConfig protects the TCP fallback listener. Accepted connections
// inherit the listener's fwmark (SO_MARK) on Linux, so their replies bypass the
// TUN like the listener's own traffic.
func tcpListenConfig() net.ListenConfig {
	return net.ListenConfig{Control: underlayControl}
}

// tcpDialer protects the TCP fallback's outbound connections.
func tcpDialer() net.Dialer {
	return net.Dialer{Control: underlayControl}
}
