//go:build android

package daemon

import "path/filepath"

// androidFilesDir is the app's private files dir
// (Context.getFilesDir() on the Java side). It is the Android
// replacement for /etc/skein (config, identity, access file) and
// /var/lib/skein (persisted paused state): an unprivileged app cannot
// write either system directory. The default is the standard files
// dir for this applicationId; SetAndroidFilesDir overrides it with
// the value the JNI bridge passes from Java, and must be called
// before daemon.New runs.
var androidFilesDir = "/data/data/com.skein.app/files"

// SetAndroidFilesDir points the daemon's on-disk state at the app's
// private files dir. It must be called before daemon.New: New reads
// the access file (controlAccessFile) and the paused state
// (stateFile) during construction.
func SetAndroidFilesDir(dir string) {
	if dir == "" {
		return
	}
	androidFilesDir = dir
	controlAccessFile = filepath.Join(dir, "access")
}

func init() {
	controlAccessFile = filepath.Join(androidFilesDir, "access")
}

// androidStateFile is the persisted paused-state path on Android:
// the app-private files dir, not /var/lib/skein.
func androidStateFile() string { return filepath.Join(androidFilesDir, "state.json") }
