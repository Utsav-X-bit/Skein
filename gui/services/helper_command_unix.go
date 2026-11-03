//go:build !windows

package services

import (
	"context"
	"os"
	"os/exec"
)

func defaultHelperPath() string { return "/usr/libexec/skein-helper" }

func helperCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	if os.Getenv("SKEIN_HELPER_NO_PKEXEC") == "1" {
		return exec.CommandContext(ctx, path, args...)
	}
	return exec.CommandContext(ctx, "pkexec", append([]string{path}, args...)...)
}
