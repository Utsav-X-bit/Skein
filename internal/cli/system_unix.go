//go:build !windows

package cli

import (
	"os"
	"path/filepath"
)

// M6 (F-06, residual of the helper-dir planting flaw): in a
// release build the CLI uses the packaged helper only
// (/usr/libexec/skein-helper), never a helper beside its own
// executable — a root-run CLI from a writable directory (Downloads,
// a dev tree, a portable layout) must not execute a planted
// helper as root. A dev build (-tags skeindev, devBuild=true)
// keeps the old exe-dir preference so a dev tree works without
// a package install. If no packaged helper exists, the release
// CLI fails loudly at exec time instead of silently trusting a
// writable-directory binary.
func defaultSystemHelperPath() string {
	if devBuild {
		if exe, err := os.Executable(); err == nil {
			if p := filepath.Join(filepath.Dir(exe), "skein-helper"); p != "" {
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	return "/usr/libexec/skein-helper"
}

// devControlTransport is the kernel control transport for a dev config:
// a socket beside the config file. It is deliberately not the service
// socket, so a dev daemon and the service daemon never share a listener.
func devControlTransport(configPath string) (socket, pipe string) {
	return filepath.Join(filepath.Dir(configPath), "control.sock"), ""
}
