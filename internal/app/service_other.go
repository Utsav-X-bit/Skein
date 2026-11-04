//go:build !windows

package app

import (
	"context"
	"errors"
)

// Variables (not plain functions) so tests can stub the launch-mode
// decision; see service_windows.go. Production code never reassigns
// them.
var isWindowsService = func() (bool, error) { return false, nil }

var runAsService = func(_ func(context.Context) error) error {
	return errors.New("not a Windows service")
}
