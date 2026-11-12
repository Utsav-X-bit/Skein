//go:build !windows && !android

package app

import (
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

const (
	systemDir     = "/etc/skein"
	backupRoot    = "/var/lib/skein/backups"
	controlSocket = "/run/skein/control.sock"
)

// setControlField writes the platform control plane into the config.
func setControlField(cfg *conf.Config) { cfg.API.Socket = controlSocket }

// ApplyControl points an in-memory client config at the service control plane.
func ApplyControl(cfg *conf.Config) { setControlField(cfg) }

// SystemDir holds the root-owned configuration and identity.
func SystemDir() string { return systemDir }

// ControlSocket is the daemon's service control plane.
const ControlSocket = controlSocket

// AccessPath lists the uids granted socket access, one per line.
const AccessPath = systemDir + "/access"

// BackupRoot is the durable directory for config backups; StateDirectory
// keeps it across reboots.
func BackupRoot() string { return backupRoot }

func SystemConfigPath() string { return filepath.Join(systemDir, "skein.toml") }

func SystemIdentityPath() string { return filepath.Join(systemDir, "identity.key") }

// ControlPath is the API transport field the helper writes: api.socket on
// Unix, api.pipe on Windows.
func ControlPath() string { return controlSocket }
