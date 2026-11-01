//go:build windows

package firewall

import (
	"fmt"
	"strings"
)

const (
	underlayRule = "Skein underlay"
	overlayRule  = "Skein overlay"
)

func ensure(cfg Config) error {
	if !ruleExists(underlayRule) {
		if _, err := runner("netsh", "advfirewall", "firewall", "add", "rule",
			"name="+underlayRule, "dir=in", "action=allow", "protocol=UDP",
			fmt.Sprintf("localport=%d", cfg.Port), "profile=any"); err != nil {
			return err
		}
	}
	if !ruleExists(overlayRule) {
		script := fmt.Sprintf(
			`New-NetFirewallRule -DisplayName "%s" -Direction Inbound -InterfaceAlias "%s" -Action Allow -Profile Any -ErrorAction Stop | Out-Null`,
			overlayRule, cfg.Interface)
		if _, err := runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
			return err
		}
	}
	return nil
}

func remove(cfg Config) error {
	_, _ = runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+underlayRule)
	script := fmt.Sprintf(`Remove-NetFirewallRule -DisplayName "%s" -ErrorAction SilentlyContinue | Out-Null`, overlayRule)
	_, _ = runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	return nil
}

func ruleExists(name string) bool {
	out, err := runner("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
	return err == nil && strings.Contains(strings.ToLower(out), "rule:")
}
