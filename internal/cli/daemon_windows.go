//go:build windows

package cli

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// elevated reports whether the process is running with administrator rights.
func elevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// terminate stops the daemon. Windows has no SIGTERM for console children.
func terminate(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}

// setParentDeath is a no-op on Windows; the dashboard kills the child itself.
func setParentDeath(*exec.Cmd) {}
