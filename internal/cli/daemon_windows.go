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

// privilegeName is what the platform calls an administrator.
func privilegeName() string { return "Administrator" }

// elevationHint tells the user how to run with privileges on this platform.
func elevationHint(configPath string) string {
	return "  run an elevated prompt (Git Bash: right-click \u2192 Run as administrator), then:\n" +
		"    skein dashboard -c " + configPath + "\n" +
		"  or install the service once, elevated, and start it:\n" +
		"    skein install-service -c " + configPath + "\n" +
		"    sc start skein"
}

// setParentDeath is a no-op on Windows; the dashboard kills the child itself.
func setParentDeath(*exec.Cmd) {}
