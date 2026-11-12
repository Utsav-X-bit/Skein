//go:build android

package app

import (
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

// androidFilesDir is the app's private files dir
// (Context.getFilesDir() on the Java side). It is the Android
// replacement for /etc/skein: an unprivileged app cannot write the
// system config directory, so the config, identity, access file, and
// backups all live here. The default is the standard files dir for
// this applicationId; SetFilesDir overrides it with the value the
// JNI bridge passes from Java, and must be called before any path is
// used (the daemon host does this in Start).
var androidFilesDir = "/data/data/com.skein.app/files"

// SetFilesDir points the app paths at the app's private files dir.
func SetFilesDir(dir string) {
	if dir == "" {
		return
	}
	androidFilesDir = dir
	AccessPath = filepath.Join(dir, "access")
}

// setControlField writes the platform control plane into the config.
// Android has none: the daemon is in-process and the GUI talks to it
// over the in-process transport, so a socket or pipe path must never
// be written into an Android config.
func setControlField(cfg *conf.Config) {}

// ApplyControl points an in-memory client config at the service control plane.
// No-op on Android (see setControlField).
func ApplyControl(cfg *conf.Config) { setControlField(cfg) }

// SystemDir holds the configuration and identity.
func SystemDir() string { return androidFilesDir }

// ControlSocket is the daemon's service control plane. Empty on
// Android: there is no Unix socket.
const ControlSocket = ""

// AccessPath lists the uids granted socket access, one per line. On
// Android it is the app-private access file; it is a var (not a
// const) so SetFilesDir can move it with the files dir. The live
// daemon reads the same path via daemon.controlAccessFile.
var AccessPath = filepath.Join(androidFilesDir, "access")

// BackupRoot is the durable directory for config backups.
func BackupRoot() string { return filepath.Join(androidFilesDir, "backups") }

func SystemConfigPath() string { return filepath.Join(androidFilesDir, "skein.toml") }

func SystemIdentityPath() string { return filepath.Join(androidFilesDir, "identity.key") }

// ControlPath is the API transport field the helper writes: api.socket on
// Unix, api.pipe on Windows. Empty on Android (in-process control).
func ControlPath() string { return "" }
