//go:build windows

package services

import (
	"context"
	"os/exec"
)

func defaultHelperPath() string { return `C:\Program Files\Skein\skein-helper.exe` }

func helperCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, path, args...)
}
