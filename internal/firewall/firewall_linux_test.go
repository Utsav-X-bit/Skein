//go:build linux

package firewall

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, want := range []string{"ufw --force delete allow 11010/udp", "ufw --force delete allow in on skein0"} {
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

// ufwState is a stateful fake firewall: it accepts only the add forms
// ufw really accepts and the two delete forms Skein is allowed to emit,
// translating a correct delete back to the add-form key. A wrong order
// (`ufw --force delete route ...`) is recorded as invalid and deletes
// nothing, so the regression cannot pass just by seeing the right words.
type ufwState struct {
	installed map[string]bool
	calls     []string
	invalid   []string
	failAdd   string
}

func newUFWState() *ufwState {
	return &ufwState{installed: map[string]bool{}}
}

func (u *ufwState) run(program string, args ...string) (string, error) {
	line := program + " " + strings.Join(args, " ")
	u.calls = append(u.calls, line)
	if program != "ufw" {
		return "", errors.New("unexpected program")
	}
	switch {
	case len(args) == 1 && args[0] == "status":
		return "Status: active", nil
	case len(args) == 2 && args[0] == "show" && args[1] == "added":
		var b strings.Builder
		for rule := range u.installed {
			b.WriteString(rule + "\n")
		}
		return b.String(), nil
	}
	if rule, ok := ufwAddedRule(args); ok {
		if u.failAdd != "" && "ufw "+rule == u.failAdd {
			return "", errors.New("boom")
		}
		u.installed["ufw "+rule] = true
		return "Rule added", nil
	}
	if rule, ok := ufwDeletedRule(args); ok {
		key := "ufw " + rule
		if !u.installed[key] {
			// Deliberately return the output separately from a generic
			// error: remove() must tolerate an already-missing rule from
			// the Runner contract, not only from the default runner's
			// output-in-error formatting.
			return "Could not find a matching rule", errors.New("exit status 1")
		}
		delete(u.installed, key)
		return "Rule deleted", nil
	}
	u.invalid = append(u.invalid, line)
	return "", errors.New("invalid ufw invocation")
}

func ufwAddedRule(args []string) (string, bool) {
	switch {
	case len(args) >= 1 && args[0] == "allow":
		return strings.Join(args, " "), true
	case len(args) >= 2 && args[0] == "route" && args[1] == "allow":
		return strings.Join(args, " "), true
	}
	return "", false
}

// ufwDeletedRule recognizes exactly the two argv shapes ufw accepts and
// returns the add-form rule they delete.
func ufwDeletedRule(args []string) (string, bool) {
	switch {
	case len(args) >= 3 && args[0] == "--force" && args[1] == "route" && args[2] == "delete":
		return "route " + strings.Join(args[3:], " "), true
	case len(args) >= 3 && args[0] == "--force" && args[1] == "delete":
		return strings.Join(args[2:], " "), true
	}
	return "", false
}

func TestRemoveUFWRouteRulesUseRouteSubcommand(t *testing.T) {
	u := newUFWState()
	old := SetRunner(u.run)
	t.Cleanup(func() { SetRunner(old) })
	cfg := Config{Interface: "skein0", Port: 11010}
	if err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(u.installed) != 5 {
		t.Fatalf("Ensure installed %d rules, want 5: %v", len(u.installed), u.calls)
	}
	if err := Remove(cfg); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(u.installed) != 0 {
		t.Fatalf("Remove left rules installed: %v", u.installed)
	}
	if len(u.invalid) != 0 {
		t.Fatalf("invalid ufw invocations: %v", u.invalid)
	}
	got := strings.Join(u.calls, "\n")
	for _, want := range []string{
		"ufw --force delete allow 11010/udp",
		"ufw --force delete allow in on skein0",
		"ufw --force delete allow out on skein0",
		"ufw --force route delete allow in on skein0",
		"ufw --force route delete allow out on skein0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// A missing rule is still not an error: remove must stay idempotent.
func TestRemoveUFWIgnoresMissingRules(t *testing.T) {
	u := newUFWState()
	old := SetRunner(u.run)
	t.Cleanup(func() { SetRunner(old) })
	if err := Remove(Config{Interface: "skein0", Port: 11010}); err != nil {
		t.Fatalf("Remove on an empty firewall: %v", err)
	}
	if len(u.invalid) != 0 {
		t.Fatalf("invalid ufw invocations: %v", u.invalid)
	}
}

// Repair rolls back this run's additions on failure; route rules must
// use the route subcommand there too.
func TestUFWRepairRollbackUsesRouteOrder(t *testing.T) {
	u := newUFWState()
	u.failAdd = "ufw route allow out on skein0"
	old := SetRunner(u.run)
	t.Cleanup(func() { SetRunner(old) })
	st, err := Repair(Config{Interface: "skein0", Port: 11010})
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if st.State != "degraded" {
		t.Fatalf("Repair state = %q, want degraded", st.State)
	}
	if len(u.installed) != 0 {
		t.Fatalf("rollback left rules installed: %v", u.installed)
	}
	if len(u.invalid) != 0 {
		t.Fatalf("invalid ufw invocations: %v", u.invalid)
	}
	got := strings.Join(u.calls, "\n")
	if !strings.Contains(got, "ufw --force route delete allow in on skein0") {
		t.Fatalf("rollback did not delete the route rule in ufw order:\n%s", got)
	}
}

// TestFirewallScriptRemoveUsesRouteOrder runs the shipped script with a
// fake ufw on PATH and a fake id that reports root, exercising the real
// delete argv without touching a firewall.
func TestFirewallScriptRemoveUsesRouteOrder(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	script := filepath.Join("..", "..", "scripts", "firewall-linux.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("firewall script not found: %v", err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "ufw.log")
	writeExecutable(t, filepath.Join(dir, "id"), "#!/bin/sh\necho 0\n")
	writeExecutable(t, filepath.Join(dir, "ufw"), `#!/bin/sh
printf 'ufw %s\n' "$*" >> "$UFW_LOG"
case "$1" in
  status) echo "Status: active" ;;
  show) echo "" ;;
esac
exit 0
`)
	cmd := exec.Command(bash, script, "--remove", "--port", "11010", "--interface", "skein0")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("UFW_LOG", log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read fake ufw log: %v", err)
	}
	got := string(raw)
	for _, want := range []string{
		"ufw --force delete allow 11010/udp",
		"ufw --force route delete allow in on skein0",
		"ufw --force route delete allow out on skein0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script did not run %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--force delete route") {
		t.Fatalf("script used the invalid route delete order:\n%s", got)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
