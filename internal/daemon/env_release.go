//go:build !skeindev

package daemon

// securityEnv is the release-build implementation: daemon security
// variables are ignored, unconditionally. This closes the M8 residual
// (F-04-shaped hole): SKEIN_NO_FIREWALL skipped the firewall Ensure
// step and SKEIN_STATE_FILE redirected the persisted paused-state
// path in release builds. A dev build (-tags skeindev) uses
// env_dev.go instead, mirroring the GUI/CLI rule from M6.
// NEVER ship a skeindev build to users: it reopens F-04.
func securityEnv(string) string { return "" }
