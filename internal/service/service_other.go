//go:build !linux && !windows

package service

import "errors"

func install(_, _ string) error { return errors.New("service: unsupported platform") }
func uninstall() error          { return errors.New("service: unsupported platform") }
