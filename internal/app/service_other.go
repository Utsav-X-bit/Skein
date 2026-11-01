//go:build !windows

package app

import (
	"context"
	"errors"
)

func isWindowsService() (bool, error) { return false, nil }

func runAsService(_ func(context.Context) error) error {
	return errors.New("not a Windows service")
}
