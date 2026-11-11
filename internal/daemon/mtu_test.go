package daemon

import (
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/dataplane"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

// fakeTUN and fakeLink satisfy the dataplane dependencies; these tests never
// run the engine's loops.
type fakeTUN struct{}

func (fakeTUN) Read([]byte) (int, error)    { return 0, io.EOF }
func (fakeTUN) Write(p []byte) (int, error) { return len(p), nil }
func (fakeTUN) Name() string                { return "fake" }
func (fakeTUN) Close() error                { return nil }

type fakeLink struct{}

func (fakeLink) Send(netip.AddrPort, []byte) error        { return nil }
func (fakeLink) Recv([]byte) (int, netip.AddrPort, error) { return 0, netip.AddrPort{}, io.EOF }
func (fakeLink) Observe() (netip.AddrPort, error)         { return netip.AddrPort{}, io.EOF }
func (fakeLink) LocalAddrs() []netip.AddrPort             { return nil }
func (fakeLink) Close() error                             { return nil }

// attachEngine gives the daemon a real engine so the MTU accept limit can be
// asserted, without a device or sockets.
func attachEngine(t *testing.T, d *Daemon) *dataplane.Engine {
	t.Helper()
	cfg := d.config()
	eng, err := dataplane.New(dataplane.Config{
		LocalID:   d.id.ID,
		Identity:  d.id,
		OverlayV4: cfg.Node.OverlayIPv4,
		OverlayV6: cfg.Node.OverlayIPv6,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		MTU:       cfg.Node.MTU,
	}, dataplane.Deps{TUN: fakeTUN{}, Link: fakeLink{}, Peers: d.peers, Routes: d.routes, Sessions: d.sessions})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	d.eng = eng
	return eng
}

// TestNegotiatedMTUAppliesSmallestPath proves the interface MTU is raised only
// after every directly connected peer reports, and to the smallest path.
func TestNegotiatedMTUAppliesSmallestPath(t *testing.T) {
	d, _ := newSystemDaemon(t)
	eng := attachEngine(t, d)
	applied := 0
	d.setMTU = func(_ string, mtu int) error {
		applied = mtu
		return nil
	}
	first := addTestPeer(t, d)
	second := addTestPeer(t, d)
	now := time.Now()
	d.peers.SetActive(first, netip.MustParseAddrPort("192.0.2.10:11010"), time.Millisecond, "udp", now)
	d.peers.SetActive(second, netip.MustParseAddrPort("192.0.2.11:11010"), time.Millisecond, "udp", now)

	d.applyNegotiatedMTU(first, wire.MaxFrameSize)
	if applied != 0 {
		t.Fatalf("applied %d before every direct peer reported", applied)
	}
	smaller := wire.MaxFrameSize - 20
	d.applyNegotiatedMTU(second, smaller)

	want := smaller - wire.Overhead
	if applied != want {
		t.Fatalf("applied MTU = %d, want %d", applied, want)
	}
	if got := eng.MTU(); got != want {
		t.Fatalf("engine MTU = %d, want %d", got, want)
	}
}

// TestNegotiatedMTUKeepsFloorWhenAProbeFails proves an unmeasurable path keeps
// the configured MTU for the whole connection instead of guessing.
func TestNegotiatedMTUKeepsFloorWhenAProbeFails(t *testing.T) {
	d, _ := newSystemDaemon(t)
	eng := attachEngine(t, d)
	applied := 0
	d.setMTU = func(_ string, mtu int) error {
		applied = mtu
		return nil
	}
	first := addTestPeer(t, d)
	second := addTestPeer(t, d)
	now := time.Now()
	d.peers.SetActive(first, netip.MustParseAddrPort("192.0.2.10:11010"), time.Millisecond, "udp", now)
	d.peers.SetActive(second, netip.MustParseAddrPort("192.0.2.11:11010"), time.Millisecond, "udp", now)

	d.applyNegotiatedMTU(first, 0)
	d.applyNegotiatedMTU(second, wire.MaxFrameSize)
	if applied != 0 {
		t.Fatalf("applied %d after an unmeasurable path", applied)
	}
	if got, want := eng.MTU(), d.config().Node.MTU; got != want {
		t.Fatalf("engine MTU = %d, want the configured %d", got, want)
	}
}
