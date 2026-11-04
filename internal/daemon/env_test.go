//go:build !skeindev

package daemon

import (
	"path/filepath"
	"strings"
	"testing"
)

// Release build (no -tags skeindev): a planted daemon environment
// must not redirect the persisted paused-state path or disable the
// firewall step. This closes the M8 residual (F-04-shaped hole) for
// SKEIN_STATE_FILE and SKEIN_NO_FIREWALL, mirroring the GUI/CLI rule
// from M6. The dev-tag counterpart lives in env_dev_test.go.
func TestReleaseBuildIgnoresDaemonSecurityEnvVars(t *testing.T) {
	planted := filepath.Join(t.TempDir(), "evil-state.json")
	t.Setenv("SKEIN_STATE_FILE", planted)
	t.Setenv("SKEIN_NO_FIREWALL", "1")

	if got := stateFile(); got == planted {
		t.Fatalf("stateFile with planted SKEIN_STATE_FILE = %q, want system default", got)
	}
	// System default ends in the Skein state file on both platforms
	// (/var/lib/skein/state.json on Unix, %ProgramData%\Skein\state.json
	// on Windows); the planted temp path must not appear.
	if got := stateFile(); !strings.HasSuffix(got, "state.json") || strings.Contains(got, "evil-state") {
		t.Fatalf("stateFile with planted env = %q, want system default", got)
	}
	if firewallDisabled() {
		t.Fatal("firewallDisabled with planted SKEIN_NO_FIREWALL = true, want false in release")
	}
}

// The test-only override seam must still work: it is the only
// in-process way for a daemon test to point the paused-state path at
// a temp file now that the environment is ignored.
func TestStateFileOverrideSeamStillWorks(t *testing.T) {
	stateFileOverride = filepath.Join(t.TempDir(), "fake-state.json")
	t.Cleanup(func() { stateFileOverride = "" })
	if got := stateFile(); got != stateFileOverride {
		t.Fatalf("stateFile override = %q, want %q", got, stateFileOverride)
	}
}
