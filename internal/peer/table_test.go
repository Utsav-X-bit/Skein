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
		if err := tbl.Upsert(testPeer(t, id)); err != nil {
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
	tbl.SetActive(a.ID, netip.MustParseAddrPort("[2001:db8::1]:11010"), 12*time.Millisecond, now)
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
