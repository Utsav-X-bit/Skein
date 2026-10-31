package route

import (
	"testing"
	"time"
)

const (
	peerA uint64 = 0xA
	peerB uint64 = 0xB
	peerC uint64 = 0xC
	peerD uint64 = 0xD
	peerE uint64 = 0xE
)

func adv(seq uint64, cost time.Duration, path ...uint64) Advertisement {
	return Advertisement{
		Destination: path[len(path)-1],
		Cost:        cost,
		Hops:        uint8(len(path) - 1),
		Path:        path,
		Seq:         seq,
		Expires:     time.Now().Add(time.Minute),
	}
}

func mustDirect(t *testing.T, tbl *Table, id uint64, rtt time.Duration) {
	t.Helper()
	if err := tbl.UpdateDirect(DirectLink{PeerID: id, RTT: rtt}); err != nil {
		t.Fatalf("UpdateDirect(%x): %v", id, err)
	}
}

func mustMerge(t *testing.T, tbl *Table, from uint64, a Advertisement) {
	t.Helper()
	if err := tbl.MergeAdvertisement(from, a); err != nil {
		t.Fatalf("MergeAdvertisement(%x): %v", from, err)
	}
}

func TestDirectRoutes(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, 10*time.Millisecond)
	r, ok := tbl.Lookup(peerB)
	if !ok || r.NextHop != peerB || r.Hops != 1 || r.Cost != 10*time.Millisecond {
		t.Fatalf("route = %+v ok=%v", r, ok)
	}
	if _, ok := tbl.Lookup(peerA); ok {
		t.Fatal("local node must not have a route to itself")
	}
}

func TestTwoHopBridge(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, 10*time.Millisecond)
	mustMerge(t, tbl, peerB, adv(1, 20*time.Millisecond, peerB, peerC))
	r, ok := tbl.Lookup(peerC)
	if !ok {
		t.Fatal("missing bridge route")
	}
	if r.NextHop != peerB || r.Hops != 2 || r.Cost != 30*time.Millisecond {
		t.Fatalf("route = %+v", r)
	}
	if len(r.Path) != 2 || r.Path[0] != peerB || r.Path[1] != peerC {
		t.Fatalf("path = %v", r.Path)
	}
}

func TestFiveNodeLineUsesFourLinks(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, 10*time.Millisecond)
	mustMerge(t, tbl, peerB, adv(1, 10*time.Millisecond, peerB, peerC))
	mustMerge(t, tbl, peerB, adv(2, 20*time.Millisecond, peerB, peerC, peerD))
	mustMerge(t, tbl, peerB, adv(3, 30*time.Millisecond, peerB, peerC, peerD, peerE))
	r, ok := tbl.Lookup(peerE)
	if !ok {
		t.Fatal("missing four-link route")
	}
	if r.Hops != 4 || r.NextHop != peerB || len(r.Path) != 4 {
		t.Fatalf("route = %+v", r)
	}
}

func TestRouteLongerThanFourLinksRejected(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, time.Millisecond)
	if err := tbl.MergeAdvertisement(peerB, adv(1, time.Millisecond, peerB, peerC, peerD, peerE, 0xF)); err == nil {
		t.Fatal("expected a five-link advertisement to be rejected")
	}
}

func TestLowerCostReplacesAndTiesBreakOnNextHop(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, 10*time.Millisecond)
	mustDirect(t, tbl, peerC, 10*time.Millisecond)
	mustMerge(t, tbl, peerB, adv(1, 30*time.Millisecond, peerB, peerD))
	mustMerge(t, tbl, peerC, adv(1, 30*time.Millisecond, peerC, peerD))
	r, _ := tbl.Lookup(peerD)
	if r.NextHop != peerB {
		t.Fatalf("tie-break chose %x, want %x", r.NextHop, peerB)
	}
	// A cheaper advertisement from C wins.
	mustMerge(t, tbl, peerC, adv(2, 5*time.Millisecond, peerC, peerD))
	r, _ = tbl.Lookup(peerD)
	if r.NextHop != peerC || r.Cost != 15*time.Millisecond {
		t.Fatalf("route = %+v", r)
	}
}

func TestWithdrawalOnDirectLinkLoss(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, time.Millisecond)
	mustMerge(t, tbl, peerB, adv(1, time.Millisecond, peerB, peerC))
	if _, ok := tbl.Lookup(peerC); !ok {
		t.Fatal("missing route before withdrawal")
	}
	tbl.RemoveDirect(peerB)
	if _, ok := tbl.Lookup(peerC); ok {
		t.Fatal("route survived next-hop withdrawal")
	}
	if _, ok := tbl.Lookup(peerB); ok {
		t.Fatal("direct route survived withdrawal")
	}
}

func TestSplitHorizon(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, time.Millisecond)
	mustDirect(t, tbl, peerC, time.Millisecond)
	mustMerge(t, tbl, peerB, adv(1, time.Millisecond, peerB, peerD))
	for _, a := range tbl.AdvertisementsFor(peerB) {
		if a.Destination == peerB || a.Destination == peerD {
			t.Fatalf("advertised %x to the next hop", a.Destination)
		}
		if containsID(a.Path, peerB) {
			t.Fatalf("advertised a path through the receiver: %v", a.Path)
		}
	}
	found := false
	for _, a := range tbl.AdvertisementsFor(peerC) {
		if a.Destination == peerD {
			found = true
			if len(a.Path) != 3 || a.Path[0] != peerA || a.Path[1] != peerB || a.Path[2] != peerD {
				t.Fatalf("re-originated path = %v", a.Path)
			}
			if a.Hops != 2 {
				t.Fatalf("hops = %d", a.Hops)
			}
		}
	}
	if !found {
		t.Fatal("route to D was not advertised to C")
	}
}

func TestPathLoopAndSelfAreRejected(t *testing.T) {
	tbl := NewTable(peerA)
	mustDirect(t, tbl, peerB, time.Millisecond)
	if err := tbl.MergeAdvertisement(peerB, adv(1, time.Millisecond, peerB, peerA, peerC)); err == nil {
		t.Fatal("expected a path through the local node to be rejected")
	}
	if err := tbl.MergeAdvertisement(peerB, adv(2, time.Millisecond, peerB, peerC, peerC)); err == nil {
		t.Fatal("expected a repeated peer to be rejected")
	}
	if err := tbl.MergeAdvertisement(peerB, adv(3, time.Millisecond, peerC, peerD)); err == nil {
		t.Fatal("expected a path not starting at the sender to be rejected")
	}
	if err := tbl.MergeAdvertisement(peerB, adv(4, time.Millisecond, peerB, peerC)); err != nil {
		t.Fatalf("valid advertisement rejected: %v", err)
	}
}

func TestNonNeighbourAdvertisementRejected(t *testing.T) {
	tbl := NewTable(peerA)
	if err := tbl.MergeAdvertisement(peerB, adv(1, time.Millisecond, peerB, peerC)); err == nil {
		t.Fatal("expected an advertisement from a non-neighbour to be rejected")
	}
}

func TestStaleAndExpiredAdvertisementsRejected(t *testing.T) {
	now := time.Now()
	tbl := NewTable(peerA)
	tbl.SetClock(func() time.Time { return now })
	mustDirect(t, tbl, peerB, time.Millisecond)
	mustMerge(t, tbl, peerB, adv(5, time.Millisecond, peerB, peerC))
	if err := tbl.MergeAdvertisement(peerB, adv(4, 2*time.Millisecond, peerB, peerC)); err == nil {
		t.Fatal("expected a stale sequence to be rejected")
	}
	expired := adv(6, time.Millisecond, peerB, peerC)
	expired.Expires = now.Add(-time.Second)
	if err := tbl.MergeAdvertisement(peerB, expired); err == nil {
		t.Fatal("expected an expired advertisement to be rejected")
	}
	// After the stored advertisement expires, the route disappears.
	now = now.Add(2 * AdvertisementTTL)
	routes := tbl.Routes()
	for _, r := range routes {
		if r.Destination == peerC {
			t.Fatalf("expired route still present: %+v", r)
		}
	}
}
