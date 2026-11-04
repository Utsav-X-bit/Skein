//go:build unix

// Server-identity verification for Unix control clients (Step 2,
// ADR-001 §5; Agent E F-03 Unix counterpart to VerifyPipeServer).
//
// The threat is a squatter: a process that owns the control socket
// path before the real daemon does, and then receives secret-bearing
// requests (e.g. POST /api/v1/invites/network) from a GUI/CLI that
// assumed it was talking to the daemon. Socket-file permissions alone
// do not stop a same-user squatter in a dev directory, and the daemon
// side already defends its own name (stale-socket verification,
// ErrSocketBusy); this is the client side of the same check.
//
// VerifyUnixServer proves the server behind a connected Unix socket
// is a Skein daemon binary, using the same trust root as the Windows
// check: the server process's executable image, kernel-derived, never
// anything the server sends. On Linux it reads SO_PEERCRED (pid + uid)
// and /proc/<pid>/exe. The uid must be root (service daemon) or the
// caller's own uid (dev daemon run by the same user); a daemon owned
// by any other user is rejected. On Unix platforms without a peer pid
// (BSD, others), it falls back to the socket-file ownership check:
// the socket file must be a socket owned by root or the caller and
// not world-writable. A failed verification must make the caller
// refuse the connection — never fall back to an unverified one.
package api

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// daemonImageNames are the only executable basenames accepted when no
// explicit expected image is configured. A test binary, a shell, or a
// renamed squatter binary fails this check.
var daemonImageNames = map[string]bool{"skein": true, "skeind": true}

// VerifyUnixServer verifies the daemon behind conn. expectedImage, when
// non-empty, is a path or path prefix the server image must start with
// (mirroring VerifyPipeServer: a full install path or a package
// directory both work, because a full path is its own prefix). When
// empty, the server image's basename must be skein or skeind.
// It returns the server pid and uid. On platforms without a peer pid
// it returns pid -1 and the socket file's owner uid instead.
func VerifyUnixServer(conn net.Conn, expectedImage string) (pid, uid int, err error) {
	if _, ok := conn.(*net.UnixConn); !ok {
		return -1, -1, errors.New("unix server verification: not a unix connection")
	}
	uid, pid, credErr := peerCredFull(conn)
	if credErr != nil {
		// No peer pid on this platform: prove identity from the
		// socket file itself, and fail closed if even that is off.
		return verifyUnixSocketFile(conn)
	}
	if pid <= 0 {
		return -1, -1, errors.New("unix server verification: peer reports no process")
	}
	if uid < 0 {
		return -1, -1, errors.New("unix server verification: peer reports no uid")
	}
	if uid != 0 && uid != os.Getuid() {
		return -1, -1, fmt.Errorf("unix server verification: server uid %d is neither root nor the caller", uid)
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return -1, -1, fmt.Errorf("unix server verification: server image: %w", err)
	}
	if expectedImage != "" {
		if exe != expectedImage && !strings.HasPrefix(exe, expectedImage) {
			return -1, -1, fmt.Errorf("unix server verification: server image %q is not under %q", exe, expectedImage)
		}
	} else if !daemonImageNames[filepath.Base(exe)] {
		return -1, -1, fmt.Errorf("unix server verification: server image %q is not a skein daemon binary", exe)
	}
	return pid, uid, nil
}

// verifyUnixSocketFile is the fallback for platforms without SO_PEERCRED
// pid support. The socket path comes from the connection's remote
// address (the path the client dialed); it must be a socket owned by
// root or the caller and not world-writable.
func verifyUnixSocketFile(conn net.Conn) (pid, uid int, err error) {
	path := conn.RemoteAddr().String()
	fi, err := os.Lstat(path)
	if err != nil {
		return -1, -1, fmt.Errorf("unix server verification: socket file: %w", err)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return -1, -1, fmt.Errorf("unix server verification: %s is not a socket", path)
	}
	if fi.Mode()&0o002 != 0 {
		return -1, -1, fmt.Errorf("unix server verification: socket %s is world-writable", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, -1, errors.New("unix server verification: socket owner unavailable")
	}
	owner := int(st.Uid)
	if owner != 0 && owner != os.Getuid() {
		return -1, -1, fmt.Errorf("unix server verification: socket owned by uid %d, neither root nor the caller", owner)
	}
	return -1, owner, nil
}
