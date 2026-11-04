//go:build skeindev

package cli

import "os"

// securityEnv is the dev-build implementation only: with
// -tags skeindev, SKEIN_* security variables work as they did
// before M6, so a developer can point a dev CLI at a locally
// built helper / test daemon without a package install.
// NEVER ship a skeindev build to users: it reopens F-04.
// Release builds (env_release.go) ignore these variables.
func securityEnv(name string) string { return os.Getenv(name) }

// devBuild is true only with -tags skeindev. It keeps the dev-tree
// helper-path preference (helper beside the CLI binary) working
// for developers; release builds use the packaged helper only.
const devBuild = true
