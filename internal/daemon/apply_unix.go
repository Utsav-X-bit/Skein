//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached launches the detached apply process: a new
// session (Setsid) so it survives the old daemon's exit, with
// stdout/stderr appended to logPath (0600). The child is not
// waited on — the parent returns as soon as Start succeeds, and
// the child reports via the update state file.
func spawnDetached(exe string, args []string, logPath string) error {
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("daemon: apply log %s: %w", logPath, err)
	}
	defer logF.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("daemon: spawn %s: %w", exe, err)
	}
	return nil
}
