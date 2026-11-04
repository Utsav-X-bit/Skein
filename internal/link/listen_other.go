//go:build !windows

package link

import "net"

// udpListenConfig is the plain config elsewhere: Unix binds are
// exclusive by default.
func udpListenConfig() net.ListenConfig { return net.ListenConfig{} }
