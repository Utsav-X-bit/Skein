//go:build !skeindev

package cli

// securityEnv is the release-build implementation: security
// variables are ignored, unconditionally. See env.go in the GUI
// services package (M6, Agent E F-04) for the same rule applied
// to the CLI: SKEIN_HELPER must not redirect which binary the
// CLI invokes for a privileged helper verb, and SKEIN_DAEMON_IMAGE
// must not redirect which daemon image a control dial verifies
// against. A dev build (-tags skeindev) uses env_dev.go instead.
func securityEnv(string) string { return "" }

// devBuild is false in release builds. It gates the dev-tree
// helper-path preference in system_unix.go (M6, F-06): a release
// CLI must never pick a helper from beside its own executable,
// because a root-run CLI from a writable directory would then
// execute a planted helper as root.
const devBuild = false
