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

// SetRunner replaces the command runner and returns the previous one, so
// tests can restore it.
func SetRunner(r Runner) Runner {
	old := runner
	runner = r
	return old
}

// Ensure opens the underlay UDP port and trusts the overlay interface.
func Ensure(cfg Config) error { return ensure(cfg) }

// EnsurePort opens only the underlay UDP port, for both address families. It
// is safe to call before the interface exists, so service installation can use
// it.
func EnsurePort(cfg Config) error { return ensurePort(cfg) }

// EnsureInterface trusts all inbound traffic on the overlay interface. It
// needs the interface to exist on Windows.
func EnsureInterface(cfg Config) error { return ensureInterface(cfg) }

// Remove deletes the rules Ensure created. Missing rules are ignored.
func Remove(cfg Config) error { return remove(cfg) }
