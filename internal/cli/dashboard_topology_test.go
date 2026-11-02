package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

func coloredDashboard() *dashboard {
	return newDashboard(conf.Config{API: conf.API{Listen: "127.0.0.1:1", Token: make([]byte, 32)}}, io.Discard, true, false)
}

func TestSinglePeerRendersDirectLinkLane(t *testing.T) {
	d := testDashboard()
	v := view{
		st:     statusDTO{ID: "local"},
		peers:  []peerDTO{{ID: "p", Name: "yoga", State: "up", Transport: "udp", RTTMs: 1.2, Endpoint: "10.0.0.2:11010"}},
		routes: []routeDTO{{Destination: "p", NextHop: "p", Hops: 1, CostMs: 1.2}},
		rates:  map[string]rate{"p": {rx: 100, tx: 50}},
	}
	out := stripANSI(strings.Join(d.renderRouteMap(v, 90, 9), "\n"))
	for _, want := range []string{"ME", "yoga", "udp", "1.2 ms", "1 hop", "10.0.0.2:11010"} {
		if !strings.Contains(out, want) {
			t.Fatalf("link lane is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "━") {
		t.Fatalf("healthy lane has no link glyph:\n%s", out)
	}
}

func TestActivityUsesLocalPerspective(t *testing.T) {
	d := testDashboard()
	v := view{
		st:    statusDTO{ID: "local"},
		peers: []peerDTO{{ID: "p", Name: "yoga", State: "up"}},
	}
	// The remote peer reports what it received from us: our outbound.
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, RxBytes: 100, ExpiresMS: 1 << 62}}
	out := strings.Join(d.renderActivity(v, 80, 4), "\n")
	if !strings.Contains(out, "ME → yoga") || !strings.Contains(out, "↑") {
		t.Fatalf("outbound activity is not localized:\n%s", out)
	}
	// What it sent to us is inbound.
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, TxBytes: 100, ExpiresMS: 1 << 62}}
	out = strings.Join(d.renderActivity(v, 80, 4), "\n")
	if !strings.Contains(out, "yoga → ME") || !strings.Contains(out, "↓") {
		t.Fatalf("inbound activity is not localized:\n%s", out)
	}
	// A ping produces both directions.
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, RxBytes: 100, TxBytes: 100, ExpiresMS: 1 << 62}}
	out = strings.Join(d.renderActivity(v, 80, 4), "\n")
	if !strings.Contains(out, "ME → yoga") || !strings.Contains(out, "yoga → ME") {
		t.Fatalf("bidirectional activity missing a direction:\n%s", out)
	}
}

func TestLocalNameReplacesME(t *testing.T) {
	d := testDashboard()
	v := view{
		st:     statusDTO{ID: "local", Name: "XD_OD", OverlayIPv4: "10.144.144.1"},
		peers:  []peerDTO{{ID: "p", Name: "yoga", State: "up"}},
		routes: []routeDTO{{Destination: "p", NextHop: "p", Hops: 1}},
	}
	out := strings.Join(d.renderRouteMap(v, 80, 9), "\n")
	if !strings.Contains(out, "XD_OD") || strings.Contains(out, "ME") {
		t.Fatalf("lane does not use the configured name:\n%s", out)
	}
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, RxBytes: 10, ExpiresMS: 1 << 62}}
	rows := strings.Join(d.renderActivity(v, 80, 2), "\n")
	if !strings.Contains(rows, "XD_OD → yoga") {
		t.Fatalf("activity does not use the configured name:\n%s", rows)
	}
}

func TestInspectorOverlayShowsTheFullEndpoint(t *testing.T) {
	d := testDashboard()
	endpoint := "[2406:b400:72:ff06:4bcb:8b68:20e3:a714]:43921"
	v := view{
		st:    statusDTO{ID: "local", Name: "XD_OD"},
		peers: []peerDTO{{ID: "p", Name: "yoga", State: "up", Transport: "udp", Endpoint: endpoint, RTTMs: 1.2}},
		ui:    dashboardState{focus: focusOverview, selected: "p", expanded: true, expandedFocus: focusOverview},
	}
	lines := d.buildSized(v, 160, 45)
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, endpoint) {
		t.Fatalf("expanded inspector truncated the endpoint:\n%s", out)
	}
	for _, line := range lines {
		if visibleWidth(line) > 160 {
			t.Fatalf("line overflows: %q", line)
		}
	}
}

func TestPanelsUseSemanticColors(t *testing.T) {
	d := coloredDashboard()
	v := view{
		st:     statusDTO{ID: "local", Name: "XD_OD"},
		peers:  []peerDTO{{ID: "p", Name: "yoga", State: "up", Transport: "udp", Direct: true, Endpoint: "10.0.0.2:11010", RTTMs: 8}},
		routes: []routeDTO{{Destination: "p", NextHop: "p", Hops: 1, CostMs: 8}},
		rates:  map[string]rate{"p": {rx: 100, tx: 50}},
		ui:     dashboardState{selected: "p"},
	}
	inspector := strings.Join(d.renderInspector(v, 80), "\n")
	if !strings.Contains(inspector, "\x1b["+ansiHealthy+"m") {
		t.Fatalf("inspector state is not colored: %q", inspector)
	}
	if !strings.Contains(inspector, "\x1b["+ansiIn+"m") || !strings.Contains(inspector, "\x1b["+ansiOut+"m") {
		t.Fatalf("inspector traffic is not colored: %q", inspector)
	}
	routes := strings.Join(d.renderRoutes(v, 80, 3), "\n")
	if !strings.Contains(routes, "\x1b["+ansiHealthy+"m") {
		t.Fatalf("route cost is not colored: %q", routes)
	}
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, TxBytes: 100, ExpiresMS: 1 << 62}}
	activity := strings.Join(d.renderActivity(v, 80, 2), "\n")
	if !strings.Contains(activity, "\x1b["+ansiIn+"m") {
		t.Fatalf("activity rate is not colored: %q", activity)
	}
}

func TestPulseDirectionsFollowLocalTraffic(t *testing.T) {
	d := testDashboard()
	v := view{
		st:     statusDTO{ID: "local"},
		peers:  []peerDTO{{ID: "p"}},
		rates:  map[string]rate{"p": {rx: 100}},
		motion: true,
	}
	pulses := d.edgePulses(v, "me", "p")
	if len(pulses) != 1 || pulses[0].color != ansiIn || !pulses[0].reverse {
		t.Fatalf("inbound pulses = %+v", pulses)
	}
	v.rates["p"] = rate{tx: 100}
	pulses = d.edgePulses(v, "me", "p")
	if len(pulses) != 1 || pulses[0].color != ansiOut || pulses[0].reverse {
		t.Fatalf("outbound pulses = %+v", pulses)
	}
	v.rates["p"] = rate{rx: 100, tx: 100}
	if got := len(d.edgePulses(v, "me", "p")); got != 2 {
		t.Fatalf("bidirectional pulses = %d", got)
	}
}

func TestMapDoesNotFabricateEdges(t *testing.T) {
	d := testDashboard()
	v := view{st: statusDTO{ID: "local"}, peers: []peerDTO{{ID: "a", Name: "alpha"}, {ID: "b", Name: "bravo"}}}
	out := strings.Join(d.renderRouteMap(v, 80, 10), "\n")
	if !strings.Contains(out, "waiting for routes") {
		t.Fatalf("empty map should wait, not invent edges:\n%s", out)
	}
}

func TestSelectedPeerIsMarkedOnTheMap(t *testing.T) {
	d := coloredDashboard()
	v := view{
		st:    statusDTO{ID: "local"},
		peers: []peerDTO{{ID: "a", Name: "alpha"}, {ID: "b", Name: "bravo"}},
		ui:    dashboardState{selected: "b"},
	}
	out := stripANSI(strings.Join(d.renderRouteMap(v, 80, 12), "\n"))
	if !strings.Contains(out, "◉") {
		t.Fatalf("selected peer has no marker:\n%s", out)
	}
}

func TestSelectedRouteIsEmphasizedOnTheMap(t *testing.T) {
	d := coloredDashboard()
	v := view{
		st:     statusDTO{ID: "local"},
		peers:  []peerDTO{{ID: "a", Name: "alpha"}, {ID: "b", Name: "bravo"}},
		routes: []routeDTO{{Destination: "a", NextHop: "a", Hops: 1}, {Destination: "b", NextHop: "b", Hops: 1}},
		ui:     dashboardState{focus: focusRoutes, routeSel: "b"},
	}
	raw := strings.Join(d.renderRouteMap(v, 80, 12), "\n")
	if !strings.Contains(raw, "\x1b["+ansiBrand+"m") {
		t.Fatalf("selected route is not emphasized:\n%q", raw)
	}
}

func TestDisplayNameLocalizesKnownIDs(t *testing.T) {
	d := testDashboard()
	v := view{st: statusDTO{ID: "local"}, peers: []peerDTO{{ID: "p", Name: "yoga"}, {ID: "q"}}}
	if got := d.displayName(v, "local"); got != "ME" {
		t.Fatalf("local = %q", got)
	}
	if got := d.displayName(v, "p"); got != "yoga" {
		t.Fatalf("named peer = %q", got)
	}
	if got := d.displayName(v, "q"); got != shortID("q") {
		t.Fatalf("short id = %q", got)
	}
	if got := d.displayName(v, ""); got != "—" {
		t.Fatalf("empty = %q", got)
	}
}

func TestLocalNodeUsesTheBrandColor(t *testing.T) {
	d := coloredDashboard()
	v := view{
		st:     statusDTO{ID: "local", Name: "XD_OD", OverlayIPv4: "10.144.144.1"},
		peers:  []peerDTO{{ID: "p", Name: "yoga", State: "up"}},
		routes: []routeDTO{{Destination: "p", NextHop: "p", Hops: 1}},
	}
	lane := strings.Join(d.renderRouteMap(v, 80, 9), "\n")
	if !strings.Contains(lane, "\x1b["+ansiBrand+"mXD_OD") {
		t.Fatalf("lane does not brand the local name:\n%q", lane)
	}
	routes := strings.Join(d.renderRoutes(v, 80, 2), "\n")
	if !strings.Contains(routes, "\x1b["+ansiBrand+"mXD_OD") {
		t.Fatalf("routes do not brand the local name:\n%q", routes)
	}
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, RxBytes: 10, ExpiresMS: 1 << 62}}
	activity := strings.Join(d.renderActivity(v, 80, 2), "\n")
	if !strings.Contains(activity, "\x1b["+ansiBrand+"mXD_OD") {
		t.Fatalf("activity does not brand the local name:\n%q", activity)
	}
}

func TestActivePeersAreHighlighted(t *testing.T) {
	d := coloredDashboard()
	v := view{
		st:    statusDTO{ID: "local", Name: "XD_OD"},
		peers: []peerDTO{{ID: "a", Name: "alpha", State: "up"}, {ID: "b", Name: "bravo", State: "up"}},
		rates: map[string]rate{"a": {rx: 100, tx: 50}},
	}
	rows := strings.Join(d.renderPeers(v, 80, 4), "\n")
	if !strings.Contains(rows, "\x1b["+ansiOut+"m●") {
		t.Fatalf("active peer has no traffic marker:\n%q", rows)
	}
	if strings.Count(rows, "●") != 1 {
		t.Fatalf("idle peer is marked active:\n%q", rows)
	}
	narrow := strings.Join(d.renderPeers(v, 40, 6), "\n")
	if !strings.Contains(narrow, "\x1b["+ansiOut+"malpha") {
		t.Fatalf("active peer name is not highlighted in a narrow card:\n%q", narrow)
	}
}

func TestMeshSubstantiatesRemotePeerLinks(t *testing.T) {
	d := testDashboard()
	v := view{
		st: statusDTO{ID: "local"},
		peers: []peerDTO{
			{ID: "a", Name: "alpha", State: "up"},
			{ID: "b", Name: "bravo", State: "up"},
			{ID: "c", Name: "charlie", State: "up"},
		},
		activities: []activityDTO{
			{Origin: "a", Destination: "b", Path: []string{"b"}, TxBytes: 10, ExpiresMS: 1 << 62},
			{Origin: "b", Destination: "c", Path: []string{"c"}, TxBytes: 10, ExpiresMS: 1 << 62},
		},
	}
	edges := d.topologyEdges(v)
	if !edges[mapEdge{"a", "b"}] || !edges[mapEdge{"b", "c"}] {
		t.Fatalf("remote links missing: %+v", edges)
	}
	if edges[mapEdge{"a", "c"}] {
		t.Fatalf("fabricated an a↔c edge: %+v", edges)
	}
	out := strings.Join(d.renderRouteMap(v, 120, 20), "\n")
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if !strings.Contains(out, name) {
			t.Fatalf("mesh is missing %q:\n%s", name, out)
		}
	}
}
