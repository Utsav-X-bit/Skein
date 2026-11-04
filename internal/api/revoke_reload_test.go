package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestRevokeTakesEffectWithoutRestart is the daemon half of the Step 10
// revoke-user story: the helper edits the access file, then the daemon
// re-reads it via LoadGrantsFromFile (exactly what Daemon.ReloadGrants
// does for POST /api/v1/system/grants/reload). No restart happens in
// this test, and the revoked caller's next secret-endpoint call must
// fail while root's implicit secret access survives.
func TestRevokeTakesEffectWithoutRestart(t *testing.T) {
	srv, st := newSystemTestServer(map[string]Role{"uid:1001": RoleSecret})
	h := srv.Handler()

	// As a secret holder, uid 1001 may call the secret reload endpoint.
	rec := doKernel(t, h, http.MethodPost, "/api/v1/system/grants/reload", "", nil, 1001)
	if rec.Code != http.StatusOK || !st.reloaded {
		t.Fatalf("before revoke: reload as 1001 = %d, reloaded=%v", rec.Code, st.reloaded)
	}

	// Helper-side revoke: rewrite the file without 1001, then reload.
	dir := t.TempDir()
	access := filepath.Join(dir, "access")
	if err := os.WriteFile(access, []byte("1002:read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.reloaded = false
	if err := srv.LoadGrantsFromFile(access); err != nil {
		t.Fatal(err)
	}

	// The revoked caller's next secret call is denied, without restart.
	rec = doKernel(t, h, http.MethodPost, "/api/v1/system/grants/reload", "", nil, 1001)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("after revoke: reload as 1001 = %d, want 403", rec.Code)
	}
	// Root keeps its implicit secret access (api.roleFor).
	rec = doKernel(t, h, http.MethodPost, "/api/v1/system/grants/reload", "", nil, 0)
	if rec.Code != http.StatusOK {
		t.Fatalf("after revoke: reload as root = %d, want 200", rec.Code)
	}
}
