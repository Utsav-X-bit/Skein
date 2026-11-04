//go:build !windows

package services

import "testing"

// Step 11: every retained helper verb that elevates must map to a
// shipped polkit action, and no removed verb may gain one. A verb
// that elevates without an action would silently fall back to
// pkexec's generic prompt (Agent C L-16); a removed verb with an
// action would bless a dead verb in the policy.
func TestPolkitActionForVerb(t *testing.T) {
	want := map[string]string{
		"install":     "org.skein.helper.install",
		"bootstrap":   "org.skein.helper.install",
		"start":       "org.skein.helper.lifecycle",
		"stop":        "org.skein.helper.lifecycle",
		"restart":     "org.skein.helper.lifecycle",
		"uninstall":   "org.skein.helper.lifecycle",
		"grant-user":  "org.skein.helper.grants",
		"set-owner":   "org.skein.helper.grants",
		"revoke-user": "org.skein.helper.grants",
	}
	for verb, action := range want {
		if got := polkitActionForVerb(verb); got != action {
			t.Errorf("polkitActionForVerb(%q) = %q, want %q", verb, got, action)
		}
	}
	for _, verb := range []string{"status", "init", "join", "import", "rotate-secret", "reset", "update-daemon", ""} {
		if got := polkitActionForVerb(verb); got != "" {
			t.Errorf("polkitActionForVerb(%q) = %q, want empty", verb, got)
		}
	}
}
