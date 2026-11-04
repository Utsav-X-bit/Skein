//go:build windows

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// M6 (F-06) note: the exe-dir preference is retained on Windows
// because the NSIS install puts the CLI, helper and daemon in the
// same admin-only $INSTDIR, so exe-dir == the packaged location
// there. It is NOT safe for a portable/zip layout (exe in a
// user-writable directory + planted skein-helper.exe beside it),
// which this VM cannot test at runtime; that layout is recorded
// as an open Windows-machine item in m6-security-report.md with
// an exact plant test, and must not be called safe.
func defaultSystemHelperPath() string {
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "skein-helper.exe"); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return `C:\Program Files\Skein\skein-helper.exe`
}

// devControlTransport is the kernel control transport for a dev config:
// a pipe name derived from the config directory, deliberately not the
// service pipe ("skein-control"), so a dev daemon and the service
// daemon never share a listener.
func devControlTransport(configPath string) (socket, pipe string) {
	sum := sha256.Sum256([]byte(filepath.Dir(configPath)))
	return "", "skein-dev-" + hex.EncodeToString(sum[:4])
}
