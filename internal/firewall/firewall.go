// Package firewall opens the ports and interfaces Skein needs so any protocol
// can run over the overlay without manual rules.
package firewall

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrInterfaceMissing reports that the overlay interface does not exist yet,
// so a rule that binds to it cannot be created.
var ErrInterfaceMissing = errors.New("firewall: overlay interface not found")

// classifyInterfaceError turns the platform error for a missing adapter into
// ErrInterfaceMissing.
func classifyInterfaceError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "interface was not found") {
		return fmt.Errorf("%w: %v", ErrInterfaceMissing, err)
	}
	return err
}

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

// Rule is one rule Skein manages, as the manager reports it.
type Rule struct {
	Name      string `json:"name"`
	Direction string `json:"direction"` // in, out, or forward
	Action    string `json:"action"`
	Protocol  string `json:"protocol"`
	Port      string `json:"port,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// Status inspects the manager for Skein's rules without changing anything.
// State is "ok" when every rule is present, "degraded" when the manager is
// active but a rule is missing, and "unknown" when the manager could not be
// queried.
type Status struct {
	Manager     string `json:"manager"`
	State       string `json:"state"`
	Rules       []Rule `json:"rules"`
	LastCheckMS int64  `json:"last_check_ms"`
	LastError   string `json:"last_error,omitempty"`
}

// ReadStatus inspects the firewall state for cfg without changing anything.
func ReadStatus(cfg Config) (Status, error) { return status(cfg) }

// Repair re-applies the managed rules transactionally and returns the
// refreshed status.
func Repair(cfg Config) (Status, error) { return repair(cfg) }
