package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

func testDashboard() *dashboard {
	return newDashboard(conf.Config{API: conf.API{Listen: "127.0.0.1:1", Token: make([]byte, 32)}}, io.Discard, false, false)
}

func TestDashboardBuildShowsMeshState(t *testing.T) {
	d := testDashboard()
	st := statusDTO{
		ID: "63c96384a48a4ebf", OverlayIPv4: "10.144.144.1", Version: "0.1.0-dev",
		ListenPort: 11010, UptimeSeconds: 90,
	}
	peers := []peerDTO{
		{
			ID: "0831c1ad08c609d9", Name: "yoga", State: "up", Path: "v4", Transport: "udp",
			RTTMs: 1.2, OverlayIPv4: "10.144.144.2", Endpoint: "125.99.177.247:11010",
			RxBytes: 4096, TxBytes: 2048, LastSeenMS: 2000,
		},
		{ID: "aaaaaaaaaaaaaaaa", State: "down"},
	}
	routes := []routeDTO{{Destination: "0831c1ad08c609d9", NextHop: "0831c1ad08c609d9", CostMs: 1.2, Hops: 1}}

	out := strings.Join(d.build(view{st: st, peers: peers, routes: routes}, 120), "\n")
	for _, want := range []string{
		"SKEIN", "PEERS", "ROUTES", "ACTIVITY",
		"63c96384a48a4ebf", "10.144.144.1",
		"yoga", "10.144.144.2", "udp", "up", "down",
		"1.2 ms", "125.99.177.247:11010",
		"1 up", "1 down",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard output is missing %q:\n%s", want, out)
		}
	}
}

func TestDashboardEmptyMeshHintsAtJoin(t *testing.T) {
	d := testDashboard()
	out := strings.Join(d.build(view{st: statusDTO{ID: "abc", OverlayIPv4: "10.144.144.1"}}, 120), "\n")
	if !strings.Contains(out, "skein join") {
		t.Fatalf("empty mesh should hint at join:\n%s", out)
	}
}

func TestRatesAndFormatting(t *testing.T) {
	if got := humanRate(0); got != "—" {
		t.Fatalf("humanRate(0) = %q", got)
	}
	if got := humanRate(2048); got != "2.0 kB/s" {
		t.Fatalf("humanRate(2048) = %q", got)
	}
	if got := humanDuration(90 * 1e9); got != "1m30s" {
		t.Fatalf("humanDuration = %q", got)
	}
	spark := sparkline([]float64{0, 1, 2, 4})
	if len([]rune(spark)) != 4 {
		t.Fatalf("sparkline = %q", spark)
	}
	d := testDashboard()
	d.targets["a"] = rate{rx: 100, tx: 50}
	d.peers = []peerDTO{{ID: "a", RTTMs: 10}}
	for range 10 {
		d.ease()
	}
	r := d.disp["a"]
	if r.rx <= 0 || r.tx <= 0 || r.rtt <= 0 {
		t.Fatalf("eased values = %+v", r)
	}
	if r.rx > 100 {
		t.Fatalf("easing overshot: %+v", r)
	}
}

func TestStripANSIAndFit(t *testing.T) {
	if got := stripANSI("\x1b[32mup\x1b[0m"); got != "up" {
		t.Fatalf("stripANSI = %q", got)
	}
	if got := fit("abc", 5); got != "abc  " {
		t.Fatalf("fit pad = %q", got)
	}
	if got := fit("abcdef", 3); got != "abc" {
		t.Fatalf("fit truncate = %q", got)
	}
}

func TestMeshRendersNodesLinksAndPulses(t *testing.T) {
	d := testDashboard()
	v := view{
		st:     statusDTO{ID: "63c96384a48a4ebf", OverlayIPv4: "10.144.144.1"},
		peers:  []peerDTO{{ID: "0831c1ad08c609d9", Name: "yoga", State: "up", OverlayIPv4: "10.144.144.2"}, {ID: "aaaaaaaaaaaaaaaa", State: "down"}},
		routes: []routeDTO{{Destination: "0831c1ad08c609d9", NextHop: "0831c1ad08c609d9", CostMs: 1, Hops: 1}},
		rates:  map[string]rate{"0831c1ad08c609d9": {rx: 5000, tx: 2000}},
		rtts:   map[string]float64{"0831c1ad08c609d9": 1.2},
		motion: true,
		phase:  0.6,
		frame:  4,
	}
	out := strings.Join(d.build(v, 110), "\n")
	for _, want := range []string{"MESH", "◆", "me", "yoga", "10.144.144.2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("mesh is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "●") {
		t.Fatalf("mesh has no pulse or node glyph:\n%s", out)
	}
}

func TestMeshHandlesRelayedPaths(t *testing.T) {
	d := testDashboard()
	v := view{
		peers: []peerDTO{
			{ID: "1111111111111111", State: "up", OverlayIPv4: "10.144.144.2"},
			{ID: "2222222222222222", State: "up", OverlayIPv4: "10.144.144.3"},
		},
		routes: []routeDTO{
			{Destination: "1111111111111111", NextHop: "1111111111111111", Hops: 1},
			{Destination: "2222222222222222", NextHop: "1111111111111111", Hops: 2},
		},
		motion: false,
	}
	out := strings.Join(d.build(v, 110), "\n")
	if !strings.Contains(out, "111111") || !strings.Contains(out, "222222") {
		t.Fatalf("relayed mesh missing nodes:\n%s", out)
	}
}

func TestDashboardPlaceholdersWithoutData(t *testing.T) {
	d := testDashboard()
	out := strings.Join(d.build(view{}, 110), "\n")
	if strings.Contains(out, ":0") {
		t.Fatalf("rendered a zero port:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Fatalf("expected placeholders:\n%s", out)
	}
}

func TestDashboardShowsRecoveryHint(t *testing.T) {
	d := testDashboard()
	v := view{
		err:        errors.New("connection refused"),
		recoverErr: errors.New("the daemon is not running and starting it needs Administrator:\n  run an elevated prompt"),
	}
	out := strings.Join(d.build(v, 110), "\n")
	if !strings.Contains(out, "disconnected from daemon") {
		t.Fatalf("missing disconnect banner:\n%s", out)
	}
	if !strings.Contains(out, "needs Administrator") {
		t.Fatalf("missing recovery hint:\n%s", out)
	}
}

func TestMissionControlAdaptsToTerminalBounds(t *testing.T) {
	d := testDashboard()
	v := view{st: statusDTO{ID: "0123456789abcdef", OverlayIPv4: "10.144.144.1"}, peers: []peerDTO{{ID: "1111111111111111", Name: "relay", State: "up", RTTMs: 4}}, routes: []routeDTO{{Destination: "1111111111111111", NextHop: "1111111111111111", Hops: 1, Path: []string{"1111111111111111"}}}}
	for _, tc := range []struct{ w, h int }{{40, 12}, {80, 24}, {120, 36}, {160, 45}} {
		for _, line := range d.buildSized(v, tc.w, tc.h) {
			if got := len([]rune(stripANSI(line))); got > tc.w {
				t.Fatalf("%dx%d line width %d: %q", tc.w, tc.h, got, line)
			}
		}
	}
}

func TestDashboardKeysOnlyChangeDashboardState(t *testing.T) {
	d := testDashboard()
	d.peers = []peerDTO{{ID: "a", State: "up"}, {ID: "b", State: "up"}}
	d.moveSelection(1)
	if d.ui.selected != "b" {
		t.Fatalf("selected = %q", d.ui.selected)
	}
	d.handleKey(' ')
	if !d.ui.paused {
		t.Fatal("space did not pause")
	}
	d.handleKey('?')
	if !d.ui.help {
		t.Fatal("help not shown")
	}
	if !d.handleKey('q') {
		t.Fatal("q did not request quit")
	}
}

func TestMeshAddsOnlyFreshReportedRemoteEdges(t *testing.T) {
	d := testDashboard()
	v := view{
		st:         statusDTO{ID: "local"},
		peers:      []peerDTO{{ID: "a", Name: "alpha", State: "up"}, {ID: "b", Name: "bravo", State: "up"}},
		activities: []activityDTO{{Origin: "a", Destination: "b", Path: []string{"b"}, TxBytes: 100, ExpiresMS: time.Now().Add(time.Second).UnixMilli()}},
		motion:     true,
	}
	out := strings.Join(d.build(v, 110), "\n")
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "bravo") {
		t.Fatalf("remote activity nodes missing:\n%s", out)
	}
	v.activities[0].ExpiresMS = time.Now().Add(-time.Second).UnixMilli()
	if strings.Contains(strings.Join(d.renderActivity(v, 110, 1), "\n"), "alpha → bravo") {
		t.Fatal("expired activity was rendered")
	}
}
