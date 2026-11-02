//go:build windows

package cli

import (
	"os/exec"
	"strings"
)

// daemonRunning reports the installed service or the daemon binary.
func daemonRunning() bool {
	if out, err := exec.Command("sc", "query", "skein").Output(); err == nil {
		if strings.Contains(strings.ToUpper(string(out)), "RUNNING") {
			return true
		}
	}
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq skeind.exe", "/NH").Output()
	return err == nil && strings.Contains(strings.ToLower(string(out)), "skeind.exe")
}
