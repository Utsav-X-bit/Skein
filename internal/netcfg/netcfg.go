// Package netcfg applies addresses, routes, and MTU to the TUN interface and
// removes them again. It shells out to `ip` on Linux and `netsh` on Windows
// using argument arrays, never a shell.
package netcfg

import (
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
)

// CommandRunner executes a program with arguments and returns combined output.
type CommandRunner func(name string, args ...string) (string, error)

var runner CommandRunner = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// SetRunner replaces the command runner. Tests use it; production never does.
func SetRunner(r CommandRunner) { runner = r }

type step struct {
	run  [][]string
	undo [][]string
}

// Configure applies MTU, addresses, and routes, rolling back on failure.
func Configure(name string, mtu int, addresses, routes []netip.Prefix) error {
	steps, err := configureSteps(name, mtu, addresses, routes)
	if err != nil {
		return err
	}
	var done []step
	for _, s := range steps {
		for _, args := range s.run {
			if _, err := runner(args[0], args[1:]...); err != nil {
				undo(done)
				return err
			}
		}
		done = append(done, s)
	}
	return nil
}

// Remove deletes addresses and routes. Missing state is not an error.
func Remove(name string, addresses, routes []netip.Prefix) error {
	var firstErr error
	for _, s := range removalSteps(name, addresses, routes) {
		for _, args := range s {
			if _, err := runner(args[0], args[1:]...); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func undo(steps []step) {
	for i := len(steps) - 1; i >= 0; i-- {
		for j := len(steps[i].undo) - 1; j >= 0; j-- {
			args := steps[i].undo[j]
			_, _ = runner(args[0], args[1:]...)
		}
	}
}

// Conflicts reports existing routes that would shadow a Skein route.
func Conflicts(routes []netip.Prefix) ([]string, error) {
	existing, err := listRoutes()
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, want := range routes {
		for _, have := range existing {
			if have.prefix != want {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("route %s already exists on %s", want, have.device))
		}
	}
	return warnings, nil
}

// Reconcile removes routes attached to name that are inside the overlay
// prefixes but absent from desired.
func Reconcile(name string, overlay []netip.Prefix, desired []netip.Prefix) error {
	existing, err := listRoutes()
	if err != nil {
		return err
	}
	want := make(map[netip.Prefix]struct{}, len(desired))
	for _, p := range desired {
		want[p] = struct{}{}
	}
	var firstErr error
	for _, have := range existing {
		if have.device != name {
			continue
		}
		if _, ok := want[have.prefix]; ok {
			continue
		}
		inside := false
		for _, p := range overlay {
			if p.Overlaps(have.prefix) {
				inside = true
				break
			}
		}
		if !inside {
			continue
		}
		for _, args := range deleteRouteArgs(have.prefix, name) {
			if _, err := runner(args[0], args[1:]...); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// redundantRoute reports whether the kernel already installs route as the
// connected route of one of the configured addresses.
func redundantRoute(route netip.Prefix, addresses []netip.Prefix) bool {
	for _, addr := range addresses {
		if addr.Masked() == route {
			return true
		}
	}
	return false
}

type routeEntry struct {
	prefix netip.Prefix
	device string
}

// ErrUnsupported reports a platform that cannot perform the operation.
var ErrUnsupported = errors.New("netcfg: operation is not supported on this platform")
