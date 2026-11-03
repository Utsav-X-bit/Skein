//go:build windows

package app

import (
	"os"
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

// setControlField writes the platform control plane into the config.
func setControlField(cfg *conf.Config) { cfg.API.Pipe = ControlPipeName }

// ControlPipeName is the bare pipe name written to api.pipe; the daemon
// prepends \\.\pipe\ itself.
const ControlPipeName = "skein-control"

// ControlPipe is the full Win32 path of the daemon's service control plane.
const ControlPipe = `\\.\pipe\` + ControlPipeName

// SystemDir holds the root-owned configuration and identity.
func SystemDir() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "Skein")
	}
	return `C:\ProgramData\Skein`
}

func SystemConfigPath() string { return filepath.Join(SystemDir(), "skein.toml") }

func SystemIdentityPath() string { return filepath.Join(SystemDir(), "identity.key") }

// ControlPath is the API transport field the helper writes: api.socket on
// Unix, api.pipe on Windows.
func ControlPath() string { return ControlPipe }

// BackupRoot is the durable directory for config backups.
func BackupRoot() string { return filepath.Join(SystemDir(), "backups") }
