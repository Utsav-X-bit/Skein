package daemon

import (
	"net/netip"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
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
