//go:build windows

package firewall

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Rule names are stable and unique per direction; every add is checked with
// `show rule` first, so exactly one set of rules exists after any number of
// repairs. All commands are netsh argv — no PowerShell string interpolation.
const (
	underlayRule     = "Skein underlay"
	overlayRule      = "Skein overlay"
	overlayOutRule   = "Skein overlay out"
	overlayV6Rule    = "Skein overlay v6"
	overlayOutV6Rule = "Skein overlay out v6"
	legacyRule       = "skein"
)

type winRule struct {
	name   string
	binary string
	args   []string
}

// windowsRules is the fixed set of rules Skein manages. netsh cannot scope a
// rule to a named interface, so the overlay rules use New-NetFirewallRule
// through an encoded PowerShell command; values are single-quote escaped.
func windowsRules(cfg Config) []winRule {
	v4 := cfg.PrefixV4
	if v4 == "" {
		v4 = "10.144.144.0/24"
	}
	v4 = strings.ReplaceAll(v4, "'", "")
	rules := []winRule{
		{underlayRule, "netsh", []string{"advfirewall", "firewall", "add", "rule",
			"name=" + underlayRule, "dir=in", "action=allow", "protocol=UDP",
			fmt.Sprintf("localport=%d", cfg.Port), "profile=any"}},
		{overlayRule, "powershell", powershellArgs(fmt.Sprintf(
			"New-NetFirewallRule -DisplayName '%s' -Direction Inbound -Action Allow -RemoteAddress '%s' -Profile Any -ErrorAction Stop | Out-Null",
			overlayRule, v4))},
		{overlayOutRule, "powershell", powershellArgs(fmt.Sprintf(
			"New-NetFirewallRule -DisplayName '%s' -Direction Outbound -Action Allow -RemoteAddress '%s' -Profile Any -ErrorAction Stop | Out-Null",
			overlayOutRule, v4))},
	}
	if v6 := strings.ReplaceAll(cfg.PrefixV6, "'", ""); v6 != "" {
		rules = append(rules,
			winRule{overlayV6Rule, "powershell", powershellArgs(fmt.Sprintf(
				"New-NetFirewallRule -DisplayName '%s' -Direction Inbound -Action Allow -RemoteAddress '%s' -Profile Any -ErrorAction Stop | Out-Null",
				overlayV6Rule, v6))},
			winRule{overlayOutV6Rule, "powershell", powershellArgs(fmt.Sprintf(
				"New-NetFirewallRule -DisplayName '%s' -Direction Outbound -Action Allow -RemoteAddress '%s' -Profile Any -ErrorAction Stop | Out-Null",
				overlayOutV6Rule, v6))},
		)
	}
	return rules
}

// powershellArgs builds a -EncodedCommand invocation so a config interface
// name cannot inject a PowerShell command.
func powershellArgs(script string) []string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], unit)
	}
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(raw)}
}

func expectedRules(cfg Config) []Rule {
	return []Rule{
		{Name: "underlay", Direction: "in", Action: "allow", Protocol: "udp", Port: strconv.Itoa(int(cfg.Port))},
		{Name: "overlay-in", Direction: "in", Action: "allow", Interface: cfg.Interface},
		{Name: "overlay-out", Direction: "out", Action: "allow", Interface: cfg.Interface},
	}
}

// ruleActive reports whether a rule with this name exists.
func ruleActive(name string) bool {
	out, err := runner("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
	if err != nil {
		return false
	}
	return !strings.Contains(out, "No rule matches")
}

// deleteRule removes every rule with this display name, including duplicates.
func deleteRule(name string) {
	for i := 0; i < 16; i++ {
		if !ruleActive(name) {
			return
		}
		if _, err := runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name); err != nil {
			return
		}
	}
}

func status(cfg Config) (Status, error) {
	st := Status{Manager: "windows firewall", LastCheckMS: time.Now().UnixMilli(), Rules: expectedRules(cfg)}
	missing := 0
	for _, r := range windowsRules(cfg) {
		if !ruleActive(r.name) {
			missing++
		}
	}
	switch {
	case missing > 0:
		st.State = "degraded"
		st.LastError = fmt.Sprintf("%d of %d Skein rules missing; run repair", missing, len(windowsRules(cfg)))
	case ruleActive(legacyRule):
		st.State = "degraded"
		st.LastError = "legacy duplicate rule " + legacyRule + " present; run repair"
	default:
		st.State = "ok"
	}
	return st, nil
}

// repair is transactional: it adds before it removes and deletes exactly the
// rules this run added if a later step fails.
func repair(cfg Config) (Status, error) {
	var added []string
	failed := func(err error) (Status, error) {
		for _, name := range added {
			_, _ = runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
		}
		st, _ := status(cfg)
		st.LastError = err.Error()
		return st, nil
	}
	for _, r := range windowsRules(cfg) {
		// Interface-scoped rules store the adapter GUID. Recreate them on
		// every daemon start so a recreated TUN adapter is matched.
		deleteRule(r.name)
		if _, err := runner(r.binary, r.args...); err != nil {
			return failed(err)
		}
		added = append(added, r.name)
	}
	// The full set is in place now; only then remove the legacy duplicate.
	_, _ = runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+legacyRule)
	return status(cfg)
}

func ensure(cfg Config) error {
	st, _ := repair(cfg)
	if st.State != "ok" {
		return fmt.Errorf("windows firewall: %s", st.LastError)
	}
	return nil
}

// ensurePort allows inbound UDP on the underlay port for IPv4 and IPv6; a
// Windows Firewall rule without localip/remoteip covers both families.
func ensurePort(cfg Config) error {
	r := windowsRules(cfg)[0]
	if ruleActive(r.name) {
		return nil
	}
	_, err := runner(r.binary, r.args...)
	return err
}

// ensureInterface allows every protocol on the overlay interface in both
// directions.
func ensureInterface(cfg Config) error {
	for _, r := range windowsRules(cfg)[1:] {
		if ruleActive(r.name) {
			continue
		}
		if _, err := runner(r.binary, r.args...); err != nil {
			return classifyInterfaceError(err)
		}
	}
	return nil
}

func remove(cfg Config) error {
	for _, name := range []string{underlayRule, overlayRule, overlayOutRule, overlayV6Rule, overlayOutV6Rule, legacyRule} {
		deleteRule(name)
	}
	return nil
}

// Manager reports the Windows Firewall backend.
func Manager() string { return "windows firewall" }
