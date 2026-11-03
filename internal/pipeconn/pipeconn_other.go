//go:build !windows

package pipeconn

import (
	"net"
	"runtime"
)

// Dial is unavailable outside Windows, where the control plane is a Unix
// socket.
func Dial(string) (net.Conn, error) {
	return nil, net.UnknownNetworkError(runtime.GOOS + " named pipes")
}
