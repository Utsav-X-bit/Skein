//go:build linux

package firewall

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ensure(cfg Config) error {
	if err := ensurePort(cfg); err != nil {
		return err
	}
	return ensureInterface(cfg)
}

// Manager reports the firewall backend in use: ufw, firewalld, iptables, or
// empty when none is available.
func Manager() string {
	if ufwActive() {
		return "ufw"
	}
	if firewalldActive() {
		return "firewalld"
	}
	if toolAvailable("iptables") {
		return "iptables"
	}
	return ""
}

// ufwRules is the fixed set of ufw commands Skein manages, in the exact
// form `ufw show added` reports them.
func ufwRules(cfg Config) []struct {
	name string
	args []string
} {
	return []struct {
		name string
		args []string
	}{
		{"underlay", []string{"allow", fmt.Sprintf("%d/udp", cfg.Port)}},
		{"overlay-in", []string{"allow", "in", "on", cfg.Interface}},
		{"overlay-out", []string{"allow", "out", "on", cfg.Interface}},
		{"forward-in", []string{"route", "allow", "in", "on", cfg.Interface}},
		{"forward-out", []string{"route", "allow", "out", "on", cfg.Interface}},
	}
}

// iptablesRules is the raw-iptables set: port plus interface rules in both
// directions, plus FORWARD so a relay can pass overlay traffic.
func iptablesRules(cfg Config) []struct {
	chain string
	spec  []string
} {
	return []struct {
		chain string
		spec  []string
	}{
		{"INPUT", []string{"-p", "udp", "--dport", strconv.Itoa(int(cfg.Port)), "-j", "ACCEPT"}},
		{"INPUT", []string{"-i", cfg.Interface, "-j", "ACCEPT"}},
		{"OUTPUT", []string{"-o", cfg.Interface, "-j", "ACCEPT"}},
		{"FORWARD", []string{"-i", cfg.Interface, "-j", "ACCEPT"}},
		{"FORWARD", []string{"-o", cfg.Interface, "-j", "ACCEPT"}},
	}
}

// ensurePort opens the underlay port for IPv4 and IPv6.
func ensurePort(cfg Config) error {
	switch Manager() {
	case "ufw":
		_, err := runner("ufw", "allow", fmt.Sprintf("%d/udp", cfg.Port))
		return err
	case "firewalld":
		if _, err := runner("firewall-cmd", "--permanent", fmt.Sprintf("--add-port=%d/udp", cfg.Port)); err != nil {
			return err
		}
		_, err := runner("firewall-cmd", "--reload")
		return err
	case "iptables":
		spec := []string{"-p", "udp", "--dport", strconv.Itoa(int(cfg.Port)), "-j", "ACCEPT"}
		for _, binary := range iptablesBinaries() {
			if err := iptablesEnsure(binary, "INPUT", spec); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureInterface trusts every protocol on the overlay interface, in both
// directions, and forwards so relay traffic passes.
func ensureInterface(cfg Config) error {
	switch Manager() {
	case "ufw":
		for _, r := range ufwRules(cfg)[1:] {
			if _, err := runner("ufw", r.args...); err != nil {
				return err
			}
		}
		return nil
	case "firewalld":
		if _, err := runner("firewall-cmd", "--permanent", "--zone=trusted", fmt.Sprintf("--add-interface=%s", cfg.Interface)); err != nil {
			return err
		}
		_, err := runner("firewall-cmd", "--reload")
		return err
	case "iptables":
		for _, r := range iptablesRules(cfg)[1:] {
			for _, binary := range iptablesBinaries() {
				if err := iptablesEnsure(binary, r.chain, r.spec); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func remove(cfg Config) error {
	// Step 7 (Agent C reset-orphans-rules fix): removal errors
	// are no longer discarded wholesale. A rule that is already
	// absent is not an error (its runner output says so); any
	// other failure is returned, so the daemon's reset endpoint
	// can report it in FirewallError instead of claiming a
	// clean reset while rules remain.
	var errs []string
	keep := func(err error) {
		if err == nil {
			return
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "could not find") || strings.Contains(msg, "not found") {
			return
		}
		errs = append(errs, err.Error())
	}
	switch Manager() {
	case "ufw":
		// --force first: without it, `ufw delete` prompts
		// interactively, and a daemon-side remove can hang
		// instead of removing the rule.
		for _, r := range ufwRules(cfg) {
			_, err := runner("ufw", append([]string{"--force", "delete"}, r.args...)...)
			keep(err)
		}
	case "firewalld":
		_, err := runner("firewall-cmd", "--permanent", fmt.Sprintf("--remove-port=%d/udp", cfg.Port))
		keep(err)
		_, err = runner("firewall-cmd", "--permanent", "--zone=trusted", fmt.Sprintf("--remove-interface=%s", cfg.Interface))
		keep(err)
		_, err = runner("firewall-cmd", "--reload")
		keep(err)
	case "iptables":
		iptablesRemove(cfg)
	}
	if len(errs) > 0 {
		return fmt.Errorf("firewall: remove: %s", strings.Join(errs, "; "))
	}
	return nil
}

// expectedRules is the rule set the status and repair verbs manage.
func expectedRules(cfg Config) []Rule {
	return []Rule{
		{Name: "underlay", Direction: "in", Action: "allow", Protocol: "udp", Port: strconv.Itoa(int(cfg.Port))},
		{Name: "overlay-in", Direction: "in", Action: "allow", Interface: cfg.Interface},
		{Name: "overlay-out", Direction: "out", Action: "allow", Interface: cfg.Interface},
		{Name: "forward-in", Direction: "forward", Action: "allow", Interface: cfg.Interface},
		{Name: "forward-out", Direction: "forward", Action: "allow", Interface: cfg.Interface},
	}
}

// status inspects the manager without changing it.
func status(cfg Config) (Status, error) {
	st := Status{Manager: Manager(), LastCheckMS: time.Now().UnixMilli(), Rules: expectedRules(cfg)}
	switch st.Manager {
	case "ufw":
		out, err := runner("ufw", "show", "added")
		if err != nil {
			st.State = "unknown"
			st.LastError = err.Error()
			return st, nil
		}
		missing := 0
		for _, r := range ufwRules(cfg) {
			if !strings.Contains(out, "ufw "+strings.Join(r.args, " ")) {
				missing++
			}
		}
		st.State = stateFor(missing)
	case "firewalld":
		missing := 0
		if _, err := runner("firewall-cmd", "--permanent", fmt.Sprintf("--query-port=%d/udp", cfg.Port)); err != nil {
			missing++
		}
		if _, err := runner("firewall-cmd", "--permanent", "--zone=trusted", fmt.Sprintf("--query-interface=%s", cfg.Interface)); err != nil {
			// The trusted zone carries in, out, and forward at once.
			missing += 4
		}
		st.State = stateFor(missing)
	case "iptables":
		missing := 0
		binaries := iptablesBinaries()
		for _, r := range iptablesRules(cfg) {
			for _, binary := range binaries {
				if _, err := runner(binary, append([]string{"-C", r.chain}, r.spec...)...); err != nil {
					missing++
					break
				}
			}
		}
		st.State = stateFor(missing)
	default:
		st.State = "unknown"
		st.LastError = "no firewall manager (ufw, firewalld, iptables) found"
	}
	return st, nil
}

func stateFor(missing int) string {
	if missing <= 0 {
		return "ok"
	}
	return "degraded"
}

// repair brings the rules to the expected state: add before remove, unique
// names, and rollback of this run's additions on failure.
func repair(cfg Config) (Status, error) {
	switch Manager() {
	case "ufw":
		return ufwRepair(cfg)
	case "firewalld":
		return firewalldRepair(cfg)
	case "iptables":
		return iptablesRepair(cfg)
	}
	st, _ := status(cfg)
	return st, nil
}

func ufwRepair(cfg Config) (Status, error) {
	out, _ := runner("ufw", "show", "added")
	var added []string
	for _, r := range ufwRules(cfg) {
		if strings.Contains(out, "ufw "+strings.Join(r.args, " ")) {
			continue
		}
		if _, err := runner("ufw", r.args...); err != nil {
			rollbackUFW(added)
			return degraded(cfg, fmt.Errorf("ufw %s: %w", strings.Join(r.args, " "), err)), nil
		}
		added = append(added, strings.Join(r.args, " "))
	}
	return status(cfg)
}

// rollbackUFW removes exactly the rules this repair run added.
func rollbackUFW(added []string) {
	for _, a := range added {
		_, _ = runner("ufw", append([]string{"delete"}, strings.Split(a, " ")...)...)
	}
}

func firewalldRepair(cfg Config) (Status, error) {
	var addedPort, addedIface bool
	if _, err := runner("firewall-cmd", "--permanent", fmt.Sprintf("--query-port=%d/udp", cfg.Port)); err != nil {
		if _, err := runner("firewall-cmd", "--permanent", fmt.Sprintf("--add-port=%d/udp", cfg.Port)); err != nil {
			return degraded(cfg, err), nil
		}
		addedPort = true
	}
	if _, err := runner("firewall-cmd", "--permanent", "--zone=trusted", fmt.Sprintf("--query-interface=%s", cfg.Interface)); err != nil {
		if _, err := runner("firewall-cmd", "--permanent", "--zone=trusted", fmt.Sprintf("--add-interface=%s", cfg.Interface)); err != nil {
			if addedPort {
				_, _ = runner("firewall-cmd", "--permanent", fmt.Sprintf("--remove-port=%d/udp", cfg.Port))
			}
			return degraded(cfg, err), nil
		}
		addedIface = true
	}
	if addedPort || addedIface {
		if _, err := runner("firewall-cmd", "--reload"); err != nil {
			if addedPort {
				_, _ = runner("firewall-cmd", "--permanent", fmt.Sprintf("--remove-port=%d/udp", cfg.Port))
			}
			if addedIface {
				_, _ = runner("firewall-cmd", "--permanent", "--zone=trusted", fmt.Sprintf("--remove-interface=%s", cfg.Interface))
			}
			return degraded(cfg, err), nil
		}
	}
	return status(cfg)
}

func iptablesRepair(cfg Config) (Status, error) {
	var added []struct {
		binary string
		chain  string
		spec   []string
	}
	for _, binary := range iptablesBinaries() {
		for _, r := range iptablesRules(cfg) {
			if _, err := runner(binary, append([]string{"-C", r.chain}, r.spec...)...); err == nil {
				continue
			}
			if _, err := runner(binary, append([]string{"-I", r.chain, "1"}, r.spec...)...); err != nil {
				rollbackIptables(added)
				return degraded(cfg, fmt.Errorf("%s %s %s: %w", binary, r.chain, strings.Join(r.spec, " "), err)), nil
			}
			added = append(added, struct {
				binary string
				chain  string
				spec   []string
			}{binary, r.chain, r.spec})
		}
	}
	return status(cfg)
}

func rollbackIptables(added []struct {
	binary string
	chain  string
	spec   []string
}) {
	for _, a := range added {
		_, _ = runner(a.binary, append([]string{"-D", a.chain}, a.spec...)...)
	}
}

func degraded(cfg Config, err error) Status {
	st, _ := status(cfg)
	st.State = "degraded"
	st.LastError = err.Error()
	return st
}

func ufwActive() bool {
	out, err := runner("ufw", "status")
	return err == nil && strings.Contains(out, "Status: active")
}

func firewalldActive() bool {
	out, err := runner("firewall-cmd", "--state")
	return err == nil && strings.Contains(out, "running")
}

func toolAvailable(name string) bool {
	_, err := runner(name, "--version")
	return err == nil
}

func iptablesRemove(cfg Config) {
	for _, binary := range iptablesBinaries() {
		for _, r := range iptablesRules(cfg) {
			_, _ = runner(binary, append([]string{"-D", r.chain}, r.spec...)...)
		}
	}
}

// iptablesEnsure appends the rule once: an existing rule (-C succeeds) is left
// alone, otherwise it is inserted at the top of the chain.
func iptablesEnsure(binary, chain string, spec []string) error {
	check := append([]string{"-C", chain}, spec...)
	if _, err := runner(binary, check...); err == nil {
		return nil
	}
	insert := append([]string{"-I", chain, "1"}, spec...)
	_, err := runner(binary, insert...)
	return err
}

func iptablesBinaries() []string {
	out := []string{}
	for _, binary := range []string{"iptables", "ip6tables"} {
		if toolAvailable(binary) {
			out = append(out, binary)
		}
	}
	return out
}
