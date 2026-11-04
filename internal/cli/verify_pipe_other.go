//go:build !windows

package cli

import "net"

// verifyClientPipe is a no-op off Windows: the pipe transport is
// Windows-only, and pipeconn.Dial fails before this is ever reached
// on other platforms.
func verifyClientPipe(net.Conn) error { return nil }
