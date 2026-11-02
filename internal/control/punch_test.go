package control

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/discovery"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

func TestPunchEncoding(t *testing.T) {
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	req := punchRequest{Target: 0x0102030405060708, Nonce: nonce}
	got, err := decodePunch(encodePunch(req))
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if got.Target != req.Target || got.Nonce != nonce || got.HasAddr {
		t.Fatalf("request roundtrip = %+v", got)
	}

	delivery := punchRequest{
		Target: 0x1112131415161718, Nonce: nonce,
		Addr: netip.MustParseAddrPort("198.51.100.7:11010"), HasAddr: true,
	}
	got, err = decodePunch(encodePunch(delivery))
	if err != nil {
		t.Fatalf("decode delivery: %v", err)
	}
	if got.Addr != delivery.Addr || !got.HasAddr || got.Target != delivery.Target {
		t.Fatalf("delivery roundtrip = %+v", got)
	}

	v6 := punchRequest{Target: 1, Addr: netip.MustParseAddrPort("[2001:db8::5]:11010"), HasAddr: true}
	got, err = decodePunch(encodePunch(v6))
	if err != nil || got.Addr != v6.Addr {
		t.Fatalf("ipv6 delivery = %+v err=%v", got, err)
	}

	if _, err := decodePunch([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected a short payload to be rejected")
	}
}

func punchFixture(t *testing.T) (*Controller, *fakeTransport, identity.Identity, identity.Identity) {
	t.Helper()
	local := mustIdentity(t)
	a := mustIdentity(t)
	c := mustIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	for _, id := range []identity.Identity{a, c} {
		if err := peers.Upsert(peer.Peer{ID: id.ID, PublicKey: id.PublicKey, State: peer.StateUp}); err != nil {
			t.Fatal(err)
		}
		if err := routes.UpdateDirect(route.DirectLink{PeerID: id.ID, RTT: time.Millisecond}); err != nil {
			t.Fatal(err)
		}
	}
	eng := &fakeTransport{}
	ctl, err := New(Local{ID: local.ID, OverlayV4: netip.MustParseAddr("10.144.144.1")},
		peers, routes, discovery.NewMerger(local.ID, peers, routes), eng)
	if err != nil {
		t.Fatal(err)
	}
	return ctl, eng, a, c
}

func TestPunchRelayIntroducesBothSides(t *testing.T) {
	ctl, eng, a, c := punchFixture(t)
	aAddr := netip.MustParseAddrPort("203.0.113.5:11010")
	cAddr := netip.MustParseAddrPort("198.51.100.7:11010")
	// The relay learns each peer's observed endpoint from its control traffic.
	ctl.HandleControl(a.ID, aAddr, wire.Ping, []byte("ping"))
	ctl.HandleControl(c.ID, cAddr, wire.Ping, []byte("ping"))

	ctl.HandleControl(a.ID, aAddr, wire.Punch, encodePunch(punchRequest{Target: c.ID}))

	frames := eng.frames(wire.Punch)
	if len(frames) != 2 {
		t.Fatalf("expected two introductions, got %d", len(frames))
	}
	seen := map[uint64]netip.AddrPort{}
	for _, f := range frames {
		req, err := decodePunch(f.body)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !req.HasAddr {
			t.Fatalf("relay sent a request instead of a delivery: %+v", req)
		}
		seen[f.peerID] = req.Addr
	}
	if seen[a.ID] != cAddr {
		t.Fatalf("a was told %s, want %s", seen[a.ID], cAddr)
	}
	if seen[c.ID] != aAddr {
		t.Fatalf("c was told %s, want %s", seen[c.ID], aAddr)
	}
}

func TestPunchDeliveryTriggersADial(t *testing.T) {
	ctl, eng, a, _ := punchFixture(t)
	addr := netip.MustParseAddrPort("198.51.100.9:11010")
	ctl.HandleControl(a.ID, netip.MustParseAddrPort("203.0.113.5:11010"), wire.Punch,
		encodePunch(punchRequest{Target: a.ID, Addr: addr, HasAddr: true}))
	eng.mu.Lock()
	calls := append([]punchCall(nil), eng.punched...)
	eng.mu.Unlock()
	if len(calls) != 1 || calls[0].peer != a.ID || calls[0].addr != addr {
		t.Fatalf("punch calls = %+v", calls)
	}
}

func TestPunchRequestIsIgnoredWithoutAnObservedTarget(t *testing.T) {
	ctl, eng, a, c := punchFixture(t)
	ctl.HandleControl(a.ID, netip.MustParseAddrPort("203.0.113.5:11010"), wire.Ping, []byte("ping"))
	ctl.HandleControl(a.ID, netip.MustParseAddrPort("203.0.113.5:11010"), wire.Punch,
		encodePunch(punchRequest{Target: c.ID}))
	if frames := eng.frames(wire.Punch); len(frames) != 0 {
		t.Fatalf("expected no introduction without the target's endpoint, got %d", len(frames))
	}
}

func TestPunchDeliveryTriesEveryCandidate(t *testing.T) {
	f := newFixture(t)
	candA := netip.MustParseAddrPort("198.51.100.7:11010")
	candB := netip.MustParseAddrPort("[2001:db8::7]:11010")
	f.peers.AddCandidate(f.peerID, candA, peer.SourceState)
	f.peers.AddCandidate(f.peerID, candB, peer.SourceState)

	observed := netip.MustParseAddrPort("203.0.113.9:40000")
	payload := encodePunch(punchRequest{Target: f.peerID, Addr: observed, HasAddr: true})
	f.ctl.HandleControl(0x1111, netip.MustParseAddrPort("192.0.2.9:11010"), wire.Punch, payload)

	f.eng.mu.Lock()
	punched := append([]punchCall(nil), f.eng.punched...)
	f.eng.mu.Unlock()
	got := map[netip.AddrPort]bool{}
	for _, call := range punched {
		if call.peer == f.peerID {
			got[call.addr] = true
		}
	}
	for _, want := range []netip.AddrPort{observed, candA, candB} {
		if !got[want] {
			t.Fatalf("punch did not try %s: %v", want, punched)
		}
	}
}
