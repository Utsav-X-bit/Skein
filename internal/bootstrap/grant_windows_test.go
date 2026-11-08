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

// Bootstrap grants must stay readable and private in a hardened directory.
func TestGrantSecretKeepsAccessFileDACLAfterSetOwner(t *testing.T) {
	sid, err := api.CurrentSID()
	if err != nil {
		t.Fatalf("current SID: %v", err)
	}
	dir := t.TempDir()
	if err := fileperm.GrantUser(dir, sid); err != nil {
		t.Fatalf("grant directory: %v", err)
	}
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
