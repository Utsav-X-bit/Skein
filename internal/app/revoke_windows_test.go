//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// TestWriteAccessFileAtomicKeepsDACLAfterSetOwner is the regression for
// the empty-DACL access file: set-owner replaces the system directory's
// DACL with protected, non-inheritable ACEs (fileperm.GrantUser), so a
// temp file created in that directory inherits nothing. Without an
// explicit DACL on the temp, the rename leaves a deny-all access file
// and the daemon cannot read its own grants on the next start (the
// "access file DACL empty after set-owner/restart" report).
func TestWriteAccessFileAtomicKeepsDACLAfterSetOwner(t *testing.T) {
	sid, err := api.CurrentSID()
	if err != nil {
		t.Fatalf("current SID: %v", err)
	}
	dir := t.TempDir()
	// The shape set-owner leaves the system directory in: SYSTEM,
	// Administrators, and the interactive user, protected and
	// non-inheritable.
	if err := fileperm.GrantUser(dir, sid); err != nil {
		t.Fatalf("grant directory: %v", err)
	}
	path := filepath.Join(dir, "access")
	if err := writeAccessFileAtomic(path, []byte(sid+":secret\n")); err != nil {
		t.Fatalf("writeAccessFileAtomic: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("access file unreadable (empty DACL?): %v", err)
	}
	if string(raw) != sid+":secret\n" {
		t.Fatalf("access file = %q", raw)
	}
	ok, err := fileperm.NoWorldAccess(path)
	if err != nil || !ok {
		t.Fatalf("access file world access: ok=%v err=%v", ok, err)
	}
}
