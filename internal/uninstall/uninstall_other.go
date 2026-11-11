//go:build !linux && !windows

package uninstall

import (
	"context"
	"errors"
)

// Run reports that complete uninstallation is unsupported on this platform.
func Run(context.Context) (string, error) {
	return "", errors.New("complete uninstall is supported only on Linux and Windows")
}
