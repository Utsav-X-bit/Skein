//go:build !windows && !android

package services

import (
	"context"
	"net"

	"github.com/Utsav-X-bit/skein/internal/api"
)

func dialControl(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", controlSocketPath())
	if err != nil {
		return nil, err
	}
	// Step 2: verify the daemon's server identity before any request
	// goes over this connection. A failed verification closes the
	// connection and fails the dial; there is no unverified fallback.
	if _, _, err := api.VerifyUnixServer(conn, expectedDaemonImage()); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
