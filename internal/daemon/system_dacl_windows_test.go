//go:build windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// Updating directory ownership must preserve access to existing config/identity
// files, and their backups must remain readable and private.
func TestBackupSystemKeepsDACLInProtectedDir(t *testing.T) {
	sid, err := api.CurrentSID()
	if err != nil {
		t.Fatalf("current SID: %v", err)
	}
	d, dir := newSystemDaemon(t)
	// Match set-owner after the fixture has created its files.
	if err := fileperm.GrantUser(dir, sid); err != nil {
		t.Fatalf("grant system dir: %v", err)
	}
	bak, err := d.backupSystem("rotate")
	if err != nil {
		t.Fatalf("backupSystem: %v", err)
	}
	if bak == "" {
		t.Fatal("backupSystem returned no directory")
	}
	entries, err := os.ReadDir(bak)
	if err != nil {
		t.Fatalf("backup dir unreadable (empty DACL?): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("backup dir is empty")
	}
	for _, e := range entries {
		p := filepath.Join(bak, e.Name())
		if _, err := os.ReadFile(p); err != nil {
			t.Fatalf("backup %s unreadable: %v", e.Name(), err)
		}
		ok, err := fileperm.NoWorldAccess(p)
		if err != nil || !ok {
			t.Fatalf("backup %s world access: ok=%v err=%v", e.Name(), ok, err)
		}
	}
	ok, err := fileperm.NoWorldAccess(bak)
	if err != nil || !ok {
		t.Fatalf("backup dir world access: ok=%v err=%v", ok, err)
	}
}
