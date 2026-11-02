package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

func layoutFixture() view {
	return view{
		st: statusDTO{ID: "0123456789abcdef", OverlayIPv4: "10.144.144.1", UptimeSeconds: 90},
		peers: []peerDTO{
			{ID: "a", Name: "alpha", State: "up", Transport: "udp", RTTMs: 1.2, OverlayIPv4: "10.144.144.2", Endpoint: "10.0.0.2:11010"},
			{ID: "b", Name: "bravo", State: "up", Transport: "udp", RTTMs: 3.4, OverlayIPv4: "10.144.144.3"},
		},
		routes: []routeDTO{
			{Destination: "a", NextHop: "a", Hops: 1, CostMs: 1.2},
			{Destination: "b", NextHop: "b", Hops: 1, CostMs: 3.4},
		},
		activities: []activityDTO{{Origin: "a", Destination: "b", Path: []string{"b"}, TxBytes: 100, ExpiresMS: 1 << 62}},
		rates:      map[string]rate{"a": {rx: 1000, tx: 500}, "b": {rx: 200, tx: 100}},
		rtts:       map[string]float64{"a": 1.2, "b": 3.4},
	}
}

func TestDeckLayoutsFitAndExposeControls(t *testing.T) {
	d := testDashboard()
	v := layoutFixture()
	for _, tc := range []struct {
		width, height int
		mode          dashboardMode
	}{
		{40, 12, modeShort},
		{64, 16, modeCompact},
		{80, 24, modeCompact},
		{120, 36, modeStandard},
		{160, 45, modeWide},
	} {
		if got := resolveLayout(tc.width, tc.height).mode; got != tc.mode {
			t.Fatalf("%dx%d mode = %v, want %v", tc.width, tc.height, got, tc.mode)
		}
		lines := d.buildSized(v, tc.width, tc.height)
		for i, line := range lines {
			if got := visibleWidth(line); got > tc.width {
				t.Fatalf("%dx%d line %d width %d: %q", tc.width, tc.height, i, got, line)
			}
		}
		out := strings.Join(lines, "\n")
		switch tc.mode {
		case modeCompact:
			if !strings.Contains(out, "[Overview]") {
				t.Fatalf("%dx%d tab strip does not name the page:\n%s", tc.width, tc.height, out)
			}
			if !strings.Contains(out, "Overview") || !strings.Contains(out, "Activity") {
				t.Fatalf("%dx%d tab strip is incomplete:\n%s", tc.width, tc.height, out)
			}
		case modeShort:
			if !strings.Contains(out, "Skein") || !strings.Contains(out, "TAB pages") {
				t.Fatalf("%dx%d short deck lacks status or controls:\n%s", tc.width, tc.height, out)
			}
		}
	}
}

func TestLayoutPreservesSelectionAcrossBreakpoints(t *testing.T) {
	d := testDashboard()
	d.peers = layoutFixture().peers
	d.handleKey('j')
	if d.ui.selected != "a" {
		t.Fatalf("selected = %q", d.ui.selected)
	}
	for _, size := range []struct{ w, h int }{{160, 45}, {120, 36}, {64, 16}, {40, 12}} {
		out := strings.Join(d.buildSized(d.view(), size.w, size.h), "\n")
		if d.ui.selected != "a" {
			t.Fatalf("%dx%d lost the selection: %q", size.w, size.h, d.ui.selected)
		}
		if !strings.Contains(out, "peer 1/2") {
			t.Fatalf("%dx%d footer lost the position:\n%s", size.w, size.h, out)
		}
	}
}

func TestFocusCyclesVisiblePanelsByMode(t *testing.T) {
	d := testDashboard()
	d.width, d.height = 160, 45
	for _, want := range []dashboardFocus{focusMap, focusPeers, focusRoutes, focusActivity, focusOverview} {
		d.handleKey('\t')
		if d.ui.focus != want {
			t.Fatalf("wide TAB focus = %v, want %v", d.ui.focus, want)
		}
	}
	d.ui.focus = focusOverview
	d.width, d.height = 120, 36
	for _, want := range []dashboardFocus{focusMap, focusPeers, focusOverview} {
		d.handleKey('\t')
		if d.ui.focus != want {
			t.Fatalf("standard TAB focus = %v, want %v", d.ui.focus, want)
		}
	}
	d.handleKey('h')
	if d.ui.focus != focusPeers {
		t.Fatalf("h focus = %v", d.ui.focus)
	}
}

func TestPanelCursorsMoveIndependently(t *testing.T) {
	d := testDashboard()
	d.width, d.height = 160, 45
	d.routes = []routeDTO{{Destination: "r1"}, {Destination: "r2"}}
	d.activities = []activityDTO{
		{Origin: "a", Destination: "b", ExpiresMS: 1 << 62},
		{Origin: "c", Destination: "d", ExpiresMS: 1 << 62},
	}
	d.ui.focus = focusRoutes
	d.handleKey('j')
	if d.ui.routeSel != "r1" {
		t.Fatalf("first route = %q", d.ui.routeSel)
	}
	d.handleKey('j')
	if d.ui.routeSel != "r2" {
		t.Fatalf("second route = %q", d.ui.routeSel)
	}
	d.ui.focus = focusActivity
	d.handleKey('j')
	if d.ui.activitySel != activityKey(d.activities[0]) {
		t.Fatalf("first activity = %q", d.ui.activitySel)
	}
	d.handleKey('j')
	if d.ui.activitySel != activityKey(d.activities[1]) {
		t.Fatalf("second activity = %q", d.ui.activitySel)
	}
	d.routes = nil
	d.ui.focus = focusRoutes
	d.handleKey('j')
	if d.ui.routeSel != "" {
		t.Fatalf("empty route list kept %q", d.ui.routeSel)
	}
}

func TestEnterExpandsTheFocusedPanel(t *testing.T) {
	d := testDashboard()
	d.width, d.height = 160, 45
	d.peers = []peerDTO{{ID: "a", Name: "alpha", State: "up"}}
	d.routes = []routeDTO{{Destination: "a", NextHop: "a", Hops: 1}}
	d.ui.focus = focusPeers
	d.ui.selected = "a"
	d.handleKey('\r')
	if !d.ui.expanded || d.ui.expandedFocus != focusPeers {
		t.Fatalf("enter: expanded=%v focus=%v", d.ui.expanded, d.ui.expandedFocus)
	}
	d.handleKey(27)
	d.handleKey('x')
	if d.ui.expanded {
		t.Fatal("escape did not close the expanded panel")
	}
	d.ui.focus = focusRoutes
	d.handleKey('\r')
	if !d.ui.expanded || d.ui.expandedFocus != focusRoutes {
		t.Fatalf("routes expand: expanded=%v focus=%v", d.ui.expanded, d.ui.expandedFocus)
	}
}

func TestInspectorExpandsIntoPlace(t *testing.T) {
	d := newDashboard(conf.Config{API: conf.API{Listen: "127.0.0.1:1", Token: make([]byte, 32)}}, io.Discard, false, true)
	base := view{
		st:     statusDTO{ID: "local", Name: "XD_OD"},
		peers:  []peerDTO{{ID: "p", Name: "yoga", State: "up", Endpoint: "[2406:b400:72:ff06:4bcb:8b68:20e3:a714]:43921"}},
		ui:     dashboardState{focus: focusOverview, selected: "p", expanded: true, expandedFocus: focusOverview},
		motion: true,
	}
	base.ui.expandedAt = time.Now()
	partial := d.buildSized(base, 160, 45)
	base.ui.expandedAt = time.Now().Add(-inspectorReveal)
	full := d.buildSized(base, 160, 45)
	if len(partial) >= len(full) {
		t.Fatalf("expansion did not animate: %d >= %d lines", len(partial), len(full))
	}
	if !strings.Contains(strings.Join(full, "\n"), "[2406:b400:72:ff06:4bcb:8b68:20e3:a714]:43921") {
		t.Fatal("settled inspector lost the endpoint")
	}
	// Reduced motion shows the final panel immediately.
	base.ui.expandedAt = time.Now()
	base.motion = false
	if got := len(d.buildSized(base, 160, 45)); got != len(full) {
		t.Fatalf("motionless inspector = %d lines, want %d", got, len(full))
	}
}

func TestEscapeClearsTheInspectorTimestamp(t *testing.T) {
	d := testDashboard()
	d.peers = []peerDTO{{ID: "p", State: "up"}}
	d.ui.focus = focusPeers
	d.ui.selected = "p"
	d.handleKey('\r')
	if d.ui.expandedAt.IsZero() {
		t.Fatal("opening the inspector did not record a timestamp")
	}
	d.handleKey(27)
	d.handleKey('x')
	if !d.ui.expandedAt.IsZero() {
		t.Fatal("closing the inspector kept the timestamp")
	}
}

func TestExpandedPanelsShowFullContent(t *testing.T) {
	d := testDashboard()
	endpoint := "[2406:b400:72:ff06:4bcb:8b68:20e3:a714]:43921"
	base := view{
		st:     statusDTO{ID: "local", Name: "XD_OD"},
		peers:  []peerDTO{{ID: "p", Name: "yoga", State: "up", Transport: "udp", Endpoint: endpoint, RTTMs: 6.6}},
		routes: []routeDTO{{Destination: "p", NextHop: "p", Hops: 1, CostMs: 6.6}},
		ui:     dashboardState{focus: focusPeers, expanded: true, expandedFocus: focusPeers, expandedAt: time.Now().Add(-inspectorReveal)},
	}
	peers := strings.Join(d.buildSized(base, 160, 45), "\n")
	if !strings.Contains(peers, "▸ PEERS") || !strings.Contains(peers, endpoint) {
		t.Fatalf("expanded peers lost content:\n%s", peers)
	}
	base.ui.focus, base.ui.expandedFocus = focusRoutes, focusRoutes
	routes := strings.Join(d.buildSized(base, 160, 45), "\n")
	if !strings.Contains(routes, "▸ ROUTES") || !strings.Contains(routes, "XD_OD → yoga") {
		t.Fatalf("expanded routes lost content:\n%s", routes)
	}
}

func TestCompactPeerRowKeepsTheOverlayIP(t *testing.T) {
	d := testDashboard()
	v := view{
		st:    statusDTO{ID: "local"},
		peers: []peerDTO{{ID: "p", Name: "yoga", State: "up", Transport: "udp", OverlayIPv4: "10.144.144.241"}},
	}
	for _, width := range []int{40, 24} {
		out := strings.Join(d.renderPeers(v, width, 4), "\n")
		if !strings.Contains(out, "10.144.144.241") {
			t.Fatalf("%d-col row clipped the overlay address:\n%s", width, out)
		}
		if strings.Contains(out, "…") {
			t.Fatalf("%d-col compact row ellipsizes:\n%s", width, out)
		}
	}
}

func TestCompactDeckStartsWithTheRouteMap(t *testing.T) {
	d := testDashboard()
	d.applyInitialFocus(40, 20)
	if d.ui.focus != focusMap {
		t.Fatalf("compact initial page = %v, want map", d.ui.focus)
	}
	wide := testDashboard()
	wide.applyInitialFocus(160, 45)
	if wide.ui.focus != focusOverview {
		t.Fatalf("wide initial page = %v, want overview", wide.ui.focus)
	}
	wide.applyInitialFocus(40, 20)
	if wide.ui.focus != focusOverview {
		t.Fatalf("initial focus changed after the first draw: %v", wide.ui.focus)
	}
}

func TestTabStripAndStatusFitNarrowTerminals(t *testing.T) {
	d := testDashboard()
	v := layoutFixture()
	v.ui.focus = focusMap
	full := strings.Join(d.buildSized(v, 64, 20), "\n")
	if !strings.Contains(full, "[Map]") || !strings.Contains(full, "Activity") {
		t.Fatalf("compact tab strip is incomplete:\n%s", full)
	}
	if narrow := d.tabStrip(focusMap, 30); !strings.Contains(narrow, "‹ Map ›") {
		t.Fatalf("narrow tab strip lost the selected page: %q", narrow)
	}
	short := strings.Join(d.buildSized(v, 40, 20), "\n")
	if !strings.Contains(short, "TAB pages") || !strings.Contains(short, "map") {
		t.Fatalf("short deck lost its page hint:\n%s", short)
	}
	for _, width := range []int{30, 40, 60} {
		if got := visibleWidth(d.statusLine(v, width)); got > width {
			t.Fatalf("status line is %d cells wide at %d", got, width)
		}
	}
}
