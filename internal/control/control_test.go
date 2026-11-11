package control

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/discovery"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

type fakeTransport struct {
	mu        sync.Mutex
	sent      []sentFrame
	fail      bool
	rekey     []uint64
	ensured   []uint64
	fallback  []uint64
	dropped   []uint64
	punched   []punchCall
	probed    []uint64
	probeErr  error
	noSession bool
}

type punchCall struct {
	peer uint64
	addr netip.AddrPort
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

func (f *fakeTransport) Punch(peerID uint64, addr netip.AddrPort) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.punched = append(f.punched, punchCall{peer: peerID, addr: addr})
	return nil
}

func (f *fakeTransport) AllowFallbackDial(peerID uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = append(f.fallback, peerID)
}

func (f *fakeTransport) DropSession(peerID uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, peerID)
}

func (f *fakeTransport) HasSession(uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.noSession
}

func (f *fakeTransport) EnsureSession(peerID uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, peerID)
	return nil
}

func (f *fakeTransport) ProbeMTU(peerID uint64, ceiling int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probed = append(f.probed, peerID)
	if f.probeErr != nil {
		return 0, f.probeErr
	}
	return ceiling, nil
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
		PublicKey:  id.PublicKey,
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

func TestDialerPeersAreContactedWithoutTraffic(t *testing.T) {
	f := newFixture(t)
	f.ctl.Tick()
	f.eng.mu.Lock()
	ensured := append([]uint64(nil), f.eng.ensured...)
	f.eng.mu.Unlock()
	found := false
	for _, id := range ensured {
		if id == f.peerID {
			found = true
		}
	}
	if !found {
		t.Fatalf("dialer peer was not contacted: %v", ensured)
	}
}

// TestNonDialerFallsBackAfterSilence is the M4 P4 (F-11) regression test:
// a non-dialer peer that has never been contacted at all after a full
// silence timeout is a dead-by-construction pair (sole dialer is the side
// that cannot initiate, e.g. public dialing a CGNAT non-dialer). On the
// old code the non-dialer waited forever and nothing reported it.
func TestNonDialerFallsBackAfterSilence(t *testing.T) {
	f := newFixture(t)
	f.eng.noSession = true
	if err := f.peers.Upsert(peer.Peer{ID: f.peerID, PublicKey: f.peerKey, Dialer: false}); err != nil {
		t.Fatal(err)
	}
	var events []string
	f.ctl.SetEventHandler(func(typ string, peerID uint64) {
		if peerID == f.peerID {
			events = append(events, typ)
		}
	})
	f.ctl.Tick()
	f.eng.mu.Lock()
	ensured := append([]uint64(nil), f.eng.ensured...)
	f.eng.mu.Unlock()
	for _, id := range ensured {
		if id == f.peerID {
			t.Fatal("non-dialer was dialed immediately, without any silence window")
		}
	}
	f.now = f.now.Add(DialerFallbackAfter + time.Second)
	f.ctl.Tick()
	f.eng.mu.Lock()
	ensured = append([]uint64(nil), f.eng.ensured...)
	fallback := append([]uint64(nil), f.eng.fallback...)
	f.eng.mu.Unlock()
	found := false
	for _, id := range ensured {
		if id == f.peerID {
			found = true
		}
	}
	if !found {
		t.Fatal("non-dialer never dialed after a full silence timeout: dead pair undetected")
	}
	found = false
	for _, id := range fallback {
		if id == f.peerID {
			found = true
		}
	}
	if !found {
		t.Fatal("fallback was never armed for the non-dialer peer")
	}
	found = false
	for _, e := range events {
		if e == "dialer_fallback" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no dialer_fallback event reported: %v", events)
	}
}

// TestLeaveGossipSelfAnnouncementIsTrusted is the M4 P8 regression test:
// a departure announced by the departed peer itself (STATE origin ==
// departed ID, session-authenticated) is applied immediately: the peer
// goes down, routes through/to it are withdrawn, and the session is
// dropped — and the tombstone is re-gossiped in the next STATE.
func TestLeaveGossipSelfAnnouncementIsTrusted(t *testing.T) {
	f := newFixture(t)
	peerID := f.peerID
	payload, err := discovery.Encode(discovery.State{
		Origin: peerID, Seq: 9, Expires: time.Now().Add(time.Minute),
		Peers:    []discovery.PeerRecord{{ID: peerID, PublicKey: f.peerKey, OverlayV4: netip.MustParseAddr("10.144.144.2")}},
		Departed: []uint64{peerID},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.ctl.HandleControl(peerID, netip.MustParseAddrPort("192.0.2.2:11010"), wire.State, payload)
	p, _ := f.peers.Get(peerID)
	if p.State != peer.StateDown {
		t.Fatalf("departed peer state = %v, want down", p.State)
	}
	if _, ok := f.routes.Lookup(peerID); ok {
		t.Fatal("route to departed peer still present")
	}
	f.eng.mu.Lock()
	dropped := append([]uint64(nil), f.eng.dropped...)
	f.eng.mu.Unlock()
	found := false
	for _, id := range dropped {
		if id == peerID {
			found = true
		}
	}
	if !found {
		t.Fatalf("session to departed peer was not dropped: %v", dropped)
	}
	// The tombstone is carried in this node's next STATE. Broadcasts
	// only go to Up peers, and the departed peer is now Down, so add
	// a second Up peer to receive it.
	other := mustIdentity(t)
	if err := f.peers.Upsert(peer.Peer{ID: other.ID, PublicKey: other.PublicKey, State: peer.StateUp}); err != nil {
		t.Fatal(err)
	}
	f.ctl.Tick()
	frames := f.eng.frames(wire.State)
	if len(frames) == 0 {
		t.Fatal("no STATE broadcast after learning a departure")
	}
	last := frames[len(frames)-1]
	st, err := discovery.Decode(last.body)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, id := range st.Departed {
		if id == peerID {
			found = true
		}
	}
	if !found {
		t.Fatalf("tombstone not re-gossiped: departed = %v", st.Departed)
	}
}

// TestLeaveGossipNeedsTwoOrigins: a re-gossiped departure (origin !=
// departed) from a single origin is pending only — one member must not
// be able to kill another peer's routes by forging a tombstone. A second
// distinct origin confirms it.
func TestLeaveGossipNeedsTwoOrigins(t *testing.T) {
	f := newFixture(t)
	peerID := f.peerID
	relayAIdent, relayBIdent := mustIdentity(t), mustIdentity(t)
	relayA, relayB := relayAIdent.ID, relayBIdent.ID
	mk := func(origin uint64, key []byte, seq uint64) []byte {
		t.Helper()
		payload, err := discovery.Encode(discovery.State{
			Origin: origin, Seq: seq, Expires: time.Now().Add(time.Minute),
			Peers:    []discovery.PeerRecord{{ID: origin, PublicKey: key, OverlayV4: netip.MustParseAddr("10.144.144.2")}},
			Departed: []uint64{peerID},
		})
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	// Note: the merger requires the sender to be a known peer for the
	// origin check only (origin == sender); peers are registered via
	// OnPeer only for unknown IDs with a valid key, and here the origin
	// IDs are synthetic, so use the peer's own keyed record path via
	// direct HandleControl: the merger accepts any origin that matches
	// the sender, regardless of peer-table membership.
	f.ctl.HandleControl(relayA, netip.MustParseAddrPort("192.0.2.9:11010"), wire.State, mk(relayA, relayAIdent.PublicKey, 1))
	p, _ := f.peers.Get(peerID)
	if p.State == peer.StateDown {
		t.Fatal("single re-gossiped tombstone was applied without corroboration")
	}
	f.ctl.HandleControl(relayB, netip.MustParseAddrPort("192.0.2.10:11010"), wire.State, mk(relayB, relayBIdent.PublicKey, 1))
	p, _ = f.peers.Get(peerID)
	if p.State != peer.StateDown {
		t.Fatalf("two-origin tombstone was not applied, state = %v", p.State)
	}
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
	var events []string
	f.ctl.SetEventHandler(func(typ string, peerID uint64) {
		if peerID == f.peerID {
			events = append(events, typ)
		}
	})
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
	f.eng.mu.Lock()
	dropped := append([]uint64(nil), f.eng.dropped...)
	f.eng.mu.Unlock()
	if len(dropped) != 1 || dropped[0] != f.peerID {
		t.Fatalf("session was not dropped: %v", dropped)
	}
	if len(events) != 1 || events[0] != "peer_down" {
		t.Fatalf("events = %v", events)
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

func TestPongRecordsTheRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.peers.SetActive(f.peerID, netip.MustParseAddrPort("192.0.2.2:11010"), 5*time.Millisecond, "udp", f.now)
	f.now = time.Unix(1_700_000_100, 0)
	payload := pingPayload(f.now.Add(-42 * time.Millisecond))
	f.ctl.HandleControl(f.peerID, netip.MustParseAddrPort("192.0.2.2:11010"), wire.Pong, payload)
	p, ok := f.peers.Get(f.peerID)
	if !ok {
		t.Fatal("peer missing")
	}
	if p.RTT != 42*time.Millisecond {
		t.Fatalf("RTT = %v, want 42ms", p.RTT)
	}
	r, ok := f.routes.Lookup(f.peerID)
	if !ok || r.Cost != 42*time.Millisecond {
		t.Fatalf("route cost = %+v, want 42ms", r)
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

func TestStateAdvertisesTheLocalName(t *testing.T) {
	f := newFixture(t)
	f.ctl.local.Name = "utsav-laptop"
	payload, err := f.ctl.statePayload(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, err := discovery.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Peers) == 0 || state.Peers[0].Name != "utsav-laptop" {
		t.Fatalf("state peers = %+v", state.Peers)
	}
}

func TestProbeWithoutASessionStartsAHandshake(t *testing.T) {
	f := newFixture(t)
	f.eng.noSession = true
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := f.ctl.Probe(ctx, f.peerID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe = %v, want the context deadline while waiting for a handshake", err)
	}
	f.eng.mu.Lock()
	ensured := append([]uint64(nil), f.eng.ensured...)
	dropped := append([]uint64(nil), f.eng.dropped...)
	f.eng.mu.Unlock()
	found := func(ids []uint64) bool {
		for _, id := range ids {
			if id == f.peerID {
				return true
			}
		}
		return false
	}
	if !found(ensured) || !found(dropped) {
		t.Fatalf("handshake was not started: ensured=%v dropped=%v", ensured, dropped)
	}
}

// TestDirectPeerPathIsProbedOnce proves the path-MTU probe runs once per
// direct connection, not on every control tick, and reports the result.
func TestDirectPeerPathIsProbedOnce(t *testing.T) {
	f := newFixture(t)
	f.peers.SetActive(f.peerID, netip.MustParseAddrPort("192.0.2.2:11010"), 5*time.Millisecond, "udp", f.now)

	type report struct {
		peer  uint64
		frame int
	}
	got := make(chan report, 2)
	f.ctl.SetMTUHandler(func(peerID uint64, frame int) { got <- report{peerID, frame} })

	f.ctl.Tick()
	f.ctl.Tick()

	select {
	case r := <-got:
		if r.peer != f.peerID || r.frame != wire.MaxFrameSize {
			t.Fatalf("report = %+v, want peer %d frame %d", r, f.peerID, wire.MaxFrameSize)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no MTU report after two ticks")
	}
	select {
	case r := <-got:
		t.Fatalf("second probe report: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}

	f.eng.mu.Lock()
	probes := len(f.eng.probed)
	f.eng.mu.Unlock()
	if probes != 1 {
		t.Fatalf("probes = %d, want 1", probes)
	}
}
