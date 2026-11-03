//go:build unix

// The Unix control socket: a 0660 root:skein file in /run/skein with a
// per-user ACL, and kernel-derived caller identity (SO_PEERCRED).
package api

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ErrSocketBusy reports that a live daemon already listens on the socket.
var ErrSocketBusy = errors.New("control socket is busy")

// NewUnixListener creates the control socket at path. The socket is created
// 0660 root:group, and the per-user ACL recorded in accessFile (one uid per
// line, written by the helper's grant-user) is reapplied so previously
// granted users keep access across restarts. A stale socket left by a dead
// daemon is verified — a live listener answers connect and yields
// ErrSocketBusy — and then unlinked.
//
// Every accepted connection's context is stamped with the peer's real uid
// (SO_PEERCRED), so the API authenticates the caller from the kernel instead
// of a bearer token.
func NewUnixListener(path, group, accessFile string) (net.Listener, error) {
	uids, err := readGrantedUIDs(accessFile)
	if err != nil {
		return nil, err
	}
	return listenUnix(path, group, uids)
}

// readGrantedUIDs parses the access file: one uid per line, # comments and
// blank lines ignored. An empty name means no file; a missing file means no
// grants.
func readGrantedUIDs(path string) ([]int, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var uids []int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		uid, err := strconv.Atoi(line)
		if err != nil || uid < 0 {
			return nil, fmt.Errorf("access file %s: bad uid %q", path, line)
		}
		uids = append(uids, uid)
	}
	return uids, nil
}

func listenUnix(path, group string, uids []int) (net.Listener, error) {
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		// A live listener answers connect; a dead one does not.
		if live, cerr := net.Dial("unix", path); cerr == nil {
			live.Close()
			return nil, ErrSocketBusy
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		ln.Close()
		_ = os.Remove(path)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		cleanup()
		return nil, err
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err == nil {
			gid, gerr := strconv.Atoi(g.Gid)
			if gerr == nil {
				if err := os.Chown(path, 0, gid); err != nil {
					cleanup()
					return nil, err
				}
			}
		}
	}
	if len(uids) > 0 {
		if err := applyUnixACL(path, uids); err != nil {
			cleanup()
			return nil, err
		}
	}
	return &peerCredListener{l: ln}, nil
}

// applyUnixACL writes a system.posix_acl_access blob granting rw to each uid
// on top of the socket's 0660 root:group permissions.
func applyUnixACL(path string, uids []int) error {
	const (
		tagUserObj  = 0x01
		tagUser     = 0x02
		tagGroupObj = 0x04
		tagMask     = 0x10
		tagOther    = 0x20
		rw          = 0o6
	)
	var b []byte
	b = append(b, 0, 0, 0, 2) // version 2, big-endian
	add := func(tag, perm uint16, id int) {
		b = binary.LittleEndian.AppendUint16(b, tag)
		b = binary.LittleEndian.AppendUint16(b, perm)
		b = binary.LittleEndian.AppendUint32(b, uint32(id))
	}
	add(tagUserObj, rw, 0)
	for _, u := range uids {
		if u != 0 { // root is already covered by user::
			add(tagUser, rw, u)
		}
	}
	add(tagGroupObj, rw, 0)
	add(tagMask, rw, 0)
	add(tagOther, 0, 0)
	return unix.Setxattr(path, "system.posix_acl_access", b, 0)
}

// peerCredListener stamps each accepted connection with its peer's uid.
type peerCredListener struct{ l net.Listener }

func (p *peerCredListener) Accept() (net.Conn, error) {
	c, err := p.l.Accept()
	if err != nil {
		return nil, err
	}
	return &peerCredConn{c: c}, nil
}
func (p *peerCredListener) Close() error   { return p.l.Close() }
func (p *peerCredListener) Addr() net.Addr { return p.l.Addr() }

// peerCredConn exposes the peer's uid through ConnContext, which net/http
// merges into every request context on the connection.
type peerCredConn struct{ c net.Conn }

func (c *peerCredConn) Read(b []byte) (int, error)         { return c.c.Read(b) }
func (c *peerCredConn) Write(b []byte) (int, error)        { return c.c.Write(b) }
func (c *peerCredConn) Close() error                       { return c.c.Close() }
func (c *peerCredConn) LocalAddr() net.Addr                { return c.c.LocalAddr() }
func (c *peerCredConn) RemoteAddr() net.Addr               { return c.c.RemoteAddr() }
func (c *peerCredConn) SetDeadline(t time.Time) error      { return c.c.SetDeadline(t) }
func (c *peerCredConn) SetReadDeadline(t time.Time) error  { return c.c.SetReadDeadline(t) }
func (c *peerCredConn) SetWriteDeadline(t time.Time) error { return c.c.SetWriteDeadline(t) }

// ConnContext returns the connection context with the peer's real uid
// attached, observed through SO_PEERCRED.
func (c *peerCredConn) ConnContext() context.Context {
	base := context.Background()
	if u, ok := c.c.(interface{ ConnContext() context.Context }); ok {
		base = u.ConnContext()
	}
	uid := -1
	if ucred, err := peerCredential(c.c); err == nil {
		uid = int(ucred.Uid)
	}
	if uid >= 0 {
		return WithCaller(base, Caller{UID: uid})
	}
	return base
}

// peerCredential reads SO_PEERCRED on a Unix connection.
func peerCredential(c net.Conn) (*unix.Ucred, error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return nil, errors.New("not a unix connection")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return nil, err
	}
	var (
		cred *unix.Ucred
		serr error
	)
	err = raw.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return nil, err
	}
	if serr != nil {
		return nil, serr
	}
	return cred, nil
}
