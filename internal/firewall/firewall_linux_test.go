//go:build linux

package firewall

import (
	"errors"
	"strings"
	"testing"
)

type call struct {
	program string
	args    []string
}

type fakeRunner struct {
	calls    []call
	active   string
	fail     string
	iptables bool
	existing map[string]bool
}

func (f *fakeRunner) run(program string, args ...string) (string, error) {
	f.calls = append(f.calls, call{program: program, args: args})
	line := program + " " + strings.Join(args, " ")
	if f.fail != "" && strings.HasPrefix(line, f.fail) {
		return "", errors.New("boom")
	}
	switch program {
	case "iptables", "ip6tables":
		if len(args) == 1 && args[0] == "--version" {
			if f.iptables {
				return "iptables v1.8.9", nil
			}
			return "", errors.New("not found")
		}
		if !f.iptables {
			return "", errors.New("not found")
		}
		if len(args) > 0 && args[0] == "-C" {
			if f.existing[line] {
				return "", nil
			}
			return "", errors.New("no rule")
		}
		return "", nil
	}
	switch line {
	case "ufw status":
		if f.active == "ufw" {
			return "Status: active", nil
		}
		return "Status: inactive", nil
	case "firewall-cmd --state":
		if f.active == "firewalld" {
			return "running", nil
		}
		return "", errors.New("not running")
	}
	return "", nil
}

func (f *fakeRunner) lines() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.program + " " + strings.Join(c.args, " ")
	}
	return out
}

func withFake(t *testing.T, active string) *fakeRunner {
	t.Helper()
	f := &fakeRunner{active: active, existing: map[string]bool{}}
	old := runner
	SetRunner(f.run)
	t.Cleanup(func() { SetRunner(old) })
	return f
}

func withIPTables(t *testing.T, existing ...string) *fakeRunner {
	t.Helper()
	f := &fakeRunner{iptables: true, existing: map[string]bool{}}
	for _, line := range existing {
		f.existing[line] = true
	}
	old := runner
	SetRunner(f.run)
	t.Cleanup(func() { SetRunner(old) })
	return f
}

func TestEnsureUsesUFW(t *testing.T) {
	f := withFake(t, "ufw")
	if err := Ensure(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	for _, want := range []string{"ufw allow 11010/udp", "ufw allow in on skein0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestEnsureUsesFirewalld(t *testing.T) {
	f := withFake(t, "firewalld")
	if err := Ensure(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	for _, want := range []string{
		"firewall-cmd --permanent --add-port=11010/udp",
		"firewall-cmd --permanent --zone=trusted --add-interface=skein0",
		"firewall-cmd --reload",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestEnsureDoesNothingWithoutAFirewall(t *testing.T) {
	f := withFake(t, "")
	if err := Ensure(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, line := range f.lines() {
		if strings.Contains(line, "allow") || strings.Contains(line, "--add") {
			t.Fatalf("unexpected firewall change: %v", f.lines())
		}
	}
}

func TestEnsurePortOnlyOpensThePort(t *testing.T) {
	f := withFake(t, "ufw")
	if err := EnsurePort(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("EnsurePort: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	if !strings.Contains(got, "ufw allow 11010/udp") {
		t.Fatalf("missing port rule:\n%s", got)
	}
	if strings.Contains(got, "allow in on") {
		t.Fatalf("port-only call touched the interface:\n%s", got)
	}
}

func TestEnsureInterfaceOnlyTrustsTheInterface(t *testing.T) {
	f := withFake(t, "ufw")
	if err := EnsureInterface(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("EnsureInterface: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	if !strings.Contains(got, "ufw allow in on skein0") {
		t.Fatalf("missing interface rule:\n%s", got)
	}
	if strings.Contains(got, "11010/udp") {
		t.Fatalf("interface-only call opened the port:\n%s", got)
	}
}

func TestRemoveDeletesUFWRules(t *testing.T) {
	f := withFake(t, "ufw")
	if err := Remove(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	for _, want := range []string{"ufw delete allow 11010/udp", "ufw delete allow in on skein0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestEnsureReportsFailures(t *testing.T) {
	f := withFake(t, "ufw")
	f.fail = "ufw allow 11010/udp"
	if err := Ensure(Config{Interface: "skein0", Port: 11010}); err == nil {
		t.Fatal("expected the failure to be reported")
	}
}

func TestEnsureFallsBackToIPTables(t *testing.T) {
	f := withIPTables(t)
	if err := Ensure(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	for _, want := range []string{
		"iptables -C INPUT -i skein0 -j ACCEPT",
		"iptables -I INPUT 1 -i skein0 -j ACCEPT",
		"iptables -I OUTPUT 1 -o skein0 -j ACCEPT",
		"iptables -I FORWARD 1 -i skein0 -j ACCEPT",
		"iptables -I INPUT 1 -p udp --dport 11010 -j ACCEPT",
		"ip6tables -I INPUT 1 -i skein0 -j ACCEPT",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestIPTablesRulesAreIdempotent(t *testing.T) {
	existing := []string{
		"iptables -C INPUT -i skein0 -j ACCEPT",
		"iptables -C OUTPUT -o skein0 -j ACCEPT",
		"iptables -C FORWARD -i skein0 -j ACCEPT",
		"iptables -C FORWARD -o skein0 -j ACCEPT",
		"iptables -C INPUT -p udp --dport 11010 -j ACCEPT",
		"ip6tables -C INPUT -i skein0 -j ACCEPT",
		"ip6tables -C OUTPUT -o skein0 -j ACCEPT",
		"ip6tables -C FORWARD -i skein0 -j ACCEPT",
		"ip6tables -C FORWARD -o skein0 -j ACCEPT",
		"ip6tables -C INPUT -p udp --dport 11010 -j ACCEPT",
	}
	f := withIPTables(t, existing...)
	if err := Ensure(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, line := range f.lines() {
		if strings.Contains(line, " -I ") {
			t.Fatalf("re-inserted an existing rule: %s", line)
		}
	}
}

func TestRemoveIPTables(t *testing.T) {
	f := withIPTables(t)
	if err := Remove(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got := strings.Join(f.lines(), "\n")
	for _, want := range []string{
		"iptables -D INPUT -i skein0 -j ACCEPT",
		"ip6tables -D INPUT -p udp --dport 11010 -j ACCEPT",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestClassifyInterfaceError(t *testing.T) {
	err := classifyInterfaceError(errors.New("New-NetFirewallRule : The specified interface was not found on the system."))
	if !errors.Is(err, ErrInterfaceMissing) {
		t.Fatalf("missing interface error was not classified: %v", err)
	}
	other := classifyInterfaceError(errors.New("access denied"))
	if errors.Is(other, ErrInterfaceMissing) {
		t.Fatal("access denied was classified as a missing interface")
	}
}
