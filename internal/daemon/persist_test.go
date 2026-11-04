package daemon

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
)

func TestConfigWithPeersRoundTrips(t *testing.T) {
	local, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	remote, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cfg := conf.Default()
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.API.Token = make([]byte, 32)

	peers := []peer.Peer{{
		ID:        remote.ID,
		PublicKey: remote.PublicKey,
		Dialer:    true,
		Candidates: []peer.Candidate{
			{Addr: netip.MustParseAddrPort("203.0.113.5:11010"), Source: peer.SourceConfig},
		},
	}}
	updated := configWithPeers(cfg, peers)
	if len(updated.Peers) != 1 || updated.Peers[0].ID != remote.ID {
		t.Fatalf("peers = %+v", updated.Peers)
	}
	text, err := conf.Render(updated)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	parsed, err := conf.Parse(string(text))
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, text)
	}
	if len(parsed.Peers) != 1 || parsed.Peers[0].ID != remote.ID || !parsed.Peers[0].Dialer {
		t.Fatalf("parsed peers = %+v", parsed.Peers)
	}
	if len(parsed.Peers[0].Addrs) != 1 || parsed.Peers[0].Addrs[0] != netip.MustParseAddrPort("203.0.113.5:11010") {
		t.Fatalf("parsed addrs = %v", parsed.Peers[0].Addrs)
	}
	_ = local
}

func TestLearnedPeersUseTheLowerIDDialerRule(t *testing.T) {
	local, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	remote, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{id: local, peers: peer.NewTable(), events: make(chan api.Event, 1)}
	d.peerLearned(remote.ID, remote.PublicKey)
	p, ok := d.peers.Get(remote.ID)
	if !ok {
		t.Fatal("peer not registered")
	}
	want := local.ID < remote.ID
	if p.Dialer != want {
		t.Fatalf("dialer = %v, want %v (local %016x remote %016x)", p.Dialer, want, local.ID, remote.ID)
	}
}

func TestAllCandidatesIncludeThePublicAddr(t *testing.T) {
	cfg := conf.Default()
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	cfg.Node.PublicAddr = netip.MustParseAddrPort("203.0.113.7:11010")
	cfg.Discovery.STUN = false
	cands := allCandidates(context.Background(), cfg)
	for _, c := range cands {
		if c == cfg.Node.PublicAddr {
			return
		}
	}
	t.Fatalf("public_addr missing from candidates: %v", cands)
}

func TestStalePeerPredicate(t *testing.T) {
	now := time.Now()
	if !stalePeer(peer.Peer{State: peer.StateDown, Added: now.Add(-20 * time.Minute)}, now, 15*time.Minute) {
		t.Fatal("a never-seen peer older than the TTL is not stale")
	}
	if stalePeer(peer.Peer{State: peer.StateDown, Added: now.Add(-20 * time.Minute), LastSeen: now}, now, 15*time.Minute) {
		t.Fatal("a peer that has been seen is stale")
	}
	if stalePeer(peer.Peer{State: peer.StateUp, Added: now.Add(-20 * time.Minute)}, now, 15*time.Minute) {
		t.Fatal("a connected peer is stale")
	}
	if stalePeer(peer.Peer{State: peer.StateDown, Added: now}, now, 15*time.Minute) {
		t.Fatal("a fresh peer is stale")
	}
}

// TestDaemonHoldsConfigLockForLifetime (Step 3) proves the daemon is
// the single writer: a second daemon for the same config path fails at
// New with ErrLocked, the first daemon's persist writes through the
// store, and closing the first daemon releases the lock so a third
// daemon can start.
func TestDaemonHoldsConfigLockForLifetime(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath := dir + "/skein.toml"
	cfg := conf.Default()
	cfg.Path = cfgPath
	cfg.Node.Name = "locktest"
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.API.Token = make([]byte, 32)
	cfg.API.Listen = ""
	cfg.API.Socket = dir + "/control.sock"
	// MDNS must stay on: with zero peers and MDNS off, Render refuses
	// the config (pre-existing validation), so persist would write
	// nothing — and this test is about the write path, not that rule.
	cfg.Discovery.MDNS = true
	cfg.Discovery.STUN = false

	d1, err := New(cfg, id)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	if _, err := New(cfg, id); err == nil {
		t.Fatal("second New = nil, want ErrLocked (daemon holds the config lock)")
	} else if !configstore.IsLocked(err) {
		// New wraps the lock error; accept either the typed error or
		// its message, but never a silent success.
		if !strings.Contains(err.Error(), "locked") {
			t.Fatalf("second New = %v, want ErrLocked", err)
		}
	}

	d1.persistPeers()
	loaded, err := conf.Load(cfgPath)
	if err != nil {
		t.Fatalf("persisted config does not load: %v", err)
	}
	if loaded.Node.Name != cfg.Node.Name {
		t.Fatalf("persisted name = %q, want %q", loaded.Node.Name, cfg.Node.Name)
	}

	if err := d1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	d3, err := New(cfg, id)
	if err != nil {
		t.Fatalf("New after Close = %v, want success (lock released)", err)
	}
	_ = d3.Close()
}
