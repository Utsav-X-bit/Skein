// Package route computes bounded mesh routes from direct links and
// authenticated route advertisements.
package route

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// MaxHops is the longest supported route in links.
const MaxHops = 4

// maxAdvertisementsPerOrigin bounds per-neighbour advertisement memory.
const maxAdvertisementsPerOrigin = 64

// AdvertisementTTL is how long a received advertisement stays valid.
const AdvertisementTTL = 90 * time.Second

// DirectLink is a measured one-hop neighbour.
type DirectLink struct {
	PeerID uint64
	RTT    time.Duration
}

// Advertisement is a re-originated route offered to a neighbour.
type Advertisement struct {
	Destination uint64
	Cost        time.Duration
	Hops        uint8
	// Path starts at the advertising peer and ends at Destination.
	Path    []uint64
	Seq     uint64
	Expires time.Time
}

// Route is a resolved path to a destination.
type Route struct {
	Destination uint64
	NextHop     uint64
	Cost        time.Duration
	Hops        uint8
	// Path lists the nodes after the local node, ending at Destination.
	Path []uint64
	// Expires bounds the life of advertised routes; direct routes never expire.
	Expires time.Time
}

// Table holds direct links, received advertisements, and resolved routes.
type Table struct {
	mu     sync.RWMutex
	self   uint64
	direct map[uint64]time.Duration
	advert map[uint64]map[uint64]Advertisement
	routes map[uint64]Route
	now    func() time.Time
}

// NewTable creates a route table for the local peer ID.
func NewTable(self uint64) *Table {
	if self == 0 {
		panic("route: local peer ID must not be zero")
	}
	return &Table{
		self:   self,
		direct: make(map[uint64]time.Duration),
		advert: make(map[uint64]map[uint64]Advertisement),
		routes: make(map[uint64]Route),
		now:    time.Now,
	}
}

// SetClock overrides the clock for tests.
func (t *Table) SetClock(now func() time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = now
}

// ResetOrigin forgets advertisements received from a peer. A restarted peer
// begins its STATE sequence at one, so the old per-destination sequences must
// not reject the new advertisements.
func (t *Table) ResetOrigin(from uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.advert, from)
	t.recompute()
}

// UpdateDirect records or refreshes a direct neighbour and recomputes routes.
func (t *Table) UpdateDirect(link DirectLink) error {
	if link.PeerID == 0 {
		return fmt.Errorf("route: direct link peer ID must not be zero")
	}
	if link.PeerID == t.self {
		return fmt.Errorf("route: direct link must not point at the local node")
	}
	if link.RTT <= 0 {
		return fmt.Errorf("route: direct link RTT must be positive")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.direct[link.PeerID] = link.RTT
	t.recompute()
	return nil
}

// RemoveDirect withdraws a direct neighbour and every route through it.
func (t *Table) RemoveDirect(peerID uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.direct, peerID)
	delete(t.advert, peerID)
	t.recompute()
}

// MergeAdvertisement installs an advertisement received from a direct
// neighbour and recomputes routes.
func (t *Table) MergeAdvertisement(from uint64, adv Advertisement) error {
	if from == 0 || from == t.self {
		return fmt.Errorf("route: invalid advertisement sender %016x", from)
	}
	if adv.Destination == 0 || adv.Destination == t.self {
		return fmt.Errorf("route: advertisement destination %016x is invalid", adv.Destination)
	}
	if len(adv.Path) == 0 {
		return fmt.Errorf("route: advertisement has an empty path")
	}
	if adv.Path[0] != from {
		return fmt.Errorf("route: advertisement path must start at sender %016x", from)
	}
	if adv.Path[len(adv.Path)-1] != adv.Destination {
		return fmt.Errorf("route: advertisement path must end at %016x", adv.Destination)
	}
	if len(adv.Path) > MaxHops {
		return fmt.Errorf("route: advertisement path of %d links exceeds %d", len(adv.Path), MaxHops)
	}
	seen := make(map[uint64]struct{}, len(adv.Path))
	for _, id := range adv.Path {
		if id == t.self {
			return fmt.Errorf("route: advertisement path loops through the local node")
		}
		if id == 0 {
			return fmt.Errorf("route: advertisement path contains a zero peer ID")
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("route: advertisement path repeats %016x", id)
		}
		seen[id] = struct{}{}
	}
	if adv.Cost < 0 {
		return fmt.Errorf("route: advertisement cost must not be negative")
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.direct[from]; !ok {
		return fmt.Errorf("route: %016x is not a direct neighbour", from)
	}
	if adv.Expires.Before(t.now()) {
		return fmt.Errorf("route: advertisement from %016x is expired", from)
	}
	dest := t.advert[from]
	if dest == nil {
		dest = make(map[uint64]Advertisement)
		t.advert[from] = dest
	}
	if old, ok := dest[adv.Destination]; ok && adv.Seq <= old.Seq {
		return fmt.Errorf("route: stale advertisement sequence %d from %016x", adv.Seq, from)
	}
	if _, exists := dest[adv.Destination]; !exists && len(dest) >= maxAdvertisementsPerOrigin {
		// Bound what one neighbour can make us remember; drop the entry that
		// expires first.
		var oldestDest uint64
		var oldestExpiry time.Time
		for d, a := range dest {
			if oldestDest == 0 || a.Expires.Before(oldestExpiry) {
				oldestDest, oldestExpiry = d, a.Expires
			}
		}
		delete(dest, oldestDest)
	}
	dest[adv.Destination] = cloneAdv(adv)
	t.recompute()
	return nil
}

// ReconcileOrigin withdraws advertisements from an origin that a newer
// STATE omitted (M4 F-08). Each STATE is a full snapshot of the origin's
// routes, so a destination present in an older STATE but absent from a
// newer one (higher seq) has been withdrawn at the origin and must stop
// being used here promptly — not after the 90 s advertisement TTL.
// Advertisements at or above seq are untouched (a trimmed route in a
// single oversized STATE is re-advertised by the next one; the flap
// window is one STATE interval, ~5 s, and is preferable to a 90 s stale
// route). Callers must only reconcile after the STATE's seq check has
// passed, so a stale STATE can never withdraw a newer route.
func (t *Table) ReconcileOrigin(from uint64, seq uint64, present map[uint64]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dests, ok := t.advert[from]
	if !ok {
		return
	}
	changed := false
	for dest, adv := range dests {
		if !present[dest] && adv.Seq < seq {
			delete(dests, dest)
			changed = true
		}
	}
	if changed {
		t.recompute()
	}
}

// WithdrawDestination removes every route to dest: the direct link, if
// any, and all advertisements offering it (M4 P8 tombstone application).
// It is how a departed peer stops being a destination, not just a next
// hop, on the node that learns the tombstone.
func (t *Table) WithdrawDestination(dest uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.direct, dest)
	for _, advs := range t.advert {
		delete(advs, dest)
	}
	t.recompute()
}

// Lookup returns the resolved route to destination.
// Lookup returns the current route. The Path slice aliases table state and
// must not be modified by the caller.
func (t *Table) Lookup(destination uint64) (Route, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.routes[destination]
	if !ok || t.expired(r) {
		return Route{}, false
	}
	return r, true
}

// Routes returns every resolved route sorted by destination.
func (t *Table) Routes() []Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Route, 0, len(t.routes))
	for _, r := range t.routes {
		if t.expired(r) {
			continue
		}
		out = append(out, cloneRoute(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Destination < out[j].Destination })
	return out
}

// recompute rebuilds the route table. Advertisements already describe a
// complete path, so one pass suffices.
func (t *Table) recompute() {
	now := t.now()
	routes := make(map[uint64]Route, len(t.direct)+len(t.routes))
	for id, rtt := range t.direct {
		routes[id] = Route{
			Destination: id,
			NextHop:     id,
			Cost:        rtt,
			Hops:        1,
			Path:        []uint64{id},
		}
	}
	for from, advs := range t.advert {
		rtt, ok := t.direct[from]
		if !ok {
			continue
		}
		for dest, adv := range advs {
			if adv.Expires.Before(now) {
				delete(advs, dest)
				continue
			}
			if len(adv.Path) > MaxHops || containsID(adv.Path, t.self) {
				continue
			}
			cand := Route{
				Destination: dest,
				NextHop:     from,
				Cost:        adv.Cost + rtt,
				Hops:        uint8(len(adv.Path)),
				Path:        append([]uint64(nil), adv.Path...),
				Expires:     adv.Expires,
			}
			cur, ok := routes[dest]
			if !ok || better(cand, cur) {
				routes[dest] = cand
			}
		}
	}
	t.routes = routes
}

// expired reports whether an advertised route has outlived its TTL.
func (t *Table) expired(r Route) bool {
	return !r.Expires.IsZero() && r.Expires.Before(t.now())
}

func better(a, b Route) bool {
	if a.Cost != b.Cost {
		return a.Cost < b.Cost
	}
	return a.NextHop < b.NextHop
}

func containsID(path []uint64, id uint64) bool {
	for _, v := range path {
		if v == id {
			return true
		}
	}
	return false
}

func cloneAdv(a Advertisement) Advertisement {
	a.Path = append([]uint64(nil), a.Path...)
	return a
}

func cloneRoute(r Route) Route {
	r.Path = append([]uint64(nil), r.Path...)
	return r
}
