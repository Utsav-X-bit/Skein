//go:build windows

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// TestCopyHelpersSetDACLInProtectedDir is the regression for the
// upgrade/rollback byte copies after set-owner. set-owner gives the system
// directory a protected, non-inheritable DACL, so a file created inside it
// inherits no ACEs at all and is deny-all: the config backup would be
// unreadable by the rollback that restores it. The helpers must set the
// DACL themselves.
func TestCopyHelpersSetDACLInProtectedDir(t *testing.T) {
	sid, err := api.CurrentSID()
	if err != nil {
		t.Fatalf("current SID: %v", err)
	}
	src := filepath.Join(t.TempDir(), daemonBaseName())
	if err := os.WriteFile(src, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	dstDir := t.TempDir()
	// The shape set-owner leaves the system directory in: SYSTEM,
	// Administrators, and the interactive user, protected and
	// non-inheritable.
	if err := fileperm.GrantUser(dstDir, sid); err != nil {
		t.Fatalf("grant destination dir: %v", err)
	}
	for _, tc := range []struct {
		name string
		copy func(src, dst string) error
	}{
		{"copyFile", copyFile},
		{"copyFileAtomic", copyFileAtomic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(dstDir, tc.name)
			if err := tc.copy(src, dst); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			raw, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("%s destination unreadable (empty DACL?): %v", tc.name, err)
			}
			if string(raw) != "binary" {
				t.Fatalf("%s destination = %q", tc.name, raw)
			}
			ok, err := fileperm.NoWorldAccess(dst)
			if err != nil || !ok {
				t.Fatalf("%s destination world access: ok=%v err=%v", tc.name, ok, err)
			}
		})
	}
}
