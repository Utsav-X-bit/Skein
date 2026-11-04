//go:build !unix

package api

import (
	"errors"
	"net"
)

// VerifyUnixServer is unavailable off Unix: there is no SO_PEERCRED
// peer to verify. It fails closed, so a Unix-socket dial on a
// non-Unix platform can never proceed unverified.
func VerifyUnixServer(net.Conn, string) (int, int, error) {
	return -1, -1, errors.New("unix server verification: unsupported on this platform")
}
