package daemon

// stateFileOverride is the test-only seam for stateFile(), mirroring
// the *Override variables in gui/services (M6, F-04). Daemon tests set
// it to a temp file so the persisted paused flag never touches the
// system path (/var/lib/skein/state.json). Production code never sets
// it; release builds ignore SKEIN_STATE_FILE entirely (env_release.go)
// and dev builds (-tags skeindev) honour it (env_dev.go).
var stateFileOverride string

// firewallDisabled reports whether the daemon should skip the firewall
// Ensure step after the TUN device exists. In a release build this is
// always false: SKEIN_NO_FIREWALL is ignored, so no environment value
// can silently disable a security control on a packaged install. A dev
// build (-tags skeindev) honours the variable for debugging.
func firewallDisabled() bool {
	return securityEnv("SKEIN_NO_FIREWALL") != ""
}
