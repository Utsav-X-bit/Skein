//go:build linux

package netcfg

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

type call struct {
	program string
	args    []string
}

type fakeRunner struct {
	calls   []call
	fail    map[string]error
	outputs map[string]string
	out     string
}

func (f *fakeRunner) run(program string, args ...string) (string, error) {
	f.calls = append(f.calls, call{program: program, args: args})
	line := program + " " + strings.Join(args, " ")
	for prefix, err := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return "", err
		}
	}
	best := ""
	for prefix := range f.outputs {
		if strings.HasPrefix(line, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best != "" {
		return f.outputs[best], nil
	}
	return f.out, nil
}

func (f *fakeRunner) lines() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.program + " " + strings.Join(c.args, " ")
	}
	return out
}

func withRunner(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{fail: map[string]error{}, outputs: map[string]string{}}
	old := runner
	SetRunner(f.run)
	t.Cleanup(func() { SetRunner(old) })
	return f
}

func TestConfigureBuildsExpectedCommands(t *testing.T) {
	f := withRunner(t)
	// Host prefixes, so the kernel does not pre-install the overlay routes.
	addrs := []netip.Prefix{
		netip.MustParsePrefix("10.144.144.2/32"),
		netip.MustParsePrefix("fd00:6d65:7368::2/128"),
	}
	routes := []netip.Prefix{
		netip.MustParsePrefix("10.144.144.0/24"),
		netip.MustParsePrefix("fd00:6d65:7368::/64"),
	}
	if err := Configure("skein0", 1280, addrs, routes); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	want := []string{
		"ip link set dev skein0 mtu 1280",
		"ip link set dev skein0 up",
		"ip addr add 10.144.144.2/32 dev skein0",
		"ip -6 addr add fd00:6d65:7368::2/128 dev skein0",
		"ip route add 10.144.144.0/24 dev skein0",
		"ip -6 route add fd00:6d65:7368::/64 dev skein0",
	}
	got := f.lines()
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestConfigureNeverTouchesTheDefaultRoute(t *testing.T) {
	f := withRunner(t)
	if err := Configure("skein0", 1280,
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.2/24")},
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")}); err != nil {
		t.Fatal(err)
	}
	for _, line := range f.lines() {
		if strings.Contains(line, "default") || strings.Contains(line, "0.0.0.0/0") || strings.Contains(line, "::/0") {
			t.Fatalf("command touches the default route: %q", line)
		}
	}
}

func TestConfigureRollsBackOnFailure(t *testing.T) {
	f := withRunner(t)
	f.fail["ip route add 10.144.144.0/24"] = errors.New("boom")
	err := Configure("skein0", 1280,
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.2/32")},
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")})
	if err == nil {
		t.Fatal("expected Configure to fail")
	}
	lines := f.lines()
	joined := strings.Join(lines, "\n")
	for _, undo := range []string{
		"ip addr del 10.144.144.2/32 dev skein0",
		"ip link set dev skein0 down",
	} {
		if !strings.Contains(joined, undo) {
			t.Fatalf("missing rollback %q in:\n%s", undo, joined)
		}
	}
}

func TestConfigureSkipsConnectedRoutes(t *testing.T) {
	f := withRunner(t)
	// A /24 address already installs the 10.144.144.0/24 connected route.
	if err := Configure("skein0", 1280,
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.2/24")},
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	for _, line := range f.lines() {
		if strings.Contains(line, "route add") {
			t.Fatalf("redundant connected route was installed: %q", line)
		}
	}
	// Removing the address must not try to delete the connected route either.
	if err := Remove("skein0",
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.2/24")},
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")}); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, line := range f.lines() {
		if strings.Contains(line, "route del") {
			t.Fatalf("connected route was deleted explicitly: %q", line)
		}
	}
}

func TestRemoveDeletesRoutesThenAddresses(t *testing.T) {
	f := withRunner(t)
	if err := Remove("skein0",
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.2/32")},
		[]netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")}); err != nil {
		t.Fatal(err)
	}
	got := f.lines()
	if len(got) != 2 || !strings.HasPrefix(got[0], "ip route del") || !strings.HasPrefix(got[1], "ip addr del") {
		t.Fatalf("commands = %v", got)
	}
}

func TestConflictsWarnsOnExistingRoute(t *testing.T) {
	f := withRunner(t)
	f.outputs["ip -6 route show"] = ""
	f.out = "10.144.144.0/24 dev eth0 proto kernel scope link src 10.144.144.2\n"
	warnings, err := Conflicts([]netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "eth0") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestReconcileRemovesOnlyStaleSkeinRoutes(t *testing.T) {
	f := withRunner(t)
	f.outputs["ip -6 route show"] = ""
	f.out = strings.Join([]string{
		"10.144.144.0/24 dev skein0 scope link",
		"10.144.144.128/25 dev skein0 scope link",
		"192.168.1.0/24 dev eth0 scope link",
		"10.144.144.0/24 dev eth0 scope link",
	}, "\n")
	overlay := []netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")}
	desired := []netip.Prefix{netip.MustParsePrefix("10.144.144.0/24")}
	if err := Reconcile("skein0", overlay, desired); err != nil {
		t.Fatal(err)
	}
	got := f.lines()
	var deletes []string
	for _, line := range got {
		if strings.Contains(line, "route del") {
			deletes = append(deletes, line)
		}
	}
	if len(deletes) != 1 || !strings.Contains(deletes[0], "10.144.144.128/25") {
		t.Fatalf("deletes = %v, want only the stale overlay route", deletes)
	}
}
