//go:build windows

package services

import (
	"context"
	"fmt"
	"os/exec"
)

func runHelper(ctx context.Context, verb string, args ...string) error {
	cmd := exec.CommandContext(ctx, defaultHelperPath(), append([]string{verb}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper %s: %w: %s", verb, err, string(out))
	}
	return nil
}
