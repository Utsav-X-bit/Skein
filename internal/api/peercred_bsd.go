//go:build darwin || freebsd

package api

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// peerCredential reads LOCAL_PEERCRED on a BSD Unix connection.
func peerCredential(c net.Conn) (int, error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix connection")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return -1, err
	}
	var (
		cred *unix.Xucred
		serr error
	)
	err = raw.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil {
		return -1, err
	}
	if serr != nil {
		return -1, serr
	}
	return int(cred.Uid), nil
}

// peerCredFull is unsupported on BSD: LOCAL_PEERCRED (Xucred) carries a
// uid but no peer pid, so executable-image verification cannot be done
// from the connection. VerifyUnixServer falls back to the socket-file
// ownership check on these platforms.
func peerCredFull(net.Conn) (int, int, error) {
	return -1, -1, errors.New("peer pid is unavailable on this Unix")
}
