//go:build windows

package services

import (
	"context"
	"errors"
	"net"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

func dialControl(context.Context) (net.Conn, error) {
	conn, err := pipeconn.Dial(controlPipeName())
	if err != nil {
		return nil, err
	}
	// Step 2: verify the daemon's server identity before any request
	// goes over this connection. A failed verification closes the
	// connection and fails the dial; there is no unverified fallback.
	h, ok := pipeconn.ServerHandle(conn)
	if !ok {
		conn.Close()
		return nil, errors.New("pipe connection has no server handle to verify")
	}
	if _, err := api.VerifyPipeServer(h, expectedDaemonImage()); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
