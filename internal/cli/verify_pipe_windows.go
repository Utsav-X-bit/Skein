//go:build windows

package cli

import (
	"errors"
	"net"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

// verifyClientPipe verifies the daemon behind a pipe connection
// (Step 2). It is called by clientFor before any request is sent;
// a failure fails the dial with no unverified fallback.
func verifyClientPipe(conn net.Conn) error {
	h, ok := pipeconn.ServerHandle(conn)
	if !ok {
		return errors.New("pipe connection has no server handle to verify")
	}
	if _, err := api.VerifyPipeServer(h, expectedDaemonImage()); err != nil {
		return err
	}
	return nil
}
