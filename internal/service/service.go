// Package service installs and removes the Skein system service.
package service

// Name is the service name used by systemd and the Windows SCM.
const Name = "skein"

// Install registers the daemon as a system service.
func Install(executable, configPath string) error { return install(executable, configPath) }

// Uninstall removes the service registration.
func Uninstall() error { return uninstall() }
