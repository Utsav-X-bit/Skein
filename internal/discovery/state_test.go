package discovery

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/Utsav-X-bit/skein/internal/activity"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
)

func testIdentity(t *testing.T) identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStateRoundTrip(t *testing.T) {
	a := testIdentity(t)
	b := testIdentity(t)
	s := State{
		Origin:  a.ID,
		Seq:     7,
		Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{
			{ID: a.ID, PublicKey: a.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.1"), OverlayV6: netip.MustParseAddr("fd00::1"),
				Candidates: []netip.AddrPort{netip.MustParseAddrPort("203.0.113.5:11010")}},
			{ID: b.ID, PublicKey: b.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.2")},
		},
		Routes: []RouteRecord{
			{Destination: b.ID, Cost: 12 * time.Millisecond, Hops: 1, Path: []uint64{b.ID}},
		},
		Activity: []activity.Record{{Destination: b.ID, RxBytes: 4096, TxBytes: 2048, RTT: 7 * time.Millisecond, Jitter: time.Millisecond, Transport: activity.TransportUDP, Direct: true, ObservedAt: time.Now(), Path: []uint64{b.ID}}},
	}
	payload, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Origin != s.Origin || got.Seq != s.Seq {
		t.Fatalf("origin/seq = %016x/%d", got.Origin, got.Seq)
	}
	if len(got.Peers) != 2 || got.Peers[0].ID != a.ID || got.Peers[1].ID != b.ID {
		t.Fatalf("peers = %+v", got.Peers)
	}
	if got.Peers[0].OverlayV6 != netip.MustParseAddr("fd00::1") {
		t.Fatalf("ipv6 = %s", got.Peers[0].OverlayV6)
	}
	if len(got.Routes) != 1 || got.Routes[0].Cost != 12*time.Millisecond || got.Routes[0].Path[0] != b.ID {
		t.Fatalf("routes = %+v", got.Routes)
	}
	if len(got.Activity) != 1 || got.Activity[0].Destination != b.ID || got.Activity[0].RxBytes != 4096 {
		t.Fatalf("activity = %+v", got.Activity)
	}
}

func TestStateRejectsMalformed(t *testing.T) {
	a := testIdentity(t)
	valid, err := Encode(State{
		Origin: a.ID, Seq: 1, Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{ID: a.ID, PublicKey: a.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"empty":       {},
		"bad version": append([]byte{9}, valid[1:]...),
		"truncated":   valid[:len(valid)-1],
		"trailing":    append(append([]byte(nil), valid...), 0),
		"zero origin": func() []byte {
			b := append([]byte(nil), valid...)
			for i := 1; i < 9; i++ {
				b[i] = 0
			}
			return b
		}(),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(payload); err == nil {
				t.Fatal("expected decode error")
			}
		})
	}
}

func TestBuildStaysWithinTheFrameCap(t *testing.T) {
	a := testIdentity(t)
	var routes []RouteRecord
	for i := range 20 {
		dest := uint64(i + 2)
		routes = append(routes, RouteRecord{
			Destination: dest, Cost: time.Duration(i) * time.Millisecond, Hops: 1,
			Path: []uint64{dest},
		})
	}
	var peers []PeerRecord
	for i := range 10 {
		peers = append(peers, PeerRecord{ID: uint64(i + 2), OverlayV4: netip.MustParseAddr("10.144.144.1")})
	}
	s, err := Build([]PeerRecord{{ID: a.ID, PublicKey: a.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.9")}}, routes, nil, 1, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	payload, err := Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(payload) > maxStateBytes {
		t.Fatalf("state is %d bytes, cap %d", len(payload), maxStateBytes)
	}
	if len(s.Routes) == 0 {
		t.Fatal("Build dropped every route")
	}
	if s.Peers[0].ID != a.ID {
		t.Fatal("Build dropped the local record")
	}
}

func TestMergerAppliesPeersAndRoutes(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	other := testIdentity(t)

	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	if err := peers.Upsert(peer.Peer{ID: origin.ID, PublicKey: origin.PublicKey, Dialer: true}); err != nil {
		t.Fatal(err)
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	// A configured peer that gossip may add hints to but never replace.
	if err := peers.Upsert(peer.Peer{ID: other.ID, PublicKey: other.PublicKey, Dialer: false}); err != nil {
		t.Fatal(err)
	}

	payload, err := Encode(State{
		Origin:  origin.ID,
		Seq:     1,
		Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{
			{ID: other.ID, PublicKey: other.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.7"),
				Candidates: []netip.AddrPort{netip.MustParseAddrPort("198.51.100.9:11010")}},
		},
		Routes: []RouteRecord{
			{Destination: other.ID, Cost: 5 * time.Millisecond, Hops: 1, Path: []uint64{other.ID}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)
	if err := m.Merge(origin.ID, payload); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	p, ok := peers.Get(other.ID)
	if !ok || len(p.Candidates) != 1 || p.Candidates[0].Source != peer.SourceState {
		t.Fatalf("candidate not merged: %+v", p)
	}
	r, ok := routes.Lookup(other.ID)
	if !ok || r.NextHop != origin.ID {
		t.Fatalf("route not merged: %+v ok=%v", r, ok)
	}
}

func TestMergerLearnsOverlayAddresses(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	other := testIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	for _, id := range []identity.Identity{origin, other} {
		if err := peers.Upsert(peer.Peer{ID: id.ID, PublicKey: id.PublicKey}); err != nil {
			t.Fatal(err)
		}
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	payload, err := Encode(State{
		Origin:  origin.ID,
		Seq:     1,
		Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{
			{ID: other.ID, PublicKey: other.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.7"), OverlayV6: netip.MustParseAddr("fd00:6d65:7368::7")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)
	if err := m.Merge(origin.ID, payload); err != nil {
		t.Fatal(err)
	}
	p, _ := peers.Get(other.ID)
	if p.OverlayV4.String() != "10.144.144.7" || p.OverlayV6.String() != "fd00:6d65:7368::7" {
		t.Fatalf("overlay addresses not learned: %+v", p)
	}
	if id, ok := peers.LookupOverlay(netip.MustParseAddr("10.144.144.7")); !ok || id != other.ID {
		t.Fatal("overlay lookup does not resolve the learned address")
	}
}

func TestMergerRejectsSpoofedAndStaleState(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	attacker := testIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	if err := peers.Upsert(peer.Peer{ID: origin.ID, PublicKey: origin.PublicKey}); err != nil {
		t.Fatal(err)
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)

	payload, err := Encode(State{Origin: origin.ID, Seq: 5, Expires: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(attacker.ID, payload); err == nil {
		t.Fatal("expected an origin mismatch to be rejected")
	}
	if err := m.Merge(origin.ID, payload); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if err := m.Merge(origin.ID, payload); err == nil {
		t.Fatal("expected a replayed sequence to be rejected")
	}
	expired, err := Encode(State{Origin: origin.ID, Seq: 9, Expires: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(origin.ID, expired); err == nil {
		t.Fatal("expected expired state to be rejected")
	}
}

func TestMergerAcceptsFreshStateAfterOriginSessionRestart(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	if err := peers.Upsert(peer.Peer{
		ID:        origin.ID,
		PublicKey: origin.PublicKey,
		OverlayV4: netip.MustParseAddr("10.144.144.2"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)

	oldState, err := Encode(State{
		Origin:  origin.ID,
		Seq:     42,
		Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{
			ID: origin.ID, PublicKey: origin.PublicKey,
			OverlayV4: netip.MustParseAddr("10.144.144.2"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(origin.ID, oldState); err != nil {
		t.Fatalf("initial Merge: %v", err)
	}

	restartedState, err := Encode(State{
		Origin:  origin.ID,
		Seq:     1, // A daemon restart resets its controller sequence.
		Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{
			ID: origin.ID, PublicKey: origin.PublicKey,
			OverlayV4: netip.MustParseAddr("10.144.144.241"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(origin.ID, restartedState); err == nil {
		t.Fatal("expected lower state sequence to be rejected before a new session")
	}

	// A fresh authenticated session proves the peer has restarted, so its new
	// state sequence is authoritative rather than a replay from the old session.
	m.Reset(origin.ID)
	if err := m.Merge(origin.ID, restartedState); err != nil {
		t.Fatalf("Merge after session restart: %v", err)
	}
	p, ok := peers.Get(origin.ID)
	if !ok || p.OverlayV4 != netip.MustParseAddr("10.144.144.241") {
		t.Fatalf("origin overlay was not corrected after restart: %+v", p)
	}
}

func TestMDNSTXTRoundTrip(t *testing.T) {
	id := testIdentity(t)
	inv := identity.Invite{
		ID:        id.ID,
		PublicKey: id.PublicKey,
		Addrs:     []netip.AddrPort{netip.MustParseAddrPort("192.0.2.10:11010")},
	}
	fp := Fingerprint(bytes.Repeat([]byte{0x42}, 32))
	txt, err := EncodeTXT(inv, fp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTXT(txt, fp)
	if err != nil {
		t.Fatalf("DecodeTXT: %v", err)
	}
	if got.ID != inv.ID || len(got.Addrs) != 1 || got.Addrs[0] != inv.Addrs[0] {
		t.Fatalf("decoded invite = %+v", got)
	}
	if _, err := DecodeTXT(txt, Fingerprint([]byte("other network secret material"))); err == nil {
		t.Fatal("expected a network tag mismatch to be rejected")
	}
}

func TestMDNSEntryFallsBackToServiceAddress(t *testing.T) {
	id := testIdentity(t)
	fp := Fingerprint(bytes.Repeat([]byte{0x24}, 32))
	entry := &zeroconf.ServiceEntry{
		Port:     11010,
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.44")},
	}
	txt, err := EncodeTXT(identity.Invite{ID: id.ID, PublicKey: id.PublicKey}, fp)
	if err != nil {
		t.Fatal(err)
	}
	entry.Text = txt
	got, err := DecodeEntry(entry, fp)
	if err != nil {
		t.Fatalf("DecodeEntry: %v", err)
	}
	if len(got.Addrs) != 1 || got.Addrs[0].Addr().String() != "192.0.2.44" {
		t.Fatalf("addrs = %v", got.Addrs)
	}
}

func TestStateCarriesPeerNames(t *testing.T) {
	a := testIdentity(t)
	b := testIdentity(t)
	payload, err := Encode(State{
		Origin: a.ID, Seq: 1, Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{
			{ID: a.ID, Name: "utsav-laptop", PublicKey: a.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.1")},
			{ID: b.ID, Name: "yoga", PublicKey: b.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.2")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.Peers[0].Name != "utsav-laptop" || got.Peers[1].Name != "yoga" {
		t.Fatalf("names = %q %q", got.Peers[0].Name, got.Peers[1].Name)
	}
}

func TestStateV1WithoutNamesStillDecodes(t *testing.T) {
	origin := testIdentity(t)
	payload := []byte{1}
	payload = binary.BigEndian.AppendUint64(payload, origin.ID)
	payload = binary.BigEndian.AppendUint64(payload, 1)  // seq
	payload = binary.BigEndian.AppendUint32(payload, 60) // ttl
	payload = append(payload, 1)                         // one peer
	payload = binary.BigEndian.AppendUint64(payload, origin.ID)
	payload = append(payload, 0) // no addresses
	payload = append(payload, 0) // no candidates
	payload = append(payload, 0) // no routes
	got, err := Decode(payload)
	if err != nil {
		t.Fatalf("v1 decode: %v", err)
	}
	if len(got.Peers) != 1 || got.Peers[0].Name != "" {
		t.Fatalf("v1 peers = %+v", got.Peers)
	}
}

func TestMergerLearnsNames(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	other := testIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	for _, id := range []identity.Identity{origin, other} {
		if err := peers.Upsert(peer.Peer{ID: id.ID, PublicKey: id.PublicKey}); err != nil {
			t.Fatal(err)
		}
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	payload, err := Encode(State{
		Origin: origin.ID, Seq: 1, Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{ID: other.ID, Name: "yoga", PublicKey: other.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.2")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)
	if err := m.Merge(origin.ID, payload); err != nil {
		t.Fatal(err)
	}
	p, _ := peers.Get(other.ID)
	if p.Name != "yoga" {
		t.Fatalf("name = %q", p.Name)
	}
}

func TestStateCarriesPeerKeys(t *testing.T) {
	a := testIdentity(t)
	b := testIdentity(t)
	payload, err := Encode(State{
		Origin: a.ID, Seq: 1, Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{ID: b.ID, PublicKey: b.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.2")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Peers[0].PublicKey, b.PublicKey) {
		t.Fatal("public key did not round-trip")
	}

	// A key that does not hash to the claimed ID is rejected on decode.
	idx := bytes.Index(payload, b.PublicKey)
	if idx < 0 {
		t.Fatal("key not found in payload")
	}
	tampered := append([]byte(nil), payload...)
	tampered[idx] ^= 0xff
	if _, err := Decode(tampered); err == nil {
		t.Fatal("expected a mismatched key to be rejected")
	}
}

func TestMergerRegistersUnknownPeersFromGossip(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	stranger := testIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	if err := peers.Upsert(peer.Peer{ID: origin.ID, PublicKey: origin.PublicKey}); err != nil {
		t.Fatal(err)
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	payload, err := Encode(State{
		Origin: origin.ID, Seq: 1, Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{
			ID: stranger.ID, PublicKey: stranger.PublicKey, Name: "nas",
			OverlayV4: netip.MustParseAddr("10.144.144.3"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)
	var learnedID uint64
	m.OnPeer = func(id uint64, key []byte) error {
		learnedID = id
		return peers.Upsert(peer.Peer{ID: id, PublicKey: key})
	}
	if err := m.Merge(origin.ID, payload); err != nil {
		t.Fatal(err)
	}
	if learnedID != stranger.ID {
		t.Fatalf("learned %016x, want %016x", learnedID, stranger.ID)
	}
	p, ok := peers.Get(stranger.ID)
	if !ok || p.Name != "nas" || p.OverlayV4.String() != "10.144.144.3" {
		t.Fatalf("peer = %+v", p)
	}
}

func TestMergerSkipsPeersWhenRegistrationFails(t *testing.T) {
	local := testIdentity(t)
	origin := testIdentity(t)
	stranger := testIdentity(t)
	peers := peer.NewTable()
	routes := route.NewTable(local.ID)
	if err := peers.Upsert(peer.Peer{ID: origin.ID, PublicKey: origin.PublicKey}); err != nil {
		t.Fatal(err)
	}
	if err := routes.UpdateDirect(route.DirectLink{PeerID: origin.ID, RTT: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	payload, err := Encode(State{
		Origin: origin.ID, Seq: 1, Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{ID: stranger.ID, PublicKey: stranger.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.3")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMerger(local.ID, peers, routes)
	m.OnPeer = func(uint64, []byte) error { return errors.New("full") }
	if err := m.Merge(origin.ID, payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := peers.Get(stranger.ID); ok {
		t.Fatal("peer was registered despite the error")
	}
}
