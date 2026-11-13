//go:build !windows && !android

package link

import (
	"net"
	"syscall"
)

// udpListenConfig is the plain config elsewhere: Unix binds are
// exclusive by default.
func udpListenConfig() net.ListenConfig { return net.ListenConfig{} }

// tcpListenConfig and tcpDialer are plain elsewhere: nothing routes the
// daemon's own traffic into its TUN.
func tcpListenConfig() net.ListenConfig { return net.ListenConfig{} }
func tcpDialer() net.Dialer            { return net.Dialer{} }

// UnderlayControl is nil outside Android: no platform here routes the daemon's
// own underlay traffic into its TUN, so a nil Control leaves net.ListenConfig
// and net.Dialer exactly as they were.
func UnderlayControl() func(network, address string, c syscall.RawConn) error { return nil }
