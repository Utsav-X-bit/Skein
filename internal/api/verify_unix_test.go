//go:build unix

package api

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// unixServerConn dials a throwaway Unix listener in this process and
// returns the client connection; the server side is held open until
// the test ends, so SO_PEERCRED sees a live peer.
func unixServerConn(t *testing.T) net.Conn {
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
	return conn
}

func TestVerifyUnixServerAcceptsOwnProcess(t *testing.T) {
	conn := unixServerConn(t)
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
	conn := unixServerConn(t)
	if _, _, err := VerifyUnixServer(conn, "/nonexistent/skeind"); err == nil {
		t.Fatal("VerifyUnixServer(wrong image) = nil, want error")
	}
}

func TestVerifyUnixServerBasenameRuleRejectsTestBinary(t *testing.T) {
	// The default rule only accepts binaries named skein/skeind. A
	// test binary is neither, so it must fail closed — this is the
	// rule working, not a broken test: real daemon binaries pass it.
	conn := unixServerConn(t)
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
