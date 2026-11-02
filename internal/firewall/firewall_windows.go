//go:build windows

package firewall

import "fmt"

const (
	underlayRule   = "Skein underlay"
	overlayRule    = "Skein overlay"
	overlayOutRule = "Skein overlay out"
	legacyRule     = "skein"
)

func ensure(cfg Config) error {
	// Older builds could not see their own rules and added a copy on every
	// start. Remove every Skein rule first so exactly one set remains.
	_ = remove(cfg)
	if err := ensurePort(cfg); err != nil {
		return err
	}
	return ensureInterface(cfg)
}

// ensurePort allows inbound UDP on the underlay port for IPv4 and IPv6; a
// Windows Firewall rule without localip/remoteip covers both families.
func ensurePort(cfg Config) error {
	_, err := runner("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+underlayRule, "dir=in", "action=allow", "protocol=UDP",
		fmt.Sprintf("localport=%d", cfg.Port), "profile=any")
	return err
}

// ensureInterface allows every protocol on the overlay interface in both
// directions. The port is intentionally any: these rules cover decapsulated
// overlay traffic (SSH, HTTP, ...), while the underlay rule above carries the
// tunnel itself on cfg.Port.
func ensureInterface(cfg Config) error {
	script := fmt.Sprintf(
		`New-NetFirewallRule -DisplayName "%s" -Direction Inbound -InterfaceAlias "%s" -Action Allow -Profile Any -ErrorAction Stop | Out-Null`,
		overlayRule, cfg.Interface)
	if _, err := runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
		return classifyInterfaceError(err)
	}
	script = fmt.Sprintf(
		`New-NetFirewallRule -DisplayName "%s" -Direction Outbound -InterfaceAlias "%s" -Action Allow -Profile Any -ErrorAction Stop | Out-Null`,
		overlayOutRule, cfg.Interface)
	if _, err := runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
		return classifyInterfaceError(err)
	}
	return nil
}

func remove(cfg Config) error {
	for _, name := range []string{underlayRule, overlayRule, overlayOutRule, legacyRule} {
		// netsh deletes every rule with that name, including duplicates.
		_, _ = runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
		script := fmt.Sprintf(`Remove-NetFirewallRule -DisplayName "%s" -ErrorAction SilentlyContinue | Out-Null`, name)
		_, _ = runner("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	}
	return nil
}

// Manager reports the Windows Firewall backend.
func Manager() string { return "windows firewall" }
