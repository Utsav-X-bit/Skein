//go:build !windows

package app

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAccess(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "access")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readAccess(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRevokeGrantFromFile(t *testing.T) {
	t.Run("removes one grant and keeps the rest byte-identical", func(t *testing.T) {
		p := writeAccess(t, t.TempDir(), "# grants\n1001:secret\n1002:mutate\n1003\n")
		out, err := revokeGrantFromFile(p, "1002", false)
		if err != nil {
			t.Fatal(err)
		}
		if out.Role != "mutate" || out.Warning != "" || out.Reloaded {
			t.Fatalf("unexpected outcome %+v", out)
		}
		if got := readAccess(t, p); got != "# grants\n1001:secret\n1003\n" {
			t.Fatalf("file = %q", got)
		}
	})

	t.Run("missing grant is a clean error and the file is untouched", func(t *testing.T) {
		p := writeAccess(t, t.TempDir(), "1001:secret\n")
		if _, err := revokeGrantFromFile(p, "9999", false); err == nil || !strings.Contains(err.Error(), "has no grant") {
			t.Fatalf("err = %v", err)
		}
		if got := readAccess(t, p); got != "1001:secret\n" {
			t.Fatalf("file changed to %q", got)
		}
	})

	t.Run("missing file is a clean error", func(t *testing.T) {
		if _, err := revokeGrantFromFile(filepath.Join(t.TempDir(), "nope"), "1001", false); err == nil || !strings.Contains(err.Error(), "no grants file") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("malformed file is never rewritten", func(t *testing.T) {
		p := writeAccess(t, t.TempDir(), "1001:secret\n1002:bogus\n")
		if _, err := revokeGrantFromFile(p, "1001", true); err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("err = %v", err)
		}
		if got := readAccess(t, p); got != "1001:secret\n1002:bogus\n" {
			t.Fatalf("file changed to %q", got)
		}
	})

	t.Run("last secret grant needs force", func(t *testing.T) {
		p := writeAccess(t, t.TempDir(), "1001:secret\n1002:read\n")
		if _, err := revokeGrantFromFile(p, "1001", false); err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v", err)
		}
		out, err := revokeGrantFromFile(p, "1001", true)
		if err != nil {
			t.Fatal(err)
		}
		if out.Warning == "" {
			t.Fatal("forced last-secret removal should carry a warning")
		}
		if got := readAccess(t, p); got != "1002:read\n" {
			t.Fatalf("file = %q", got)
		}
	})

	t.Run("last grant needs force and the file is kept empty, never deleted", func(t *testing.T) {
		p := writeAccess(t, t.TempDir(), "1001:mutate\n")
		if _, err := revokeGrantFromFile(p, "1001", false); err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v", err)
		}
		if _, err := revokeGrantFromFile(p, "1001", true); err != nil {
			t.Fatal(err)
		}
		// Empty, not deleted: a reload reads "configured, no grants"
		// (deny) instead of "unconfigured" (kernel callers read-only).
		if got := readAccess(t, p); got != "" {
			t.Fatalf("file = %q, want empty", got)
		}
	})

	t.Run("removing a non-secret grant while a secret remains needs no force", func(t *testing.T) {
		p := writeAccess(t, t.TempDir(), "1001:secret\n1002:read\n")
		if _, err := revokeGrantFromFile(p, "1002", false); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAccessRoles(t *testing.T) {
	p := writeAccess(t, t.TempDir(), "# c\n1001:secret\n1002\n")
	roles, err := accessRoles(p)
	if err != nil {
		t.Fatal(err)
	}
	if roles["1001"] != "secret" || roles["1002"] != "" {
		t.Fatalf("roles = %v", roles)
	}
	if _, err := accessRoles(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatalf("missing file should be no grants, got %v", err)
	}
}

// TestReloadGrantsViaSocket spins up a real HTTP server on a temp Unix
// socket: this is the exact transport RevokeUser uses, and it proves
// the reload POST (method, path, 2xx handling) without a daemon.
func TestReloadGrantsViaSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var method, path string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	if err := reloadGrantsViaSocket(sock); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/api/v1/system/grants/reload" {
		t.Fatalf("reload hit %s %s", method, path)
	}
}

func TestReloadGrantsViaSocketRefusalIsError(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})}
	go srv.Serve(ln)
	defer srv.Close()
	if err := reloadGrantsViaSocket(sock); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestReloadGrantsViaSocketDownIsDialError(t *testing.T) {
	// No listener at all: dial fails, which RevokeUser maps to
	// "daemon not running", not to a refusal.
	err := reloadGrantsViaSocket(filepath.Join(t.TempDir(), "missing.sock"))
	if err == nil {
		t.Fatal("want dial error")
	}
	if !isDaemonDown(err) {
		t.Fatalf("dial error should classify as daemon down, got %v", err)
	}
}
