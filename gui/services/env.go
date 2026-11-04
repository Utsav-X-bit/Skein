package services

// M6 — Agent E F-04: security-sensitive environment variables.
//
// In a release build (the default: no -tags skeindev) the GUI ignores
// SKEIN_HELPER, SKEIN_CONTROL_SOCKET, SKEIN_CONTROL_PIPE,
// SKEIN_DAEMON_IMAGE and SKEIN_HELPER_NO_PKEXEC entirely. Before M6
// any process running as the user could launch the GUI with
// SKEIN_HELPER=/tmp/evil-helper and the GUI would ask the user to
// approve elevation for that binary, or redirect the GUI to an
// attacker-controlled control socket/pipe (compounding F-03).
// securityEnv is defined per build tag (env_release.go / env_dev.go):
// release returns "" for every name; dev returns os.Getenv(name).
//
// Test seam: tests in this package set the *Override variables
// instead of the environment. That is why step8_test.go no longer
// uses t.Setenv for the control socket / daemon image — a release
// build must ignore those variables even in tests, and the override
// variables are the only in-process way to point a test client at
// a fake daemon. They are package-private and never set by
// production code.
var (
	controlSocketOverride string
	controlPipeOverride   string
	helperPathOverride    string
	daemonImageOverride   string
)

// helperPath is the single helper-path resolution for the GUI:
// test override, then (dev only) SKEIN_HELPER, then the packaged
// default. It replaces the per-file os.Getenv("SKEIN_HELPER")
// lookups that used to live in helper.go, helper_json_*.go and
// helper_command_windows.go.
func helperPath() string {
	if helperPathOverride != "" {
		return helperPathOverride
	}
	if v := securityEnv("SKEIN_HELPER"); v != "" {
		return v
	}
	return defaultHelperPath()
}

// noPkexecOverride reports whether the dev-only
// SKEIN_HELPER_NO_PKEXEC=1 bypass is active. It is false in every
// release build (securityEnv returns ""), so release GUI flows
// always go through pkexec.
func noPkexecOverride() bool {
	return securityEnv("SKEIN_HELPER_NO_PKEXEC") == "1"
}
