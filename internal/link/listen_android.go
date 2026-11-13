//go:build android

package link

import (
	"log"
	"net"
	"syscall"
)

// udpListenConfig protects the underlay socket from the VPN: the
// daemon's UDP to a peer must bypass the TUN, or its packets are
// routed into the TUN and the peer is never reached. The Control
// hook runs while the socket is being created, so the FD is
// protected before the daemon ever sends on it. When no protector
// is registered (the service is not running yet) the socket is left
// unprotected and the dial is retried once the service is up.
func udpListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			if ProtectSocket == nil {
				return nil
			}
			var protectErr error
			if err := c.Control(func(fd uintptr) {
				if !ProtectSocket(int(fd)) {
					log.Printf("skein: android: protect UDP socket fd=%d: no VPN service running; dial may loop into the TUN", fd)
				}
			}); err != nil {
				protectErr = err
			}
			return protectErr
		},
	}
}
