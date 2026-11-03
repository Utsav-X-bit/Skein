//go:build windows

package firewall

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Rule names are stable and unique per direction; every add is checked with
// `show rule` first, so exactly one set of rules exists after any number of
// repairs. All commands are netsh argv — no PowerShell string interpolation.
const (
	underlayRule   = "Skein underlay"
	overlayRule    = "Skein overlay"
	overlayOutRule = "Skein overlay out"
	legacyRule     = "skein"
)

// windowsRules is the fixed set of netsh "add rule" command lines Skein
// manages.
func windowsRules(cfg Config) []struct {
	name string
	args []string
} {
	return []struct {
		name string
		args []string
	}{
		{underlayRule, []string{"advfirewall", "firewall", "add", "rule",
			"name=" + underlayRule, "dir=in", "action=allow", "protocol=UDP",
			fmt.Sprintf("localport=%d", cfg.Port), "profile=any"}},
		{overlayRule, []string{"advfirewall", "firewall", "add", "rule",
			"name=" + overlayRule, "dir=in", "action=allow",
			fmt.Sprintf("interface=%s", cfg.Interface), "profile=any"}},
		{overlayOutRule, []string{"advfirewall", "firewall", "add", "rule",
			"name=" + overlayOutRule, "dir=out", "action=allow",
			fmt.Sprintf("interface=%s", cfg.Interface), "profile=any"}},
	}
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
		if ruleActive(r.name) {
			continue
		}
		if _, err := runner("netsh", r.args...); err != nil {
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
	_, err := runner("netsh", r.args...)
	return err
}

// ensureInterface allows every protocol on the overlay interface in both
// directions.
func ensureInterface(cfg Config) error {
	for _, r := range windowsRules(cfg)[1:] {
		if ruleActive(r.name) {
			continue
		}
		if _, err := runner("netsh", r.args...); err != nil {
			return classifyInterfaceError(err)
		}
	}
	return nil
}

func remove(cfg Config) error {
	for _, name := range []string{underlayRule, overlayRule, overlayOutRule, legacyRule} {
		// netsh deletes every rule with that name, including duplicates.
		_, _ = runner("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
	}
	return nil
}

// Manager reports the Windows Firewall backend.
func Manager() string { return "windows firewall" }
