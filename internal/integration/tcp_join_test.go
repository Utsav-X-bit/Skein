package integration

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/dataplane"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/link"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/session"
)

// A fresh invite join must establish both sides over the TCP fallback, not
// merely mark the initiator up after receiving the responder's confirmation.
func TestUnknownPeerJoinsOverTCP(t *testing.T) {
	psk := make([]byte, identity.KeySize)
	type node struct {
		id       identity.Identity
		eng      *dataplane.Engine
		link     *link.Switch
		tun      *pipeTUN
		peers    *peer.Table
		sessions *session.Manager
		overlay  netip.Addr
	}
	start := func(overlay string) *node {
		id, err := identity.Generate()
		if err != nil {
			t.Fatal(err)
		}
		l, err := link.Listen(0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		if !l.HasTCP() {
			t.Fatal("TCP listener unavailable")
		}
		peers := peer.NewTable()
		sessions, err := session.NewManager(id, psk)
		if err != nil {
			t.Fatal(err)
		}
		tun := newPipeTUN()
		t.Cleanup(func() { _ = tun.Close() })
		ip := netip.MustParseAddr(overlay)
		eng, err := dataplane.New(dataplane.Config{LocalID: id.ID, Identity: id, OverlayV4: ip, PrefixV4: netip.MustParsePrefix("10.144.144.0/24"), MTU: 1280}, dataplane.Deps{TUN: tun, Link: l, Peers: peers, Routes: route.NewTable(id.ID), Sessions: sessions})
		if err != nil {
			t.Fatal(err)
		}
		return &node{id, eng, l, tun, peers, sessions, ip}
	}
	a, b := start("10.144.144.1"), start("10.144.144.2")
	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.link.Port())
	if err := a.peers.Upsert(peer.Peer{ID: b.id.ID, PublicKey: b.id.PublicKey, OverlayV4: b.overlay, Dialer: true, Candidates: []peer.Candidate{{Addr: addr, Source: peer.SourceConfig}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.sessions.SetPeer(b.id.ID, b.id.PublicKey); err != nil {
		t.Fatal(err)
	}
	b.sessions.SetAcceptUnknown(true)
	b.sessions.SetPeerLearnedHandler(func(id uint64, key []byte) {
		if err := b.peers.Upsert(peer.Peer{ID: id, PublicKey: key, OverlayV4: a.overlay}); err != nil {
			t.Error(err)
		}
	})
	if err := a.link.DialTCP(addr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = a.eng.Run(ctx) }()
	go func() { _ = b.eng.Run(ctx) }()
	if err := a.eng.EnsureSession(b.id.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return a.sessions.HasSession(b.id.ID) && b.sessions.HasSession(a.id.ID) && b.peers.Exists(a.id.ID)
	})
	for _, pair := range [][2]*node{{a, b}, {b, a}} {
		from, to := pair[0], pair[1]
		pkt := ipv4Packet(from.overlay, to.overlay, []byte("tcp invite join"))
		if err := from.eng.SendOverlayPacket(pkt); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-to.tun.out:
			if string(got[20:]) != "tcp invite join" {
				t.Fatalf("payload = %q", got[20:])
			}
		case <-time.After(time.Second):
			t.Fatal("overlay packet not delivered")
		}
	}
}
