//go:build unix

package api

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStaleSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")

	// A socket with no listener is stale: it is removed and the listener
	// starts. Bind without listening and keep the fd open so the file
	// survives Close on Go versions that unlink on close.
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewUnixListener(path, "", "")
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	fresh.Close()

	// A live listener is busy, not stale.
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewUnixListener(path, "", ""); !errors.Is(err, ErrSocketBusy) {
		t.Fatalf("busy socket: %v, want ErrSocketBusy", err)
	}
	live.Close()

	// A non-socket file is an error.
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewUnixListener(path, "", ""); err == nil {
		t.Fatal("non-socket file accepted")
	}
}

func TestReadGrantedUIDs(t *testing.T) {
	if uids, err := readGrantedUIDs(""); err != nil || uids != nil {
		t.Fatalf("empty path: %v %v", uids, err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "access")
	if err := os.WriteFile(p, []byte("# comment\n1000\n\n1001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uids, err := readGrantedUIDs(p)
	if err != nil || len(uids) != 2 || !slices.Contains(uids, 1000) || !slices.Contains(uids, 1001) {
		t.Fatalf("uids = %v, %v", uids, err)
	}
	if err := os.WriteFile(p, []byte("nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGrantedUIDs(p); err == nil {
		t.Fatal("bad uid accepted")
	}
	if _, err := readGrantedUIDs(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}

// TestGrantedUIDACL: the access file's uids land in the socket's POSIX ACL.
func TestGrantedUIDACL(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses the DACL; run unprivileged to exercise the ACL")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")
	access := filepath.Join(dir, "access")
	uid := os.Getuid() + 1
	if err := os.WriteFile(access, []byte(fmt.Sprintf("%d\n", uid)), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := NewUnixListener(path, "", access)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem lacks POSIX ACL support: %v", err)
		}
		t.Fatal(err)
	}
	defer ln.Close()

	buf := make([]byte, 512)
	n, err := unix.Getxattr(path, "system.posix_acl_access", buf)
	if err != nil {
		t.Skipf("read ACL back: %v", err)
	}
	raw := buf[:n]
	if len(raw) < 12 || binary.LittleEndian.Uint32(raw[:4]) != 2 {
		t.Fatalf("bad ACL blob: %x", raw)
	}
	found := false
	for i := 4; i+8 <= len(raw); i += 8 {
		tag := binary.LittleEndian.Uint16(raw[i : i+2])
		id := int(binary.LittleEndian.Uint32(raw[i+4 : i+8]))
		if tag == 0x02 && id == uid { // ACL_USER entry for the granted uid
			found = true
		}
	}
	if !found {
		t.Fatalf("granted uid %d missing from ACL blob %x", uid, raw)
	}
}

// doRaw performs one HTTP request over an existing connection.
func doRaw(t *testing.T, c net.Conn, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://control"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := req.Write(c); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestPeerUIDAuthentication: a socket client without a bearer token is
// authenticated by SO_PEERCRED, and the uid lands in the audit event.
func TestPeerUIDAuthentication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")
	ln, err := NewUnixListener(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv, _, _, _ := newTestServer(t)
	grantTestUser(srv, os.Getuid(), RoleMutate)
	events := make(chan AuditEvent, 4)
	srv.Audit(func(e AuditEvent) { events <- e })
	go func() {
		s := &http.Server{Handler: srv.Handler(), ConnContext: CallerContext}
		_ = s.Serve(ln)
	}()

	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	resp := doRaw(t, c, http.MethodPost, "/api/v1/name", `{"name":"x"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (caller uid, no bearer)", resp.StatusCode)
	}

	select {
	case ev := <-events:
		want := "uid:" + strconv.Itoa(os.Getuid())
		if ev.Caller != want || ev.Method != http.MethodPost || ev.Status != http.StatusNoContent {
			t.Fatalf("audit event = %+v, want caller %s", ev, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no audit event")
	}
}
