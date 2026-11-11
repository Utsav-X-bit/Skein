//go:build !linux && !windows

package pmtu

import "net"

// setDontFragment is unsupported here; probes fail closed and the daemon keeps
// its configured MTU.
func setDontFragment(*net.UDPConn, bool) error { return ErrUnsupported }
