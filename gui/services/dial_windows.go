//go:build windows

package services

import (
	"context"
	"net"

	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

func dialControl(context.Context) (net.Conn, error) {
	return pipeconn.Dial(controlPipeName())
}
