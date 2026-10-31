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

	"github.com/utsavgupta5467/skein/internal/dataplane"
	"github.com/utsavgupta5467/skein/internal/identity"
	"github.com/utsavgupta5467/skein/internal/link"
	"github.com/utsavgupta5467/skein/internal/peer"
	"github.com/utsavgupta5467/skein/internal/route"
	"github.com/utsavgupta5467/skein/internal/session"
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
