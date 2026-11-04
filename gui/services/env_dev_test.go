//go:build skeindev

package services

import "testing"

// Dev-build counterpart of env_test.go: with -tags skeindev the
// security variables work again, so a developer can point a dev
// GUI at a local helper / test daemon. This file runs ONLY with
// that tag; a release build must never pass it (its securityEnv
// returns "" for every name).
func TestDevBuildHonoursSecurityEnvVars(t *testing.T) {
	t.Setenv("SKEIN_HELPER", "/tmp/dev-helper")
	t.Setenv("SKEIN_CONTROL_SOCKET", "/tmp/dev.sock")
	if got := securityEnv("SKEIN_HELPER"); got != "/tmp/dev-helper" {
		t.Fatalf("dev securityEnv(SKEIN_HELPER) = %q", got)
	}
	if got := helperPath(); got != "/tmp/dev-helper" {
		t.Fatalf("dev helperPath = %q, want /tmp/dev-helper", got)
	}
	if got := controlSocketPath(); got != "/tmp/dev.sock" {
		t.Fatalf("dev controlSocketPath = %q, want /tmp/dev.sock", got)
	}
	t.Setenv("SKEIN_HELPER_NO_PKEXEC", "1")
	if !noPkexecOverride() {
		t.Fatal("dev noPkexecOverride with SKEIN_HELPER_NO_PKEXEC=1 = false")
	}
}
