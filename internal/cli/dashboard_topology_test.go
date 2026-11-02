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
	v.activities = []activityDTO{{Origin: "local", Destination: "p", Path: []string{"p"}, TxBytes: 100, ExpiresMS: 1 << 62}}
	out := strings.Join(d.renderActivity(v, 80, 4), "\n")
	if !strings.Contains(out, "ME → yoga") {
		t.Fatalf("outbound activity is not localized:\n%s", out)
	}
	v.activities = []activityDTO{{Origin: "p", Destination: "local", Path: []string{"local"}, RxBytes: 100, ExpiresMS: 1 << 62}}
	out = strings.Join(d.renderActivity(v, 80, 4), "\n")
	if !strings.Contains(out, "yoga → ME") {
		t.Fatalf("inbound activity is not localized:\n%s", out)
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
