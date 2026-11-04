//go:build skeindev

package services

import "os"

// securityEnv is the dev-build implementation only: with
// -tags skeindev, SKEIN_* security variables work as they did
// before M6, so a developer can point a dev GUI at a locally
// built helper / test daemon without a package install.
// NEVER ship a skeindev build to users: it reopens F-04, because
// any process running as the user can redirect the elevated
// helper binary and the daemon connection. Release builds
// (env_release.go) ignore these variables entirely.
func securityEnv(name string) string { return os.Getenv(name) }
