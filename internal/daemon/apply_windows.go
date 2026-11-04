//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// spawnDetached launches the detached apply process on Windows:
// DETACHED_PROCESS + a new process group, so it survives the old
// service process's exit while the service is stopped and
// re-pointed. Stdout/stderr go to logPath (appended).
// Compile-checked here only; the detached-apply half on Windows
// needs the user's Windows machine to verify (plan §10.2).
func spawnDetached(exe string, args []string, logPath string) error {
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("daemon: apply log %s: %w", logPath, err)
	}
	defer logF.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon: spawn %s: %w", exe, err)
	}
	return nil
}
