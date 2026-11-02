//go:build windows

package firewall

import (
	"fmt"
	"strings"
)

const (
	underlayRule   = "Skein underlay"
	overlayRule    = "Skein overlay"
	overlayOutRule = "Skein overlay out"
)

func ensure(cfg Config) error {
	if err := ensurePort(cfg); err != nil {
		return err
	}
	return ensureInterface(cfg)
}

// ensurePort allows inbound UDP on the underlay port for IPv4 and IPv6; a
// Windows Firewall rule without localip/remoteip covers both families.
func ensurePort(cfg Config) error {
	if !ruleExists(underlayRule) {
		if _, err := runner("netsh", "advfirewall", "firewall", "add", "rule",
			"name="+underlayRule, "dir=in", "action=allow", "protocol=UDP",
			fmt.Sprintf("localport=%d", cfg.Port), "profile=any"); err != nil {
			return err
		}
	}
	return nil
}

// ensureInterface allows every protocol on the overlay interface in both
// directions.
func ensureInterface(cfg Config) error {
	if !ruleExists(overlayRule) {
		script := fmt.Sprintf(
			`New-NetFirewallRule -DisplayName "%s" -Direction Inbound -InterfaceAlias "%s" -Action Allow -Profile Any -ErrorAction Stop | Out-Null`,
			overlayRule, cfg.Interface)
		if _, err := runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
			return err
		}
	}
	if !ruleExists(overlayOutRule) {
		script := fmt.Sprintf(
			`New-NetFirewallRule -DisplayName "%s" -Direction Outbound -InterfaceAlias "%s" -Action Allow -Profile Any -ErrorAction Stop | Out-Null`,
			overlayOutRule, cfg.Interface)
		if _, err := runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
			return err
		}
	}
	return nil
}

// Manager reports the Windows Firewall backend.
func Manager() string { return "windows firewall" }

func remove(cfg Config) error {
	_, _ = runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+underlayRule)
	for _, name := range []string{overlayRule, overlayOutRule} {
		script := fmt.Sprintf(`Remove-NetFirewallRule -DisplayName "%s" -ErrorAction SilentlyContinue | Out-Null`, name)
		_, _ = runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	return nil
}

func ruleExists(name string) bool {
	out, err := runner("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
	return err == nil && strings.Contains(strings.ToLower(out), "rule:")
}
