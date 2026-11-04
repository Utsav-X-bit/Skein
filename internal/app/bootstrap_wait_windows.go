//go:build windows

package app

import (
	"errors"
	"time"

	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

// waitBootstrapPlane waits for the bootstrap daemon's control pipe to
// accept a connection, so a freshly minted token is posted to a
// listening daemon instead of racing its bind. Each probe dials and
// immediately closes; the daemon's accept loop treats that as a
// harmless empty connection.
func waitBootstrapPlane(deadline time.Time) error {
	for {
		if conn, err := pipeconn.Dial(ControlPipeName); err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("control pipe did not accept a connection")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
