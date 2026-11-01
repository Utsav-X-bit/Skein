//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// elevated reports whether the process can create a TUN device.
func elevated() bool { return os.Geteuid() == 0 }

// terminate asks the daemon to shut down cleanly.
func terminate(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

// setParentDeath makes the daemon exit if the dashboard dies.
func setParentDeath(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
