//go:build windows

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func runHelper(ctx context.Context, verb string, args ...string) error {
	path := os.Getenv("SKEIN_HELPER")
	if path == "" {
		path = `C:\Program Files\Skein\skein-helper.exe`
	}
	cmd := exec.CommandContext(ctx, path, append([]string{verb}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper %s: %w: %s", verb, err, string(out))
	}
	return nil
}
