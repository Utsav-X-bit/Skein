//go:build !linux && !windows

package service

import "errors"

func status() (Status, error) {
	return Status{State: StateUnknown}, errors.New("service: unsupported platform")
}
func start() error   { return errors.New("service: unsupported platform") }
func stop() error    { return errors.New("service: unsupported platform") }
func restart() error { return errors.New("service: unsupported platform") }

func install(_, _ string) error { return errors.New("service: unsupported platform") }

func enable() error  { return errors.New("service: unsupported platform") }
func disable() error { return errors.New("service: unsupported platform") }

func uninstall() error { return errors.New("service: unsupported platform") }

func installed() (string, string, bool, error) {
	return "", "", false, nil
}

func rewrite(_, _ string) error { return errors.New("service: unsupported platform") }
