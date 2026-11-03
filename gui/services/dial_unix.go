//go:build !windows

package services

import (
	"context"
	"net"
)

func dialControl(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", controlSocketPath())
}
