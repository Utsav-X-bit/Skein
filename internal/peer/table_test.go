package peer

import (
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

func testPeer(t *testing.T, id identity.Identity) Peer {
	t.Helper()
	return Peer{
		ID:        id.ID,
		PublicKey: id.PublicKey,
		OverlayV4: netip.MustParseAddr("10.144.144.9"),
		Dialer:    true,
		Candidates: []Candidate{
			{Addr: netip.MustParseAddrPort("203.0.113.5:11010"), Source: SourceConfig},
		},
	}
}

func TestUpsertRejectsKeyMismatch(t *testing.T) {
	tbl := NewTable()
	a, _ := identity.Generate()
	b, _ := identity.Generate()

	p := testPeer(t, a)
	if err := tbl.Upsert(p); err != nil {
		t.Fatal(err)
	}
	// Same ID with a different key must be rejected.
	p.PublicKey = b.PublicKey
	if err := tbl.Upsert(p); err == nil {
		t.Fatal("expected a conflicting public key to be rejected")
	}
	if err := tbl.Upsert(Peer{ID: 0, PublicKey: a.PublicKey}); err == nil {
		t.Fatal("expected a zero peer ID to be rejected")
	}
	if err := tbl.Upsert(Peer{ID: a.ID, PublicKey: a.PublicKey[:16]}); err == nil {
		t.Fatal("expected a short public key to be rejected")
	}
}

func TestGetReturnsDeepCopy(t *testing.T) {
	tbl := NewTable()
	a, _ := identity.Generate()
	if err := tbl.Upsert(testPeer(t, a)); err != nil {
		t.Fatal(err)
	}
	got, ok := tbl.Get(a.ID)
	if !ok {
		t.Fatal("peer missing")
	}
	got.Candidates[0].Addr = netip.MustParseAddrPort("198.51.100.1:1")
	got.PublicKey[0] ^= 0xff
	again, _ := tbl.Get(a.ID)
	if again.Candidates[0].Addr == got.Candidates[0].Addr {
		t.Fatal("candidate slice aliased the table")
	}
	if again.PublicKey[0] == got.PublicKey[0] {
		t.Fatal("public key aliased the table")
	}
}

func TestListIsStableAndSorted(t *testing.T) {
	tbl := NewTable()
	for range 5 {
		id, _ := identity.Generate()
		p := testPeer(t, id)
		p.OverlayV4 = netip.Addr{}
		if err := tbl.Upsert(p); err != nil {
			t.Fatal(err)
		}
	}
	first := tbl.List()
	second := tbl.List()
	if len(first) != 5 {
		t.Fatalf("len = %d", len(first))
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].ID >= first[i].ID {
			t.Fatal("peers are not sorted by ID")
		}
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatal("list order is not stable")
		}
	}
}

func TestCandidatesDeduplicateAndRank(t *testing.T) {
	tbl := NewTable()
	a, _ := identity.Generate()
	if err := tbl.Upsert(testPeer(t, a)); err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddrPort("203.0.113.5:11010")
	if tbl.AddCandidate(a.ID, addr, SourceState) {
		t.Fatal("duplicate candidate reported as new")
	}
	p, _ := tbl.Get(a.ID)
	if p.Candidates[0].Source != SourceConfig {
		t.Fatalf("weaker source replaced config: %v", p.Candidates[0].Source)
	}
	newAddr := netip.MustParseAddrPort("198.51.100.7:11010")
	if !tbl.AddCandidate(a.ID, newAddr, SourceMDNS) {
		t.Fatal("new candidate not added")
	}
	p, _ = tbl.Get(a.ID)
	if len(p.Candidates) != 2 {
		t.Fatalf("candidates = %v", p.Candidates)
	}
}

func TestLookupOverlayAndState(t *testing.T) {
	tbl := NewTable()
	a, _ := identity.Generate()
	if err := tbl.Upsert(testPeer(t, a)); err != nil {
		t.Fatal(err)
	}
	if id, ok := tbl.LookupOverlay(netip.MustParseAddr("10.144.144.9")); !ok || id != a.ID {
		t.Fatal("overlay lookup failed")
	}
	if _, ok := tbl.LookupOverlay(netip.MustParseAddr("10.144.144.10")); ok {
		t.Fatal("unknown overlay address resolved")
	}
	now := time.Now()
	tbl.SetActive(a.ID, netip.MustParseAddrPort("[2001:db8::1]:11010"), 12*time.Millisecond, "udp", now)
	p, _ := tbl.Get(a.ID)
	if p.State != StateUp || p.Path != "v6" || p.RTT != 12*time.Millisecond || !p.Direct {
		t.Fatalf("active state = %+v", p)
	}
	tbl.SetState(a.ID, StateDown)
	p, _ = tbl.Get(a.ID)
	if p.State != StateDown {
		t.Fatalf("state = %v", p.State)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tbl := NewTable()
	a, _ := identity.Generate()
	if err := tbl.Upsert(testPeer(t, a)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				tbl.Get(a.ID)
				tbl.List()
				tbl.AddCandidate(a.ID, netip.MustParseAddrPort("198.51.100.8:11010"), SourceState)
				tbl.MarkSeen(a.ID, time.Now())
				tbl.AddTraffic(a.ID, 1, 1)
				tbl.SetState(a.ID, StateUp)
			}
		}()
	}
	wg.Wait()
}

func TestOverlayAddressCannotBeStolen(t *testing.T) {
	identA, _ := identity.Generate()
	identB, _ := identity.Generate()
	tbl := NewTable()
	if err := tbl.Upsert(Peer{ID: identA.ID, PublicKey: identA.PublicKey, OverlayV4: netip.MustParseAddr("10.0.0.2")}); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Upsert(Peer{ID: identB.ID, PublicKey: identB.PublicKey}); err != nil {
		t.Fatal(err)
	}
	tbl.SetOverlay(identB.ID, netip.MustParseAddr("10.0.0.2"), netip.Addr{})
	if id, ok := tbl.LookupOverlay(netip.MustParseAddr("10.0.0.2")); !ok || id != identA.ID {
		t.Fatalf("overlay lookup = %016x,%v; want %016x,true", id, ok, identA.ID)
	}
}

func TestUpsertPreservesLearnedFields(t *testing.T) {
	ident, _ := identity.Generate()
	tbl := NewTable()
	seen := time.Now().Add(-time.Minute)
	peer := Peer{ID: ident.ID, PublicKey: ident.PublicKey}
	if err := tbl.Upsert(peer); err != nil {
		t.Fatal(err)
	}
	tbl.SetOverlay(ident.ID, netip.MustParseAddr("10.0.0.2"), netip.Addr{})
	tbl.SetName(ident.ID, "archlinux")
	tbl.SetActive(ident.ID, netip.MustParseAddrPort("192.0.2.1:11010"), time.Millisecond, "udp", seen)
	tbl.AddTraffic(ident.ID, 11, 22)

	if err := tbl.Upsert(peer); err != nil {
		t.Fatal(err)
	}
	got, _ := tbl.Get(ident.ID)
	if got.OverlayV4 != netip.MustParseAddr("10.0.0.2") || got.Name != "archlinux" ||
		got.LastSeen.IsZero() || got.RxBytes != 11 || got.TxBytes != 22 || !got.Direct {
		t.Fatalf("upsert dropped learned state: %+v", got)
	}
}
