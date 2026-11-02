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

// privilegeName is what the platform calls an administrator.
func privilegeName() string { return "root" }

// elevationHint tells the user how to run with privileges on this platform.
func elevationHint(configPath string) string {
	return "  sudo skein dashboard -c " + configPath + "\n" +
		"  or start it separately: sudo skein run -c " + configPath + "\n" +
		"  or install the service once: sudo skein install-service -c " + configPath +
		" && sudo systemctl enable --now skein"
}
