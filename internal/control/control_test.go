package control

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/utsavgupta5467/skein/internal/discovery"
	"github.com/utsavgupta5467/skein/internal/identity"
	"github.com/utsavgupta5467/skein/internal/peer"
	"github.com/utsavgupta5467/skein/internal/route"
	"github.com/utsavgupta5467/skein/internal/wire"
)

type fakeTransport struct {
	mu    sync.Mutex
	sent  []sentFrame
	fail  bool
	rekey []uint64
}

type sentFrame struct {
	peerID uint64
	addr   netip.AddrPort
	typ    wire.MessageType
	body   []byte
}

func (f *fakeTransport) SendControl(peerID uint64, typ wire.MessageType, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errFake
	}
	f.sent = append(f.sent, sentFrame{peerID: peerID, typ: typ, body: append([]byte(nil), payload...)})
	return nil
}

func (f *fakeTransport) SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errFake
	}
	f.sent = append(f.sent, sentFrame{peerID: peerID, addr: addr, typ: typ, body: append([]byte(nil), payload...)})
	return nil
}

func (f *fakeTransport) Rekey(peerID uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rekey = append(f.rekey, peerID)
	return nil
}

func (f *fakeTransport) frames(typ wire.MessageType) []sentFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentFrame
	for _, s := range f.sent {
		if s.typ == typ {
			out = append(out, s)
		}
	}
	return out
}

var errFake = errFakeType{}

type errFakeType struct{}

func (errFakeType) Error() string { return "fake transport failure" }

type fixture struct {
	ctl     *Controller
	peers   *peer.Table
	routes  *route.Table
	eng     *fakeTransport
	now     time.Time
	peerID  uint64
	peerKey []byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	id := mustIdentity(t)
	remote := mustIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(id.ID)
	if err := peers.Upsert(peer.Peer{
		ID: remote.ID, PublicKey: remote.PublicKey, Dialer: true, State: peer.StateUp,
		Candidates: []peer.Candidate{{Addr: netip.MustParseAddrPort("192.0.2.2:11010"), Source: peer.SourceConfig}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: remote.ID, RTT: 5 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	eng := &fakeTransport{}
	merger := discovery.NewMerger(id.ID, peers, routes)
	f := &fixture{peers: peers, routes: routes, eng: eng, now: time.Unix(1_700_000_000, 0), peerID: remote.ID, peerKey: remote.PublicKey}
	ctl, err := New(Local{
		ID:         id.ID,
		OverlayV4:  netip.MustParseAddr("10.144.144.1"),
		Candidates: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:11010")},
	}, peers, routes, merger, eng)
	if err != nil {
		t.Fatal(err)
	}
	ctl.SetClock(func() time.Time { return f.now })
	f.ctl = ctl
	return f
}

func mustIdentity(t *testing.T) identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPingIsSentOnTheInterval(t *testing.T) {
	f := newFixture(t)
	f.peers.MarkSeen(f.peerID, f.now)
	f.ctl.Tick()
	if got := len(f.eng.frames(wire.Ping)); got != 1 {
		t.Fatalf("pings = %d, want 1", got)
	}
	f.now = f.now.Add(5 * time.Second)
	f.ctl.Tick()
	if got := len(f.eng.frames(wire.Ping)); got != 1 {
		t.Fatalf("ping sent before the interval: %d", got)
	}
	f.now = f.now.Add(PingInterval)
	f.ctl.Tick()
	if got := len(f.eng.frames(wire.Ping)); got != 2 {
		t.Fatalf("pings = %d, want 2", got)
	}
}

func TestSilentPeerIsMarkedDownAndRoutesWithdrawn(t *testing.T) {
	f := newFixture(t)
	f.peers.MarkSeen(f.peerID, f.now)
	f.now = f.now.Add(PeerTimeout + time.Second)
	f.ctl.Tick()
	p, _ := f.peers.Get(f.peerID)
	if p.State != peer.StateDown {
		t.Fatalf("state = %v, want down", p.State)
	}
	if _, ok := f.routes.Lookup(f.peerID); ok {
		t.Fatal("route survived peer timeout")
	}
}

func TestStateIsBroadcastAndMerged(t *testing.T) {
	f := newFixture(t)
	f.peers.MarkSeen(f.peerID, f.now)
	f.ctl.Tick()
	frames := f.eng.frames(wire.State)
	if len(frames) != 1 {
		t.Fatalf("state frames = %d, want 1", len(frames))
	}
	state, err := discovery.Decode(frames[0].body)
	if err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Origin != f.ctl.local.ID || len(state.Peers) == 0 {
		t.Fatalf("state = %+v", state)
	}

	// A received state from the peer must merge into local routes.
	payload, err := discovery.Encode(discovery.State{
		Origin:  f.peerID,
		Seq:     1,
		Expires: time.Now().Add(time.Minute),
		Routes:  []discovery.RouteRecord{{Destination: 0x99, Cost: time.Millisecond, Hops: 1, Path: []uint64{0x99}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.ctl.HandleControl(f.peerID, netip.MustParseAddrPort("192.0.2.2:11010"), wire.State, payload)
	if _, ok := f.routes.Lookup(0x99); !ok {
		t.Fatal("state route was not merged")
	}
}

func TestPingHandlerEchoesPayload(t *testing.T) {
	f := newFixture(t)
	reply := f.ctl.HandlePing(f.peerID, []byte("ping-data"))
	if string(reply) != "ping-data" {
		t.Fatalf("reply = %q", reply)
	}
	if p, _ := f.peers.Get(f.peerID); p.LastSeen.IsZero() {
		t.Fatal("ping did not refresh last seen")
	}
}

func TestProbeMovesTheActiveEndpoint(t *testing.T) {
	f := newFixture(t)
	candidate := netip.MustParseAddrPort("198.51.100.9:11010")
	if !f.peers.AddCandidate(f.peerID, candidate, peer.SourceConfig) {
		t.Fatal("candidate not added")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// Answer the challenge from the second candidate.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			frames := f.eng.frames(wire.Challenge)
			if len(frames) > 0 {
				token := frames[len(frames)-1].body
				f.ctl.HandleControl(f.peerID, candidate, wire.ChallengeReply, token)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if err := f.ctl.Probe(ctx, f.peerID); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	p, _ := f.peers.Get(f.peerID)
	if p.ActiveEndpoint != candidate {
		t.Fatalf("active endpoint = %s, want %s", p.ActiveEndpoint, candidate)
	}
}
