package dataplane

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/session"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

// fakeTUN records written packets.
type fakeTUN struct {
	mu     sync.Mutex
	packet []byte
	writes int
}

func (f *fakeTUN) Read([]byte) (int, error) { return 0, errors.New("not used") }
func (f *fakeTUN) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packet = append([]byte(nil), p...)
	f.writes++
	return len(p), nil
}
func (f *fakeTUN) Name() string { return "fake" }
func (f *fakeTUN) Close() error { return nil }

func (f *fakeTUN) last() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.packet...)
}

// hub links test engines by address.
type hub struct {
	mu        sync.Mutex
	engines   map[netip.AddrPort]*Engine
	forwarded map[netip.AddrPort][][]byte
}

func newHub() *hub {
	return &hub{engines: map[netip.AddrPort]*Engine{}, forwarded: map[netip.AddrPort][][]byte{}}
}

func (h *hub) register(addr netip.AddrPort, e *Engine) { h.engines[addr] = e }

// link returns a Link bound to addr.
func (h *hub) link(addr netip.AddrPort) *testLink {
	return &testLink{hub: h, addr: addr}
}

type testLink struct {
	hub  *hub
	addr netip.AddrPort
}

func (l *testLink) Send(dst netip.AddrPort, payload []byte) error {
	l.hub.mu.Lock()
	e, ok := l.hub.engines[dst]
	l.hub.forwarded[dst] = append(l.hub.forwarded[dst], append([]byte(nil), payload...))
	l.hub.mu.Unlock()
	if !ok || e == nil {
		return errors.New("no such endpoint")
	}
	return e.HandleFrame(l.addr, payload)
}

func (l *testLink) Recv([]byte) (int, netip.AddrPort, error) {
	return 0, netip.AddrPort{}, errors.New("not used")
}
func (l *testLink) Observe() (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("unavailable")
}
func (l *testLink) LocalAddrs() []netip.AddrPort { return []netip.AddrPort{l.addr} }
func (l *testLink) Close() error                 { return nil }

type node struct {
	eng     *Engine
	tun     *fakeTUN
	addr    netip.AddrPort
	id      uint64
	ident   identity.Identity
	overlay netip.Addr
}

type testNet struct {
	hub   *hub
	psk   []byte
	nodes []*node
}

func newTestNet(t testing.TB) *testNet {
	t.Helper()
	psk := make([]byte, identity.KeySize)
	for i := range psk {
		psk[i] = byte(i + 1)
	}
	return &testNet{hub: newHub(), psk: psk}
}

// addNode creates an engine with the given overlay address and link address.
func (n *testNet) addNode(t testing.TB, overlay, addr string) *node {
	t.Helper()
	ident, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	linkAddr := netip.MustParseAddrPort(addr)
	overlayAddr := netip.MustParseAddr(overlay)
	peers := peer.NewTable()
	routes := route.NewTable(ident.ID)
	sessions, err := session.NewManager(ident, n.psk)
	if err != nil {
		t.Fatal(err)
	}
	tn := &fakeTUN{}
	eng, err := New(Config{
		LocalID:   ident.ID,
		Identity:  ident,
		OverlayV4: overlayAddr,
		PrefixV4:  netip.MustParsePrefix("10.144.144.0/24"),
		PrefixV6:  netip.MustParsePrefix("fd00:6d65:7368::/64"),
		MTU:       1280,
	}, Deps{
		TUN:      tn,
		Link:     n.hub.link(linkAddr),
		Peers:    peers,
		Routes:   routes,
		Sessions: sessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	nd := &node{eng: eng, tun: tn, addr: linkAddr, id: ident.ID, ident: ident, overlay: overlayAddr}
	n.hub.register(linkAddr, eng)
	n.nodes = append(n.nodes, nd)
	return nd
}

// know registers a peer on both sides with overlay addresses and endpoints.
func (n *testNet) know(t testing.TB, a, b *node, aDialer bool) {
	t.Helper()
	if err := a.eng.peers.Upsert(peer.Peer{
		ID: b.id, PublicKey: b.ident.PublicKey, OverlayV4: b.overlay, Dialer: aDialer,
		Candidates: []peer.Candidate{{Addr: b.addr, Source: peer.SourceConfig}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.sessions.SetPeer(b.id, b.ident.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := b.eng.peers.Upsert(peer.Peer{
		ID: a.id, PublicKey: a.ident.PublicKey, OverlayV4: a.overlay, Dialer: !aDialer,
		Candidates: []peer.Candidate{{Addr: a.addr, Source: peer.SourceConfig}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.eng.sessions.SetPeer(a.id, a.ident.PublicKey); err != nil {
		t.Fatal(err)
	}
}

func ipv4Packet(src, dst netip.Addr, payload []byte) []byte {
	pkt := make([]byte, 20+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 17
	copy(pkt[12:16], src.AsSlice())
	copy(pkt[16:20], dst.AsSlice())
	copy(pkt[20:], payload)
	return pkt
}

func TestDirectHandshakeAndData(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)

	pkt := ipv4Packet(a.overlay, b.overlay, []byte("hello"))
	// The first send starts the handshake and reports that no session exists yet.
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, ErrNoSession) {
		t.Fatalf("first send = %v, want ErrNoSession", err)
	}
	if !a.eng.sessions.HasSession(b.id) || !b.eng.sessions.HasSession(a.id) {
		t.Fatal("handshake did not complete on both sides")
	}
	if err := a.eng.SendOverlayPacket(pkt); err != nil {
		t.Fatalf("second send: %v", err)
	}
	got := b.tun.last()
	if !bytes.Equal(got, pkt) {
		t.Fatalf("delivered packet = %x, want %x", got, pkt)
	}
	if a.eng.Counters().TxPackets != 1 || b.eng.Counters().RxPackets != 1 {
		t.Fatalf("counters = %+v / %+v", a.eng.Counters(), b.eng.Counters())
	}
}

// TestExpiredSessionFailsClosed is the M4 P7 (F-04) regression test: an
// epoch past RekeyInterval must not keep carrying traffic while rekey
// fails. On the old code SendOverlayPacket sealed with the active session
// regardless of Expired(), so an expired key carried traffic indefinitely.
func TestExpiredSessionFailsClosed(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)
	pkt := ipv4Packet(a.overlay, b.overlay, []byte("hello"))
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, ErrNoSession) {
		t.Fatalf("first send = %v, want ErrNoSession", err)
	}
	if err := a.eng.SendOverlayPacket(pkt); err != nil {
		t.Fatalf("second send: %v", err)
	}
	// Age the session past the 1h epoch lifetime via the engine clock.
	a.eng.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired send = %v, want ErrExpired", err)
	}
	if got := a.eng.Counters().TxPackets; got != 1 {
		t.Fatalf("TxPackets = %d, want 1 (expired packet must not be sent)", got)
	}
	// Restoring the clock restores traffic: expiry pauses, it does not kill.
	a.eng.SetClock(time.Now)
	if err := a.eng.SendOverlayPacket(pkt); err != nil {
		t.Fatalf("send after clock restore: %v", err)
	}
}

// TestHandshakeRacesRouteAndDirect is the M4 P6 (F-08, route-only
// starvation) regression test: a stale relay route must not block direct
// dialing. On the old code sendHandshake sent only via the route when
// one existed, so a dead relay route starved direct candidates until the
// route expired.
func TestHandshakeRacesRouteAndDirect(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	c := n.addNode(t, "10.144.144.3", "192.0.2.3:11010")
	n.know(t, a, b, true)
	// A-B direct session, so B is a usable next hop for A's route table.
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("ab"))); !errors.Is(err, ErrNoSession) {
		t.Fatalf("A->B first send = %v", err)
	}
	// A knows C directly (candidate) but also has a stale route via B;
	// B has no route to C and cannot forward.
	n.know(t, a, c, true)
	merge(t, a.eng.routes, b.id, route.Advertisement{
		Destination: c.id, Cost: 2 * time.Millisecond, Hops: 1,
		Path: []uint64{b.id, c.id}, Seq: 1, Expires: time.Now().Add(time.Minute),
	})
	pkt := ipv4Packet(a.overlay, c.overlay, []byte("direct"))
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, ErrNoSession) {
		t.Fatalf("A->C first send = %v", err)
	}
	if !a.eng.sessions.HasSession(c.id) || !c.eng.sessions.HasSession(a.id) {
		t.Fatal("handshake did not complete via the direct candidate despite the stale relay route")
	}
	if err := a.eng.SendOverlayPacket(pkt); err != nil {
		t.Fatalf("A->C send: %v", err)
	}
	if got := c.tun.last(); !bytes.Equal(got, pkt) {
		t.Fatalf("delivered packet = %x, want %x", got, pkt)
	}
}

func TestRelayForwardsOpaqueCiphertext(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	c := n.addNode(t, "10.144.144.3", "192.0.2.3:11010")

	// Direct sessions A<->B and B<->C establish the direct links.
	n.know(t, a, b, true)
	n.know(t, b, c, true)
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("ab"))); !errors.Is(err, ErrNoSession) {
		t.Fatalf("A->B first send = %v", err)
	}
	if err := b.eng.SendOverlayPacket(ipv4Packet(b.overlay, c.overlay, []byte("bc"))); !errors.Is(err, ErrNoSession) {
		t.Fatalf("B->C first send = %v", err)
	}

	// B advertises C to A and A to C, so A and C have a two-hop route.
	merge(t, a.eng.routes, b.id, route.Advertisement{
		Destination: c.id, Cost: 2 * time.Millisecond, Hops: 1,
		Path: []uint64{b.id, c.id}, Seq: 1, Expires: time.Now().Add(time.Minute),
	})
	merge(t, c.eng.routes, b.id, route.Advertisement{
		Destination: a.id, Cost: 2 * time.Millisecond, Hops: 1,
		Path: []uint64{b.id, a.id}, Seq: 1, Expires: time.Now().Add(time.Minute),
	})
	// Both ends must know each other's identity and overlay address; that
	// knowledge normally arrives through STATE gossip.
	if err := a.eng.peers.Upsert(peer.Peer{ID: c.id, PublicKey: c.ident.PublicKey, OverlayV4: c.overlay, Dialer: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.sessions.SetPeer(c.id, c.ident.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := c.eng.peers.Upsert(peer.Peer{ID: a.id, PublicKey: a.ident.PublicKey, OverlayV4: a.overlay, Dialer: false}); err != nil {
		t.Fatal(err)
	}
	if err := c.eng.sessions.SetPeer(a.id, a.ident.PublicKey); err != nil {
		t.Fatal(err)
	}

	pkt := ipv4Packet(a.overlay, c.overlay, []byte("end to end"))
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, ErrNoSession) {
		t.Fatalf("A->C first send = %v, want ErrNoSession", err)
	}
	if !a.eng.sessions.HasSession(c.id) || !c.eng.sessions.HasSession(a.id) {
		t.Fatal("end-to-end handshake through the relay did not complete")
	}
	if err := a.eng.SendOverlayPacket(pkt); err != nil {
		t.Fatalf("A->C send: %v", err)
	}
	if got := c.tun.last(); !bytes.Equal(got, pkt) {
		t.Fatalf("relayed packet = %x, want %x", got, pkt)
	}
	if b.eng.Counters().Forwarded == 0 {
		t.Fatal("relay did not report a forwarded frame")
	}

	// The relay must not be able to decrypt the end-to-end payload: its session
	// with A uses different keys, and the AD names C as the final peer.
	// Frames delivered to the relay keep the relay as their next hop.
	n.hub.mu.Lock()
	frames := n.hub.forwarded[b.addr]
	n.hub.mu.Unlock()
	var dataFrame []byte
	for _, f := range frames {
		h, _, err := wire.Parse(f)
		if err == nil && h.Type == wire.Data && h.From == a.id && h.ToFinal == c.id {
			dataFrame = f
		}
	}
	if dataFrame == nil {
		t.Fatal("no relayed data frame observed")
	}
	h, payload, err := wire.Parse(dataFrame)
	if err != nil {
		t.Fatal(err)
	}
	epoch, counter, ct, err := wire.ParseNonce(payload)
	if err != nil {
		t.Fatal(err)
	}
	adArr := associatedData(wire.Data, c.id, a.id, epoch)
	ad := adArr[:]
	if _, err := b.eng.sessions.Open(a.id, epoch, counter, ad, ct); err == nil {
		t.Fatal("relay decrypted the end-to-end payload")
	}
	if h.ToNext != b.id {
		t.Fatalf("relay frame to_next = %016x, want %016x", h.ToNext, b.id)
	}
}

func merge(t *testing.T, tbl *route.Table, from uint64, adv route.Advertisement) {
	t.Helper()
	if err := tbl.MergeAdvertisement(from, adv); err != nil {
		t.Fatalf("MergeAdvertisement: %v", err)
	}
}

func TestFrameForAnotherNodeIsRejected(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("x"))); !errors.Is(err, ErrNoSession) {
		t.Fatal(err)
	}
	frame, err := wire.Marshal(wire.Header{Type: wire.Handshake, ToNext: 0xdead, ToFinal: b.id, From: a.id, TTL: 4}, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.eng.HandleFrame(a.addr, frame); !errors.Is(err, ErrNotForUs) {
		t.Fatalf("err = %v, want ErrNotForUs", err)
	}
}

func TestTamperedFinalPeerIsRejected(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("x"))); !errors.Is(err, ErrNoSession) {
		t.Fatal(err)
	}

	// Capture the next data frame and tamper with to_final.
	var captured []byte
	n.hub.mu.Lock()
	n.hub.engines[b.addr] = nil
	n.hub.mu.Unlock()
	defer func() {
		n.hub.mu.Lock()
		n.hub.engines[b.addr] = b.eng
		n.hub.mu.Unlock()
	}()
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("x"))); err == nil {
		t.Fatal("expected send to fail with no endpoint")
	}
	n.hub.mu.Lock()
	frames := n.hub.forwarded[b.addr]
	n.hub.mu.Unlock()
	if len(frames) == 0 {
		t.Fatal("no frame captured")
	}
	captured = frames[len(frames)-1]
	h, payload, err := wire.Parse(captured)
	if err != nil {
		t.Fatal(err)
	}
	if h.Type != wire.Data {
		t.Fatalf("captured type %d", h.Type)
	}
	// Re-address the frame to a different final peer: authentication must fail.
	tampered, err := wire.Marshal(wire.Header{Type: h.Type, ToNext: b.id, ToFinal: h.From, From: h.From, TTL: 4}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.eng.HandleFrame(a.addr, tampered); err == nil {
		t.Fatal("expected tampered to_final to be rejected")
	}
}

func TestUnknownOverlayDestinationIsRejected(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, netip.MustParseAddr("10.144.144.99"), []byte("x"))); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("err = %v, want ErrUnknownPeer", err)
	}
	if err := a.eng.SendOverlayPacket(ipv4Packet(netip.MustParseAddr("10.144.144.99"), a.overlay, []byte("x"))); !errors.Is(err, ErrBadPacket) {
		t.Fatalf("spoofed source err = %v, want ErrBadPacket", err)
	}
	if err := a.eng.SendOverlayPacket([]byte{0x00}); !errors.Is(err, ErrBadPacket) {
		t.Fatalf("short packet err = %v", err)
	}
}

func TestPunchStartsAHandshakeAtTheGivenAddress(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)

	if err := a.eng.Punch(b.id, b.addr); err != nil {
		t.Fatalf("Punch: %v", err)
	}
	if !a.eng.sessions.HasSession(b.id) || !b.eng.sessions.HasSession(a.id) {
		t.Fatal("punch did not establish a session")
	}
}

// BenchmarkEngineThroughput measures the seal/send/open/deliver path.
func BenchmarkEngineThroughput(b *testing.B) {
	n := newTestNet(b)
	a := n.addNode(b, "10.144.144.1", "192.0.2.1:11010")
	bb := n.addNode(b, "10.144.144.2", "192.0.2.2:11010")
	n.know(b, a, bb, true)

	pkt := ipv4Packet(a.overlay, bb.overlay, make([]byte, 1240))
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, ErrNoSession) {
		b.Fatalf("first send = %v", err)
	}
	if err := a.eng.SendOverlayPacket(pkt); err != nil {
		b.Fatalf("send: %v", err)
	}
	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.eng.SendOverlayPacket(pkt); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDeliverRejectsShortAndSpoofedPackets(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "127.0.0.1:10001")
	b := n.addNode(t, "10.144.144.2", "127.0.0.1:10002")
	n.know(t, a, b, true)

	if err := a.eng.deliver(b.id, nil); !errors.Is(err, ErrBadPacket) {
		t.Fatalf("empty packet: got %v, want ErrBadPacket", err)
	}
	if err := a.eng.deliver(b.id, []byte{0x45}); !errors.Is(err, ErrBadPacket) {
		t.Fatalf("short packet: got %v, want ErrBadPacket", err)
	}

	toOther := ipv4Packet(b.overlay, netip.MustParseAddr("10.144.144.99"), []byte("x"))
	if err := a.eng.deliver(b.id, toOther); !errors.Is(err, ErrBadPacket) {
		t.Fatalf("packet for another overlay: got %v, want ErrBadPacket", err)
	}
	if a.tun.writes != 0 {
		t.Fatalf("deliver wrote %d packets, want 0", a.tun.writes)
	}
}

func TestRelayedSessionStillNotifiesAndKeepsRoute(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "127.0.0.1:10001")
	b := n.addNode(t, "10.144.144.2", "127.0.0.1:10002")
	relay := n.addNode(t, "10.144.144.3", "127.0.0.1:10003")

	if err := a.eng.peers.Upsert(peer.Peer{ID: b.id, PublicKey: b.ident.PublicKey, OverlayV4: b.overlay}); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.peers.Upsert(peer.Peer{
		ID: relay.id, PublicKey: relay.ident.PublicKey, OverlayV4: relay.overlay,
		Candidates: []peer.Candidate{{Addr: relay.addr, Source: peer.SourceConfig}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.routes.UpdateDirect(route.DirectLink{PeerID: relay.id, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.routes.MergeAdvertisement(relay.id, route.Advertisement{
		Destination: b.id,
		Cost:        2 * time.Millisecond,
		Path:        []uint64{relay.id, b.id},
		Expires:     time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	notified := false
	a.eng.SetSessionHandler(func(uint64) { notified = true })
	a.eng.established(b.id, relay.addr)

	if !notified {
		t.Fatal("relayed session did not notify the session handler")
	}
	r, ok := a.eng.routes.Lookup(b.id)
	if !ok || r.NextHop != relay.id {
		t.Fatalf("route = %+v, want next hop %016x", r, relay.id)
	}
	if p, _ := a.eng.peers.Get(b.id); p.ActiveEndpoint.IsValid() {
		t.Fatalf("relayed session set a direct endpoint %s", p.ActiveEndpoint)
	}
}

func TestDirectSourceRejectsRelayAddress(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "127.0.0.1:10001")
	b := n.addNode(t, "10.144.144.2", "127.0.0.1:10002")
	relay := n.addNode(t, "10.144.144.3", "127.0.0.1:10003")

	for _, p := range []*node{b, relay} {
		if err := a.eng.peers.Upsert(peer.Peer{
			ID: p.id, PublicKey: p.ident.PublicKey, OverlayV4: p.overlay,
			Candidates: []peer.Candidate{{Addr: p.addr, Source: peer.SourceConfig}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !a.eng.directSource(b.id, b.addr) {
		t.Fatal("peer's own address should be a direct source")
	}
	if a.eng.directSource(b.id, relay.addr) {
		t.Fatal("relay address must not be treated as the peer's direct source")
	}
	if !a.eng.directSource(b.id, netip.MustParseAddrPort("127.0.0.1:19999")) {
		t.Fatal("unknown address should be accepted as first contact")
	}
}

func TestPunchFrameReachesTheControlHandler(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("x"))); !errors.Is(err, ErrNoSession) {
		t.Fatalf("handshake: %v", err)
	}

	var mu sync.Mutex
	var got wire.MessageType
	b.eng.SetControlHandler(func(_ uint64, _ netip.AddrPort, typ wire.MessageType, _ []byte) {
		mu.Lock()
		got = typ
		mu.Unlock()
	})
	if err := a.eng.SendControl(b.id, wire.Punch, []byte("punch")); err != nil {
		t.Fatalf("send punch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got != wire.Punch {
		t.Fatalf("control handler got %d, want Punch", got)
	}
}

func TestDirectHandshakeUpgradesARelayedRoute(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	relay := n.addNode(t, "10.144.144.3", "192.0.2.3:11010")

	for _, p := range []*node{b, relay} {
		if err := a.eng.peers.Upsert(peer.Peer{
			ID: p.id, PublicKey: p.ident.PublicKey, OverlayV4: p.overlay,
			Candidates: []peer.Candidate{{Addr: p.addr, Source: peer.SourceConfig}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.eng.routes.UpdateDirect(route.DirectLink{PeerID: relay.id, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.routes.MergeAdvertisement(relay.id, route.Advertisement{
		Destination: b.id, Cost: 2 * time.Millisecond,
		Path: []uint64{relay.id, b.id}, Expires: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	a.eng.established(b.id, b.addr)
	r, ok := a.eng.routes.Lookup(b.id)
	if !ok || r.NextHop != b.id {
		t.Fatalf("route = %+v, want direct to %016x", r, b.id)
	}
	p, _ := a.eng.peers.Get(b.id)
	if p.ActiveEndpoint != b.addr {
		t.Fatalf("active endpoint = %s, want %s", p.ActiveEndpoint, b.addr)
	}
}

func ipv6Packet(src, dst netip.Addr, payload []byte) []byte {
	pkt := make([]byte, 40+len(payload))
	pkt[0] = 0x60
	binary.BigEndian.PutUint16(pkt[4:6], uint16(len(payload)))
	src16, dst16 := src.As16(), dst.As16()
	copy(pkt[8:24], src16[:])
	copy(pkt[24:40], dst16[:])
	copy(pkt[40:], payload)
	return pkt
}

func TestIPv6PayloadLength(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	src := netip.MustParseAddr("fd00:6d65:7368::1")
	dst := netip.MustParseAddr("fd00:6d65:7368::2")
	a.eng.cfg.OverlayV6 = src
	a.eng.cfg.PrefixV6 = netip.MustParsePrefix("fd00:6d65:7368::/64")
	if err := a.eng.peers.Upsert(peer.Peer{ID: b.id, PublicKey: b.ident.PublicKey, OverlayV6: dst}); err != nil {
		t.Fatal(err)
	}

	// A 20-byte payload (a bare TCP ACK) is valid, not "shorter than 40".
	pkt := ipv6Packet(src, dst, make([]byte, 20))
	if _, err := a.eng.validateOutbound(pkt); err != nil {
		t.Fatalf("60-byte IPv6 packet rejected: %v", err)
	}
	in := ipv6Packet(dst, src, make([]byte, 20))
	if err := a.eng.deliver(b.id, in); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := a.tun.last(); len(got) != len(in) {
		t.Fatalf("delivered %d bytes, want %d", len(got), len(in))
	}
}

func TestDropSessionKeepsThePeerRegistered(t *testing.T) {
	n := newTestNet(t)
	a := n.addNode(t, "10.144.144.1", "192.0.2.1:11010")
	b := n.addNode(t, "10.144.144.2", "192.0.2.2:11010")
	n.know(t, a, b, true)
	if err := a.eng.SendOverlayPacket(ipv4Packet(a.overlay, b.overlay, []byte("x"))); !errors.Is(err, ErrNoSession) {
		t.Fatalf("handshake: %v", err)
	}
	a.eng.DropSession(b.id)
	if err := a.eng.EnsureSession(b.id); err != nil {
		t.Fatalf("reconnect after drop: %v", err)
	}
	if !a.eng.sessions.HasSession(b.id) {
		t.Fatal("reconnect did not establish a session")
	}
}

func TestNewRejectsMTUAboveFrameLimit(t *testing.T) {
	ident, _ := identity.Generate()
	_, err := New(Config{
		LocalID: ident.ID, Identity: ident, MTU: 1500,
		OverlayV4: netip.MustParseAddr("10.144.144.1"),
		PrefixV4:  netip.MustParsePrefix("10.144.144.0/24"),
	}, Deps{})
	if err == nil {
		t.Fatal("MTU above the frame limit was accepted")
	}
}
