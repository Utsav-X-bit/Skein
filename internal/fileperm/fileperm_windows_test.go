//go:build windows

package fileperm

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDirectoryHardeningPreservesExistingLock(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		apply func(string) error
	}{
		{"restrict", Restrict},
		{"restrict-dacl", RestrictDACL},
		{"root", Root},
		{"grant-user", func(path string) error { return GrantUser(path, user.User.Sid.String()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "root" && !windows.GetCurrentProcessToken().IsElevated() {
				t.Skip("reading a service-only file requires an administrator")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "skein.toml.lock")
			const want = "existing lock data"
			if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tc.apply(dir); err != nil {
				t.Fatal(err)
			}
			sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			dacl, _, err := sd.DACL()
			// Backup privileges in administrator SSH tokens can mask denied reads.
			if err != nil || dacl == nil || dacl.AceCount == 0 {
				t.Fatalf("lock lost its access rules after directory hardening: %v", err)
			}
			f, err := os.OpenFile(path, os.O_RDWR, 0o600)
			if err != nil {
				t.Fatalf("reopen existing lock: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Fatalf("lock data changed: %q, err=%v", got, err)
			}
			if ok, err := NoWorldAccess(path); err != nil || !ok {
				t.Fatalf("lock grants broad access: ok=%v err=%v", ok, err)
			}
		})
	}
}
