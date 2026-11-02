//go:build !windows && !linux

package cli

import "os/exec"

// setParentDeath is a no-op where the platform has no parent-death signal.
func setParentDeath(*exec.Cmd) {}
