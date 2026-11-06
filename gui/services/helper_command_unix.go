//go:build !windows

package services

import (
	"context"
	"os/exec"
)

func defaultHelperPath() string { return "/usr/libexec/skein-helper" }

// pkexec selects the packaged policy from its exec.path annotation. It has
// no --action flag. The helper still validates its own fixed verb allowlist.
func helperCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	// M6 (F-04): the no-pkexec bypass is dev-only (securityEnv
	// returns "" in release builds), so a release GUI always
	// elevates through pkexec.
	if noPkexecOverride() {
		return exec.CommandContext(ctx, path, args...)
	}
	return exec.CommandContext(ctx, "pkexec", append([]string{path}, args...)...)
}
