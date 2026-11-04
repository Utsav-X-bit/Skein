//go:build !skeindev

package services

import "testing"

// M6 — Agent E F-04. In a release build (no -tags skeindev) a
// planted environment must not redirect the elevated helper, the
// control socket/pipe, or the daemon-image verification target.
// This test sets exactly the hostile environment a same-user
// process could set before launching the GUI, and asserts every
// resolution ignores it. The dev-tag counterpart lives in
// env_dev_test.go and only runs with -tags skeindev.
func TestReleaseBuildIgnoresSecurityEnvVars(t *testing.T) {
	t.Setenv("SKEIN_HELPER", "/tmp/evil-helper")
	t.Setenv("SKEIN_CONTROL_SOCKET", "/tmp/evil.sock")
	t.Setenv("SKEIN_CONTROL_PIPE", `\\.\pipe\evil-control`)
	t.Setenv("SKEIN_DAEMON_IMAGE", "/tmp/evil-skeind")
	t.Setenv("SKEIN_HELPER_NO_PKEXEC", "1")

	if got := helperPath(); got != defaultHelperPath() {
		t.Fatalf("helperPath with planted SKEIN_HELPER = %q, want packaged %q", got, defaultHelperPath())
	}
	if got := controlSocketPath(); got != "/run/skein/control.sock" {
		t.Fatalf("controlSocketPath with planted env = %q, want service socket", got)
	}
	if got := controlPipeName(); got != `\\.\pipe\skein-control` {
		t.Fatalf("controlPipeName with planted env = %q, want service pipe", got)
	}
	if got := expectedDaemonImage(); got != "" {
		t.Fatalf("expectedDaemonImage with planted env = %q, want empty (basename rule)", got)
	}
	if noPkexecOverride() {
		t.Fatal("noPkexecOverride with SKEIN_HELPER_NO_PKEXEC=1 = true, want false in release")
	}
}

// The test-only override seam must still work: it is the only
// in-process way for a test to point a client at a fake daemon
// now that the environment is ignored.
func TestTestOverrideSeamStillPointsAtFakeDaemon(t *testing.T) {
	controlSocketOverride = "/tmp/fake-control.sock"
	daemonImageOverride = "/tmp/fake-skeind"
	helperPathOverride = "/tmp/fake-helper"
	t.Cleanup(func() {
		controlSocketOverride, daemonImageOverride, helperPathOverride = "", "", ""
	})
	if got := controlSocketPath(); got != "/tmp/fake-control.sock" {
		t.Fatalf("controlSocketPath override = %q", got)
	}
	if got := expectedDaemonImage(); got != "/tmp/fake-skeind" {
		t.Fatalf("expectedDaemonImage override = %q", got)
	}
	if got := helperPath(); got != "/tmp/fake-helper" {
		t.Fatalf("helperPath override = %q", got)
	}
}
