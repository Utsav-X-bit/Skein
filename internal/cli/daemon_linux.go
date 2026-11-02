//go:build linux

package cli

import (
	"os/exec"
	"syscall"
)

// setParentDeath makes the daemon exit if the dashboard dies.
func setParentDeath(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
