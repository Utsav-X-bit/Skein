//go:build !windows

package app

import (
	"errors"
	"os"
	"time"
)

// waitBootstrapPlane waits for the bootstrap daemon's control socket to
// appear, so a freshly minted token is posted to a listening daemon
// instead of racing its bind. A stale socket from an unclean shutdown
// would false-positive here; the daemon unlinks its socket on graceful
// shutdown, and the bootstrap flow always starts from a stopped service.
func waitBootstrapPlane(deadline time.Time) error {
	for {
		if _, err := os.Stat(ControlSocket); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("control socket did not appear")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
