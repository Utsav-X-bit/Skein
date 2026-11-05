//go:build unix

package api

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// unixServerConn dials a throwaway Unix listener in this process and
// returns the client connection and the socket path; the server side
// is held open until the test ends, so SO_PEERCRED sees a live peer.
func unixServerConn(t *testing.T) (net.Conn, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verify.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	select {
	case srv := <-accepted:
		t.Cleanup(func() { srv.Close() })
	default:
	}
	return conn, path
}

func TestVerifyUnixServerAcceptsOwnProcess(t *testing.T) {
	conn, _ := unixServerConn(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pid, uid, err := VerifyUnixServer(conn, exe)
	if err != nil {
		t.Fatalf("VerifyUnixServer(exe) = %v, want nil", err)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d, want > 0", pid)
	}
	if uid != os.Getuid() {
		t.Fatalf("uid = %d, want %d (test process)", uid, os.Getuid())
	}
}

func TestVerifyUnixServerRejectsWrongImage(t *testing.T) {
	conn, _ := unixServerConn(t)
	if _, _, err := VerifyUnixServer(conn, "/nonexistent/skeind"); err == nil {
		t.Fatal("VerifyUnixServer(wrong image) = nil, want error")
	}
}

func TestVerifyUnixServerBasenameRuleRejectsTestBinary(t *testing.T) {
	// The default rule only accepts binaries named skein/skeind. A
	// test binary is neither, so it must fail closed — this is the
	// rule working, not a broken test: real daemon binaries pass it.
	conn, _ := unixServerConn(t)
	if _, _, err := VerifyUnixServer(conn, ""); err == nil {
		t.Fatal("VerifyUnixServer(basename rule, test binary) = nil, want error")
	}
}

func TestVerifyUnixServerRejectsNonUnix(t *testing.T) {
	a, _ := net.Pipe()
	defer a.Close()
	if _, _, err := VerifyUnixServer(a, ""); err == nil {
		t.Fatal("VerifyUnixServer(net.Pipe) = nil, want error")
	}
}

// TestVerifyUnixServerRootPeerFallsBackOnUnreadableExe is the
// regression test for M9 bug #12: a non-root caller cannot read
// /proc/<pid>/exe of a root daemon (EACCES), so verification of a
// root peer must fall back to the socket-file check instead of
// failing. The stub simulates the unreadable /proc entry. It only
// runs as root, where the peer uid is 0 (the fallback's condition).
func TestVerifyUnixServerRootPeerFallsBackOnUnreadableExe(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs a root peer (uid 0)")
	}
	conn, path := unixServerConn(t)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	old := procExeReader
	procExeReader = func(int) (string, error) { return "", syscall.EACCES }
	t.Cleanup(func() { procExeReader = old })
	pid, uid, err := VerifyUnixServer(conn, "")
	if err != nil {
		t.Fatalf("VerifyUnixServer(root peer, unreadable exe) = %v, want nil", err)
	}
	if pid != -1 {
		t.Fatalf("pid = %d, want -1 (socket-file fallback)", pid)
	}
	if uid != 0 {
		t.Fatalf("uid = %d, want 0 (socket owner)", uid)
	}
}

// TestVerifyUnixServerRootPeerFallbackStillRejectsWorldWritableSocket
// proves the fallback is still a real check: a root peer with an
// unreadable image is accepted only when the socket file itself is
// sound. A world-writable socket must fail closed.
func TestVerifyUnixServerRootPeerFallbackStillRejectsWorldWritableSocket(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs a root peer (uid 0)")
	}
	conn, path := unixServerConn(t)
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	old := procExeReader
	procExeReader = func(int) (string, error) { return "", syscall.EACCES }
	t.Cleanup(func() { procExeReader = old })
	if _, _, err := VerifyUnixServer(conn, ""); err == nil {
		t.Fatal("VerifyUnixServer(root peer, world-writable socket) = nil, want error")
	}
}
