// Package firewall opens the ports and interfaces Skein needs so any protocol
// can run over the overlay without manual rules.
package firewall

import (
	"fmt"
	"os/exec"
	"strings"
)

// Config identifies the underlay port and overlay interface.
type Config struct {
	Interface string
	Port      uint16
}

// Runner executes a command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

var runner Runner = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// SetRunner replaces the command runner. Tests use it.
func SetRunner(r Runner) { runner = r }

// Ensure opens the underlay UDP port and trusts the overlay interface.
func Ensure(cfg Config) error { return ensure(cfg) }

// Remove deletes the rules Ensure created. Missing rules are ignored.
func Remove(cfg Config) error { return remove(cfg) }
