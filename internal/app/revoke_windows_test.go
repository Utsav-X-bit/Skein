//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// Atomic grants must stay readable and private after directory hardening.
func TestWriteAccessFileAtomicKeepsDACLAfterSetOwner(t *testing.T) {
	sid, err := api.CurrentSID()
	if err != nil {
		t.Fatalf("current SID: %v", err)
	}
	dir := t.TempDir()
	// Match set-owner's directory ownership policy.
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
