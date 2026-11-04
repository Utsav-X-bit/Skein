//go:build skeindev

package daemon

import "os"

// securityEnv is the dev-build implementation only: with
// -tags skeindev, the daemon security variables work as they did
// before this change, so a developer (and the netns test harness,
// which builds its daemons with -tags skeindev) can redirect the
// paused-state path and skip firewall setup without a package
// install. Release builds (env_release.go) ignore these variables.
// NEVER ship a skeindev build to users: it reopens F-04.
func securityEnv(name string) string { return os.Getenv(name) }
