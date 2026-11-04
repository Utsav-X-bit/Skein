//go:build !skeindev

package cli

import "testing"

// M6 — Agent E F-04, CLI mirror of the GUI rule: in a release
// build a planted environment must not redirect the helper the
// CLI invokes for a privileged verb, nor the daemon image a
// control dial verifies against.
func TestReleaseBuildIgnoresSecurityEnvVars(t *testing.T) {
	t.Setenv("SKEIN_HELPER", "/tmp/evil-helper")
	t.Setenv("SKEIN_DAEMON_IMAGE", "/tmp/evil-skeind")
	if got := systemHelperPath(); got == "/tmp/evil-helper" || got != defaultSystemHelperPath() {
		t.Fatalf("systemHelperPath with planted SKEIN_HELPER = %q, want packaged %q", got, defaultSystemHelperPath())
	}
	if got := expectedDaemonImage(); got != "" {
		t.Fatalf("expectedDaemonImage with planted env = %q, want empty (basename rule)", got)
	}
}
