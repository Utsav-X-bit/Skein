//go:build android

package services

import (
	"context"
	"net"

	"github.com/Utsav-X-bit/skein/internal/inproc"
)

// dialControl dials the in-process daemon: on Android the GUI and the
// daemon are the same process, so there is no Unix socket to dial and
// no daemon image to verify (see dial_unix.go). This does NOT weaken
// authentication: the in-process transport stamps the app's own UID
// as the kernel caller and the daemon grants that UID the secret
// role, so the API's wrapRole gate still enforces the per-route role
// check. No other process can dial this transport — it is not backed
// by a socket, pipe, or TCP port — so there is no spoofing surface
// for a fake daemon to sit on.
func dialControl(ctx context.Context) (net.Conn, error) {
	return inproc.Dial(ctx)
}
