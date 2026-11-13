//go:build windows

package link

import (
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

// soExclusiveAddrUse is winsock's SO_EXCLUSIVEADDRUSE (verified against
// this stack via .NET's SocketOptionName.ExclusiveAddressUse): a socket
// bound with it cannot share its port, not even with a wildcard-vs-
// specific bind, which Windows otherwise allows for UDP.
const soExclusiveAddrUse = -5

// udpListenConfig returns a ListenConfig that forces exclusive UDP
// binds on Windows, so a second bind on a live port fails instead of
// silently sharing it (and stealing datagrams).
func udpListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, soExclusiveAddrUse, 1)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
}

// tcpListenConfig and tcpDialer keep the Windows defaults: the exclusive-bind
// rule above is a UDP concern, and nothing routes the daemon's traffic into its
// own TUN on Windows.
func tcpListenConfig() net.ListenConfig { return net.ListenConfig{} }
func tcpDialer() net.Dialer            { return net.Dialer{} }

// UnderlayControl is nil on Windows: nothing routes the daemon's own traffic
// into its TUN, so a nil Control leaves net.ListenConfig and net.Dialer exactly
// as they were.
func UnderlayControl() func(network, address string, c syscall.RawConn) error { return nil }
