//go:build !windows

package cli

import (
	"os/exec"
	"strings"
)

// daemonRunning reports the installed service or a manual daemon process.
func daemonRunning() bool {
	if out, err := exec.Command("systemctl", "is-active", "skein").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "active" {
			return true
		}
	}
	out, err := exec.Command("pgrep", "-f", `skein(d)? run`).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
