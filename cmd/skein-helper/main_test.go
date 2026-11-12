package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

var retainedVerbs = []string{
	"bootstrap", "install", "uninstall", "start", "stop", "restart",
	"enable", "disable", "status", "grant-user", "revoke-user", "set-owner",
}

var removedVerbEndpoints = map[string]string{
	"init":          "/api/v1/bootstrap/init",
	"join":          "/api/v1/bootstrap/join",
	"import":        "/api/v1/bootstrap/import",
	"rotate-secret": "/api/v1/system/rotate-secret",
	"reset":         "/api/v1/system/reset",
	"update-daemon": "/api/v1/system/update/apply",
}

// verbLines returns the verbs named by a help section's verb lines:
// lines indented by exactly two spaces (description lines are indented
// further and are ignored). A verb line names one verb in its first
// field, except the "start | stop | restart" line, whose segments
// each name one verb. Only first fields count, so a removed verb
// mentioned in a description or an endpoint (e.g. "bootstrap" in a
// removed line's replacement text) does not pollute the set.
func verbLines(section string) map[string]bool {
	found := map[string]bool{}
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, " | ") {
			for _, seg := range strings.Split(trimmed, "|") {
				if f := strings.Fields(seg); len(f) > 0 {
					found[f[0]] = true
				}
			}
			continue
		}
		if f := strings.Fields(trimmed); len(f) > 0 {
			found[f[0]] = true
		}
	}
	return found
}

// TestHelpListsRetainedAndRemoved is the Step 10 --help test (a test,
// not a grep): the retained list must be exactly the minimal helper,
// and the removed list must name each removed verb with its real
// daemon endpoint.
func TestHelpListsRetainedAndRemoved(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"--help"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("--help exit = %d, want 0", code)
	}
	help := out.String()
	parts := strings.SplitN(help, "Removed verbs", 2)
	if len(parts) != 2 {
		t.Fatal("--help has no Removed verbs section")
	}
	retained, removed := verbLines(parts[0]), verbLines(parts[1])
	for _, verb := range retainedVerbs {
		if !retained[verb] {
			t.Errorf("retained verb %q missing from --help retained section", verb)
		}
		if removed[verb] {
			t.Errorf("retained verb %q also appears as a removed verb line", verb)
		}
	}
	for verb, endpoint := range removedVerbEndpoints {
		if !removed[verb] {
			t.Errorf("removed verb %q missing from --help removed section", verb)
		}
		if retained[verb] {
			t.Errorf("removed verb %q still listed as a retained verb line", verb)
		}
		// The removed section's line for this verb must carry its endpoint.
		found := false
		for _, line := range strings.Split(parts[1], "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), verb+" ") && strings.Contains(line, endpoint) {
				found = true
			}
		}
		if !found {
			t.Errorf("removed verb %q has no line pointing at %s", verb, endpoint)
		}
	}
	if _, ok := removedVerbs["restart"]; ok {
		t.Error("restart must stay retained (plan Step 10 removed list + the rotate/reset endpoint sequences use it); it is not a removed verb")
	}
}

// TestRemovedVerbsReturnEndpointPointer runs every removed verb and
// asserts exit 1 plus the exact endpoint pointer JSON. Removed verbs
// are answered before any privilege check, so this passes on non-root
// CI runners as well as root — and no removed verb touches the system.
func TestRemovedVerbsReturnEndpointPointer(t *testing.T) {
	for verb, endpoint := range removedVerbEndpoints {
		var out bytes.Buffer
		code := run([]string{verb}, &out, &bytes.Buffer{})
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1", verb, code)
			continue
		}
		var env struct {
			OK          bool   `json:"ok"`
			Error       string `json:"error"`
			Verb        string `json:"verb"`
			Remediation string `json:"remediation"`
		}
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Errorf("%s: output is not JSON: %v (%q)", verb, err, out.String())
			continue
		}
		if env.OK || !strings.Contains(env.Error, "verb removed") || env.Verb != verb || !strings.Contains(env.Remediation, endpoint) {
			t.Errorf("%s: envelope = %+v, want ok=false, verb removed, remediation containing %s", verb, env, endpoint)
		}
	}
}

func TestUnknownVerbStillExit2(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"frobnicate"}, &out, &bytes.Buffer{}); code != 2 {
		t.Fatalf("unknown verb exit = %d, want 2", code)
	}
}
