//go:build skeindev

package daemon

import (
	"path/filepath"
	"testing"
)

// Dev-build counterpart of env_test.go: with -tags skeindev the
// daemon security variables work again, so a developer (and the
// netns test harness, which builds its daemons with -tags skeindev)
// can redirect the paused-state path and skip firewall setup. This
// file runs ONLY with that tag; a release build must never pass it
// (its securityEnv returns "" for every name).
func TestDevBuildHonoursDaemonSecurityEnvVars(t *testing.T) {
	planted := filepath.Join(t.TempDir(), "dev-state.json")
	t.Setenv("SKEIN_STATE_FILE", planted)
	t.Setenv("SKEIN_NO_FIREWALL", "1")
	if got := stateFile(); got != planted {
		t.Fatalf("dev stateFile = %q, want %q", got, planted)
	}
	if !firewallDisabled() {
		t.Fatal("dev firewallDisabled with SKEIN_NO_FIREWALL=1 = false, want true")
	}
}
