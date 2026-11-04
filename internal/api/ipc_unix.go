//go:build unix

// The Unix control socket: a 0660 root:skein file in /run/skein with a
// per-user ACL, and kernel-derived caller identity (SO_PEERCRED).
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"
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

// readGrantedUIDs parses the access file for socket ACL purposes: one
// uid per line, # comments and blank lines ignored. Roles are stripped
// here (the API layer resolves them via ParseAccessGrants); a line is
// "uid" or "uid:role". An empty name means no file; a missing file means
// no grants.
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
	grants, err := ParseAccessGrants(strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("access file %s: %w", path, err)
	}
	var uids []int
	for id := range grants {
		uidStr, ok := strings.CutPrefix(id, "uid:")
		if !ok {
			continue // a SID line cannot appear on a Unix access file
		}
		uid, err := strconv.Atoi(uidStr)
		if err != nil || uid < 0 {
			return nil, fmt.Errorf("access file %s: bad uid %q", path, uidStr)
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

// applyUnixACL grants rw to each persisted uid on the socket. setfacl is
// used because some tmpfs mounts reject the raw system.posix_acl_access
// xattr even though the ACL helper works.
func applyUnixACL(path string, uids []int) error {
	for _, uid := range uids {
		if uid == 0 {
			continue
		}
		out, err := exec.Command("setfacl", "-m", fmt.Sprintf("u:%d:rw", uid), path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("setfacl %s uid %d: %w: %s", path, uid, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
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
	if peerUID, err := peerCredential(c.c); err == nil {
		uid = peerUID
	}
	if uid >= 0 {
		return WithCaller(base, Caller{UID: uid, Transport: TransportKernel})
	}
	return base
}
