//go:build windows

package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// TestGrantSecretKeepsAccessFileDACLAfterSetOwner is the regression for
// the empty-DACL access file. set-owner replaces the system directory's
// DACL with protected, non-inheritable ACEs, and the bootstrap grant is
// the next writer to run (init/join after the restart set-owner asks
// for). The temp file must be created in the access file's own
// directory — a rename cannot cross volumes and the directory decides
// inheritance — and given an explicit DACL, or the renamed file is
// deny-all and the daemon cannot read its own grants on the next start.
//
// The chdir pins the second half of the bug: the writer split the path
// on "/", which no Windows path contains, so its temp landed in the
// process CWD (for the service, C:\Windows\System32) and the access
// file inherited that directory's DACL instead.
func TestGrantSecretKeepsAccessFileDACLAfterSetOwner(t *testing.T) {
	sid, err := api.CurrentSID()
	if err != nil {
		t.Fatalf("current SID: %v", err)
	}
	dir := t.TempDir()
	if err := fileperm.GrantUser(dir, sid); err != nil {
		t.Fatalf("grant directory: %v", err)
	}
	t.Chdir(dir) // where the old writer created its temp file
	path := filepath.Join(dir, "access")
	if err := GrantSecret(path, sid); err != nil {
		t.Fatalf("GrantSecret: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("access file unreadable (empty DACL?): %v", err)
	}
	grants, err := api.ParseAccessGrants(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse grants: %v", err)
	}
	if grants[sid] != api.RoleSecret {
		t.Fatalf("grants = %v, want %s:secret", grants, sid)
	}
	ok, err := fileperm.NoWorldAccess(path)
	if err != nil || !ok {
		t.Fatalf("access file world access: ok=%v err=%v", ok, err)
	}
}
