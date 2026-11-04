//go:build linux

package api

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// peerCredential reads SO_PEERCRED on a Linux Unix connection.
func peerCredential(c net.Conn) (int, error) {
	uid, _, err := peerCredFull(c)
	return uid, err
}

// peerCredFull reads SO_PEERCRED on a Linux Unix connection, returning
// the peer's uid and pid. The pid is what server-identity verification
// (VerifyUnixServer) uses to check the daemon's executable image.
func peerCredFull(c net.Conn) (uid, pid int, err error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return -1, -1, errors.New("not a unix connection")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return -1, -1, err
	}
	var (
		cred *unix.Ucred
		serr error
	)
	err = raw.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return -1, -1, err
	}
	if serr != nil {
		return -1, -1, serr
	}
	return int(cred.Uid), int(cred.Pid), nil
}
