//go:build !windows

package services

import (
	"context"
	"fmt"
)

// runHelper invokes the fixed-verb privileged helper. The helper path is
// root-owned; the GUI never passes a free-form shell or a config path.
// It is a variable (not a func) so service-layer tests can substitute
// a fake helper without pkexec/UAC.
var runHelper = func(ctx context.Context, verb string, args ...string) error {
	// M6 (F-04): helperPath() ignores SKEIN_HELPER in release builds.
	path := helperPath()
	cmd := helperCommand(ctx, path, append([]string{verb}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper %s: %w: %s", verb, err, string(out))
	}
	return nil
}
