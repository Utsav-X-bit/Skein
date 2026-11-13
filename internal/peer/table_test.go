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

func TestSetActiveStartsSessionWhenDown(t *testing.T) {
	ident, _ := identity.Generate()
	tbl := NewTable()
	if err := tbl.Upsert(Peer{ID: ident.ID, PublicKey: ident.PublicKey}); err != nil {
		t.Fatal(err)
	}
	// A probe reply is often the up transition, so it must start the session
	// rather than leaving SessionStart zero for the next handshake to fill.
	up := time.Now().Add(-time.Minute)
	tbl.SetActive(ident.ID, netip.MustParseAddrPort("192.0.2.1:11010"), time.Millisecond, "udp", up)
	p, _ := tbl.Get(ident.ID)
	if p.State != StateUp || !p.SessionStart.Equal(up) {
		t.Fatalf("probe up = state %q start %v, want up at %v", p.State, p.SessionStart, up)
	}

	// Another probe reply while already up must not restart the session.
	again := up.Add(10 * time.Second)
	tbl.SetActive(ident.ID, netip.MustParseAddrPort("192.0.2.1:11010"), time.Millisecond, "udp", again)
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.Equal(up) {
		t.Fatalf("repeat SetActive moved session start to %v, want %v", p.SessionStart, up)
	}

	// A rekey after the probe-driven up transition only moves LastHandshake.
	rekey := again.Add(30 * time.Second)
	tbl.MarkSession(ident.ID, rekey)
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.Equal(up) {
		t.Fatalf("rekey moved session start to %v, want %v", p.SessionStart, up)
	}
	if !p.LastHandshake.Equal(rekey) {
		t.Fatalf("rekey handshake = %v, want %v", p.LastHandshake, rekey)
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

func TestSessionTimestampsFollowTheLink(t *testing.T) {
	ident, _ := identity.Generate()
	tbl := NewTable()
	if err := tbl.Upsert(Peer{ID: ident.ID, PublicKey: ident.PublicKey}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute)
	tbl.MarkSession(ident.ID, start)
	p, _ := tbl.Get(ident.ID)
	if !p.SessionStart.Equal(start) || !p.LastHandshake.Equal(start) {
		t.Fatalf("first handshake = start %v handshake %v, want %v", p.SessionStart, p.LastHandshake, start)
	}

	// The direct proof moves the link up without restarting the session.
	tbl.SetActive(ident.ID, netip.MustParseAddrPort("192.0.2.1:11010"), time.Millisecond, "udp", start)
	rekey := start.Add(30 * time.Second)
	tbl.MarkSession(ident.ID, rekey)
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.Equal(start) {
		t.Fatalf("rekey moved session start to %v, want %v", p.SessionStart, start)
	}
	if !p.LastHandshake.Equal(rekey) {
		t.Fatalf("rekey handshake = %v, want %v", p.LastHandshake, rekey)
	}

	// A down transition ends the session; a reconnect starts a new one.
	tbl.SetState(ident.ID, StateDown)
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.IsZero() || !p.LastHandshake.IsZero() {
		t.Fatalf("down left timestamps: %+v", p)
	}
	reconnect := rekey.Add(time.Minute)
	tbl.MarkSession(ident.ID, reconnect)
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.Equal(reconnect) || !p.LastHandshake.Equal(reconnect) {
		t.Fatalf("reconnect timestamps = %+v, want %v", p, reconnect)
	}

	// ClearActive forgets the session too.
	tbl.SetActive(ident.ID, netip.MustParseAddrPort("192.0.2.1:11010"), time.Millisecond, "udp", reconnect)
	tbl.ClearActive(ident.ID)
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.IsZero() || !p.LastHandshake.IsZero() {
		t.Fatalf("ClearActive left timestamps: %+v", p)
	}

	// Upsert with zero timestamps preserves learned values.
	tbl.SetState(ident.ID, StateDown)
	tbl.MarkSession(ident.ID, reconnect)
	if err := tbl.Upsert(Peer{ID: ident.ID, PublicKey: ident.PublicKey}); err != nil {
		t.Fatal(err)
	}
	p, _ = tbl.Get(ident.ID)
	if !p.SessionStart.Equal(reconnect) || !p.LastHandshake.Equal(reconnect) {
		t.Fatalf("upsert dropped session timestamps: %+v", p)
	}
}

func TestSetRTTReportsMedianNotOutlier(t *testing.T) {
	tbl := NewTable()
	ident, _ := identity.Generate()
	if err := tbl.Upsert(testPeer(t, ident)); err != nil {
		t.Fatal(err)
	}
	for _, rtt := range []time.Duration{5 * time.Millisecond, 6 * time.Millisecond, 120 * time.Millisecond, 4 * time.Millisecond} {
		tbl.SetRTT(ident.ID, rtt)
	}
	p, _ := tbl.Get(ident.ID)
	// Sorted series 4, 5, 6, 120: the middle pair averages to 5.5 ms, so the
	// one 120 ms outlier never becomes the displayed latency.
	if p.RTT != 5500*time.Microsecond {
		t.Fatalf("RTT = %v, want 5.5ms", p.RTT)
	}
}

func TestSetActiveStartsAFreshSeries(t *testing.T) {
	tbl := NewTable()
	ident, _ := identity.Generate()
	if err := tbl.Upsert(testPeer(t, ident)); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		tbl.SetRTT(ident.ID, 200*time.Millisecond)
	}
	tbl.SetActive(ident.ID, netip.MustParseAddrPort("192.0.2.9:11010"), 8*time.Millisecond, "udp", time.Now())
	p, _ := tbl.Get(ident.ID)
	if p.RTT != 8*time.Millisecond {
		t.Fatalf("RTT = %v, want the new path's 8ms", p.RTT)
	}
}
