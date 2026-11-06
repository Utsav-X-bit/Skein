//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubEUID(t *testing.T, uid int) {
	t.Helper()
	old := geteuid
	geteuid = func() int { return uid }
	t.Cleanup(func() { geteuid = old })
	// Release builds ignore this; dev builds must not accidentally
	// bypass pkexec in these assertions.
	t.Setenv("SKEIN_HELPER_NO_PKEXEC", "")
}

// The token must come back on stdout through pkexec and must never
// appear in the elevated argv.
func TestExecSystemHelperCapturesBootstrapTokenFromPkexec(t *testing.T) {
	stubEUID(t, 1000)
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "pkexec.argv")
	writeCLIExecutable(t, filepath.Join(dir, "pkexec"), `#!/bin/sh
test "$1" = "/usr/libexec/skein-helper" || exit 64
test "$2" = "bootstrap" || exit 64
test "$3" = "--verb" || exit 64
test "$4" = "init" || exit 64
printf '%s\n' "$*" > "$PKEXEC_ARGV"
printf '%s\n' '{"ok":true,"token":"tok-123","verb":"init"}'
`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("PKEXEC_ARGV", argvLog)

	tok, err := mintSystemToken("init")
	if err != nil {
		t.Fatalf("mintSystemToken: %v", err)
	}
	if tok != "tok-123" {
		t.Fatalf("token = %q, want tok-123", tok)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read pkexec argv: %v", err)
	}
	argv := string(raw)
	if strings.Contains(argv, "tok-123") {
		t.Fatalf("bootstrap token leaked into argv: %s", argv)
	}
}

func writeCLIExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
