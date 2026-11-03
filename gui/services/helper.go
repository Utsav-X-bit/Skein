//go:build !windows

package services

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// runHelper invokes the fixed-verb privileged helper. The helper path is
// root-owned; the GUI never passes a free-form shell or a config path.
func runHelper(ctx context.Context, verb string, args ...string) error {
	path := os.Getenv("SKEIN_HELPER")
	if path == "" {
		path = "/usr/libexec/skein-helper"
	}
	cmdArgs := append([]string{path, verb}, args...)
	cmd := exec.CommandContext(ctx, "pkexec", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper %s: %w: %s", verb, err, string(out))
	}
	return nil
}
