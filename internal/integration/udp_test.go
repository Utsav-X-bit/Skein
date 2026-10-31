// Package integration exercises the full stack over real loopback UDP sockets.
package integration

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/Utsav-X-Bit/skein/internal/control"
	"github.com/Utsav-X-Bit/skein/internal/dataplane"
	"github.com/Utsav-X-Bit/skein/internal/discovery"
	"github.com/Utsav-X-Bit/skein/internal/identity"
	"github.com/Utsav-X-Bit/skein/internal/link"
	"github.com/Utsav-X-Bit/skein/internal/peer"
	"github.com/Utsav-X-Bit/skein/internal/route"
	"github.com/Utsav-X-Bit/skein/internal/session"
)

// pipeTUN is an in-memory TUN device.
type pipeTUN struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
}

func newPipeTUN() *pipeTUN {
	return &pipeTUN{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
}

func (p *pipeTUN) Read(buf []byte) (int, error) {
	select {
	case pkt := <-p.in:
		return copy(buf, pkt), nil
	case <-p.closed:
		return 0, io.EOF
	}
}

func (p *pipeTUN) Write(buf []byte) (int, error) {
	select {
	case p.out <- append([]byte(nil), buf...):
	default:
	}
	return len(buf), nil
}

func (p *pipeTUN) Name() string { return "pipe" }
func (p *pipeTUN) Close() error {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	return nil
}

type endpoint struct {
	id       identity.Identity
	eng      *dataplane.Engine
	tun      *pipeTUN
	link     *link.UDP
	peers    *peer.Table
	routes   *route.Table
	sessions *session.Manager
	overlay  netip.Addr
	dialAddr netip.AddrPort
}

func startEndpoint(t *testing.T, psk []byte, overlay string) *endpoint {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := link.ListenUDP(0)
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { udp.Close() })

	tun := newPipeTUN()
	peers := peer.NewTable()
	routes := route.NewTable(id.ID)
	sessions, err := session.NewManager(id, psk)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := dataplane.New(dataplane.Config{
		LocalID:   id.ID,
		Identity:  id,
		OverlayV4: netip.MustParseAddr(overlay),
		PrefixV4:  netip.MustParsePrefix("10.144.144.0/24"),
		PrefixV6:  netip.MustParsePrefix("fd00:6d65:7368::/64"),
		MTU:       1280,
	}, dataplane.Deps{TUN: tun, Link: udp, Peers: peers, Routes: routes, Sessions: sessions})
	if err != nil {
		t.Fatal(err)
	}
	ep := &endpoint{
		id: id, eng: eng, tun: tun, link: udp, peers: peers, routes: routes,
		sessions: sessions, overlay: netip.MustParseAddr(overlay),
		dialAddr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), udp.Port()),
	}
	return ep
}

func (e *endpoint) run(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = e.eng.Run(ctx) }()
	return cancel
}

func (e *endpoint) know(t *testing.T, other *endpoint, dialer bool) {
	t.Helper()
	if err := e.peers.Upsert(peer.Peer{
		ID:        other.id.ID,
		PublicKey: other.id.PublicKey,
		OverlayV4: other.overlay,
		Dialer:    dialer,
		Candidates: []peer.Candidate{
			{Addr: other.dialAddr, Source: peer.SourceConfig},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.sessions.SetPeer(other.id.ID, other.id.PublicKey); err != nil {
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

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before the deadline")
}

// TestOverlayOverLoopbackUDP runs the real transport: two engines, real UDP
// sockets, a Noise handshake, and an IPv4 overlay packet end to end.
func TestOverlayOverLoopbackUDP(t *testing.T) {
	psk := make([]byte, identity.KeySize)
	for i := range psk {
		psk[i] = byte(i + 7)
	}
	a := startEndpoint(t, psk, "10.144.144.1")
	b := startEndpoint(t, psk, "10.144.144.2")
	a.know(t, b, true)
	b.know(t, a, false)

	cancelA := a.run(t)
	defer cancelA()
	cancelB := b.run(t)
	defer cancelB()

	pkt := ipv4Packet(a.overlay, b.overlay, []byte("over the wire"))
	// The first send starts the handshake; it is dropped.
	if err := a.eng.SendOverlayPacket(pkt); !errors.Is(err, dataplane.ErrNoSession) && err != nil {
		t.Fatalf("first send: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return a.sessions.HasSession(b.id.ID) && b.sessions.HasSession(a.id.ID)
	})

	var received []byte
	waitFor(t, 5*time.Second, func() bool {
		if err := a.eng.SendOverlayPacket(pkt); err != nil {
			return false
		}
		select {
		case received = <-b.tun.out:
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	})
	if string(received[20:]) != "over the wire" {
		t.Fatalf("payload = %q", received[20:])
	}
	if a.eng.Counters().TxPackets == 0 || b.eng.Counters().RxPackets == 0 {
		t.Fatalf("counters = %+v / %+v", a.eng.Counters(), b.eng.Counters())
	}
}

// TestIdlePeersConnectThroughTheController covers the failure mode where
// configured peers never connect because no overlay packet ever needs them.
func TestIdlePeersConnectThroughTheController(t *testing.T) {
	psk := make([]byte, identity.KeySize)
	for i := range psk {
		psk[i] = byte(i + 3)
	}
	a := startEndpoint(t, psk, "10.144.144.1")
	b := startEndpoint(t, psk, "10.144.144.2")
	a.know(t, b, true)
	b.know(t, a, false)

	cancelA := a.run(t)
	defer cancelA()
	cancelB := b.run(t)
	defer cancelB()

	ctl, err := control.New(control.Local{ID: a.id.ID, OverlayV4: a.overlay},
		a.peers, a.routes, discovery.NewMerger(a.id.ID, a.peers, a.routes), a.eng)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelCtl := context.WithCancel(context.Background())
	defer cancelCtl()
	go func() { _ = ctl.Run(ctx) }()

	waitFor(t, 5*time.Second, func() bool {
		return a.sessions.HasSession(b.id.ID) && b.sessions.HasSession(a.id.ID)
	})
}

// TestRelayOverLoopbackUDP runs the three-node bridge over real sockets:
// A and C have no common reachable family, so their session must be relayed
// by B, and B must never decrypt the payload.
func TestRelayOverLoopbackUDP(t *testing.T) {
	psk := make([]byte, identity.KeySize)
	for i := range psk {
		psk[i] = byte(i + 11)
	}
	a := startEndpoint(t, psk, "10.144.144.1")
	b := startEndpoint(t, psk, "10.144.144.2")
	c := startEndpoint(t, psk, "10.144.144.3")

	a.know(t, b, true)
	b.know(t, a, false)
	b.know(t, c, true)
	c.know(t, b, false)

	cancelA := a.run(t)
	defer cancelA()
	cancelB := b.run(t)
	defer cancelB()
	cancelC := c.run(t)
	defer cancelC()

	// Direct sessions first: these create B's direct routes.
	for _, pair := range [][2]*endpoint{{a, b}, {b, c}} {
		x, y := pair[0], pair[1]
		waitFor(t, 5*time.Second, func() bool {
			if err := x.eng.EnsureSession(y.id.ID); err != nil {
				return false
			}
			return x.sessions.HasSession(y.id.ID) && y.sessions.HasSession(x.id.ID)
		})
	}

	// A and C learn each other the way STATE gossip would teach them.
	if err := a.peers.Upsert(peer.Peer{ID: c.id.ID, PublicKey: c.id.PublicKey, OverlayV4: c.overlay, Dialer: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.sessions.SetPeer(c.id.ID, c.id.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := c.peers.Upsert(peer.Peer{ID: a.id.ID, PublicKey: a.id.PublicKey, OverlayV4: a.overlay, Dialer: false}); err != nil {
		t.Fatal(err)
	}
	if err := c.sessions.SetPeer(a.id.ID, a.id.PublicKey); err != nil {
		t.Fatal(err)
	}
	// Two-hop routes, as an advertisement would install.
	if err := a.routes.MergeAdvertisement(b.id.ID, route.Advertisement{
		Destination: c.id.ID, Cost: 2 * time.Millisecond, Hops: 1,
		Path: []uint64{b.id.ID, c.id.ID}, Seq: 1, Expires: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.routes.MergeAdvertisement(b.id.ID, route.Advertisement{
		Destination: a.id.ID, Cost: 2 * time.Millisecond, Hops: 1,
		Path: []uint64{b.id.ID, a.id.ID}, Seq: 1, Expires: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	pkt := ipv4Packet(a.overlay, c.overlay, []byte("relayed"))
	waitFor(t, 5*time.Second, func() bool {
		if err := a.eng.SendOverlayPacket(pkt); err != nil {
			return false
		}
		select {
		case got := <-c.tun.out:
			return string(got[20:]) == "relayed"
		case <-time.After(50 * time.Millisecond):
			return false
		}
	})
	if b.eng.Counters().Forwarded == 0 {
		t.Fatal("relay did not forward anything")
	}
}
