//go:build linux

package firewall

import (
	"fmt"
	"strconv"
	"strings"
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
		return iptablesPort(cfg.Port)
	}
	return nil
}

// ensureInterface trusts every protocol on the overlay interface, in both
// directions.
func ensureInterface(cfg Config) error {
	switch Manager() {
	case "ufw":
		if _, err := runner("ufw", "allow", "in", "on", cfg.Interface); err != nil {
			return err
		}
		_, err := runner("ufw", "allow", "out", "on", cfg.Interface)
		return err
	case "firewalld":
		if _, err := runner("firewall-cmd", "--permanent", "--zone=trusted", "--add-interface="+cfg.Interface); err != nil {
			return err
		}
		_, err := runner("firewall-cmd", "--reload")
		return err
	case "iptables":
		return iptablesInterface(cfg.Interface)
	}
	return nil
}

func remove(cfg Config) error {
	switch Manager() {
	case "ufw":
		_, _ = runner("ufw", "delete", "allow", fmt.Sprintf("%d/udp", cfg.Port))
		_, _ = runner("ufw", "delete", "allow", "in", "on", cfg.Interface)
		_, _ = runner("ufw", "delete", "allow", "out", "on", cfg.Interface)
	case "firewalld":
		_, _ = runner("firewall-cmd", "--permanent", fmt.Sprintf("--remove-port=%d/udp", cfg.Port))
		_, _ = runner("firewall-cmd", "--permanent", "--zone=trusted", "--remove-interface="+cfg.Interface)
		_, _ = runner("firewall-cmd", "--reload")
	case "iptables":
		iptablesRemove(cfg)
	}
	return nil
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

func iptablesPort(port uint16) error {
	spec := []string{"-p", "udp", "--dport", strconv.Itoa(int(port)), "-j", "ACCEPT"}
	for _, binary := range iptablesBinaries() {
		if err := iptablesEnsure(binary, "INPUT", spec); err != nil {
			return err
		}
	}
	return nil
}

func iptablesInterface(iface string) error {
	rules := []struct {
		chain string
		spec  []string
	}{
		{"INPUT", []string{"-i", iface, "-j", "ACCEPT"}},
		{"OUTPUT", []string{"-o", iface, "-j", "ACCEPT"}},
		{"FORWARD", []string{"-i", iface, "-j", "ACCEPT"}},
		{"FORWARD", []string{"-o", iface, "-j", "ACCEPT"}},
	}
	for _, binary := range iptablesBinaries() {
		for _, rule := range rules {
			if err := iptablesEnsure(binary, rule.chain, rule.spec); err != nil {
				return err
			}
		}
	}
	return nil
}

func iptablesRemove(cfg Config) {
	interfaceRules := []struct {
		chain string
		spec  []string
	}{
		{"INPUT", []string{"-i", cfg.Interface, "-j", "ACCEPT"}},
		{"OUTPUT", []string{"-o", cfg.Interface, "-j", "ACCEPT"}},
		{"FORWARD", []string{"-i", cfg.Interface, "-j", "ACCEPT"}},
		{"FORWARD", []string{"-o", cfg.Interface, "-j", "ACCEPT"}},
		{"INPUT", []string{"-p", "udp", "--dport", strconv.Itoa(int(cfg.Port)), "-j", "ACCEPT"}},
	}
	for _, binary := range iptablesBinaries() {
		for _, rule := range interfaceRules {
			args := append([]string{"-D", rule.chain}, rule.spec...)
			_, _ = runner(binary, args...)
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
