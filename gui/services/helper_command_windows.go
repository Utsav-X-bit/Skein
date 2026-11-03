//go:build windows

package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// defaultHelperPath prefers the helper shipped beside the desktop binary,
// then the installed Program Files location.
func defaultHelperPath() string {
	if p := os.Getenv("SKEIN_HELPER"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "skein-helper.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return `C:\Program Files\Skein\skein-helper.exe`
}

func helperCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}
