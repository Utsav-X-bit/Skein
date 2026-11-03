//go:build unix && !linux && !darwin && !freebsd

package api

import (
	"errors"
	"net"
)

func peerCredential(net.Conn) (int, error) {
	return -1, errors.New("peer credentials are unsupported on this Unix")
}
