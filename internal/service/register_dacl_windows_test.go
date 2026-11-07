//go:build windows

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// Byte copies must remain readable and private beneath hardened directory ACLs.
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
	// Match set-owner's directory ownership policy.
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
