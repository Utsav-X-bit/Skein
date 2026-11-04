//go:build unix && !linux && !darwin && !freebsd

package api

import (
	"errors"
	"net"
)

func peerCredential(net.Conn) (int, error) {
	return -1, errors.New("peer credentials are unsupported on this Unix")
}

// peerCredFull is unsupported on this Unix; see peercred_bsd.go.
func peerCredFull(net.Conn) (int, int, error) {
	return -1, -1, errors.New("peer credentials are unsupported on this Unix")
}
