// Package peer tracks remote nodes: identity, candidate endpoints, direct-link
// health, and counters.
package peer

import (
	"bytes"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

// State is the direct-link state of a peer.
type State string

// Peer states.
const (
	StateDown     State = "down"
	StateUp       State = "up"
	StateStale    State = "stale"
	StateDegraded State = "degraded"
)

// Source records where a candidate endpoint came from.
type Source string

// Candidate provenance, strongest first.
const (
	SourceConfig Source = "config"
	SourceMDNS   Source = "mdns"
	SourceState  Source = "state"
	SourceProbe  Source = "probe"
)

// Candidate is one underlay endpoint for a peer.
type Candidate struct {
	Addr   netip.AddrPort
	Source Source
}

// Peer is an immutable snapshot of a peer's state.
type Peer struct {
	ID        uint64
	Name      string
	PublicKey []byte
	OverlayV4 netip.Addr
	OverlayV6 netip.Addr
	Dialer    bool

	Candidates     []Candidate
	State          State
	ActiveEndpoint netip.AddrPort
	Path           string
	Transport      string
	RxTransport    string
	RTT            time.Duration
	RxBytes        uint64
	TxBytes        uint64
	LastSeen       time.Time
	// SessionStart is when the current direct session was first proven up;
	// LastHandshake is the latest installed handshake (a rekey refreshes it
	// without restarting the session). Both are zero when no session has
	// been observed, so callers must report "unknown", never epoch 0.
	SessionStart  time.Time
	LastHandshake time.Time
	// Added is when this entry first appeared locally. Never-seen peers older
	// than the stale timeout are pruned.
	Added  time.Time
	Direct bool
}

// Table is a concurrency-safe peer store. Every read returns a deep copy.
type Table struct {
	mu        sync.RWMutex
	peers     map[uint64]*Peer
	byOverlay map[netip.Addr]uint64
	// rtts holds each peer's recent control measurements. RTT is their
	// median: on a power-saving radio one probe can be an order of
	// magnitude slower than the path, and a single sample would show that
	// outlier until the next probe.
	rtts map[uint64][]time.Duration
}

// rttSamples is how many measurements the displayed RTT is taken from.
const rttSamples = 5

// median returns the middle value, averaging the two middles for an even
// count. It never reorders the caller's slice.
func median(values []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// NewTable creates an empty peer table.
func NewTable() *Table {
	return &Table{
		peers:     make(map[uint64]*Peer),
		byOverlay: make(map[netip.Addr]uint64),
		rtts:      make(map[uint64][]time.Duration),
	}
}

// Upsert inserts or updates a peer. A peer ID bound to a different public key
// is rejected, as is a zero ID or malformed key.
func (t *Table) Upsert(p Peer) error {
	if p.ID == 0 {
		return fmt.Errorf("peer: id must not be zero")
	}
	derived, err := identity.PeerID(p.PublicKey)
	if err != nil {
		return err
	}
	if derived != p.ID {
		return fmt.Errorf("peer: id %016x does not match public key %016x", p.ID, derived)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	existing, ok := t.peers[p.ID]
	if ok {
		p.Added = existing.Added
		if p.Added.IsZero() {
			p.Added = time.Now()
		}
		if !bytes.Equal(existing.PublicKey, p.PublicKey) {
			return fmt.Errorf("peer: %016x is already bound to a different public key", p.ID)
		}
		if p.State == "" {
			p.State = existing.State
		}
		if !p.ActiveEndpoint.IsValid() {
			p.ActiveEndpoint = existing.ActiveEndpoint
			p.Path = existing.Path
			p.RTT = existing.RTT
		}
		if len(p.Candidates) == 0 {
			p.Candidates = existing.Candidates
		}
		if !p.OverlayV4.IsValid() {
			p.OverlayV4 = existing.OverlayV4
		}
		if !p.OverlayV6.IsValid() {
			p.OverlayV6 = existing.OverlayV6
		}
		if p.Name == "" {
			p.Name = existing.Name
		}
		if p.LastSeen.IsZero() {
			p.LastSeen = existing.LastSeen
		}
		if p.SessionStart.IsZero() {
			p.SessionStart = existing.SessionStart
		}
		if p.LastHandshake.IsZero() {
			p.LastHandshake = existing.LastHandshake
		}
		if p.RxBytes == 0 {
			p.RxBytes = existing.RxBytes
		}
		if p.TxBytes == 0 {
			p.TxBytes = existing.TxBytes
		}
		if !p.Direct {
			p.Direct = existing.Direct
		}
		if p.Transport == "" {
			p.Transport = existing.Transport
		}
		if p.RxTransport == "" {
			p.RxTransport = existing.RxTransport
		}
	}
	if t.overlayConflictLocked(p.ID, p.OverlayV4, p.OverlayV6) {
		return fmt.Errorf("peer: overlay address is already assigned to another peer")
	}
	if p.Added.IsZero() {
		p.Added = time.Now()
	}
	clone := p
	clone.PublicKey = append([]byte(nil), p.PublicKey...)
	clone.Candidates = append([]Candidate(nil), p.Candidates...)
	if clone.State == "" {
		clone.State = StateDown
	}
	t.peers[p.ID] = &clone
	t.reindexLocked(p.ID)
	return nil
}

// Remove deletes a peer.
func (t *Table) Remove(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for addr, pid := range t.byOverlay {
		if pid == id {
			delete(t.byOverlay, addr)
		}
	}
	delete(t.peers, id)
	delete(t.rtts, id)
}

// reindexLocked refreshes the overlay index for one peer.
func (t *Table) reindexLocked(id uint64) {
	for addr, pid := range t.byOverlay {
		if pid == id {
			delete(t.byOverlay, addr)
		}
	}
	p, ok := t.peers[id]
	if !ok {
		return
	}
	if p.OverlayV4.IsValid() {
		t.byOverlay[p.OverlayV4] = id
	}
	if p.OverlayV6.IsValid() {
		t.byOverlay[p.OverlayV6] = id
	}
}

// Get returns a copy of one peer.
func (t *Table) Get(id uint64) (Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.peers[id]
	if !ok {
		return Peer{}, false
	}
	return clonePeer(p), true
}

// List returns all peers sorted by ID.
func (t *Table) List() []Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Peer, 0, len(t.peers))
	for _, p := range t.peers {
		out = append(out, clonePeer(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Exists reports whether id is a known peer.
func (t *Table) Exists(id uint64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.peers[id]
	return ok
}

// ByAddr maps an underlay endpoint to a peer without copying the table.
func (t *Table) ByAddr(addr netip.AddrPort) (uint64, bool) {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	t.mu.RLock()
	defer t.mu.RUnlock()
	for id, p := range t.peers {
		if p.ActiveEndpoint == addr {
			return id, true
		}
		for _, c := range p.Candidates {
			if c.Addr == addr {
				return id, true
			}
		}
	}
	return 0, false
}

// Overlay returns a peer's overlay addresses without copying the full record.
func (t *Table) Overlay(id uint64) (v4, v6 netip.Addr, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.peers[id]
	if !ok {
		return netip.Addr{}, netip.Addr{}, false
	}
	return p.OverlayV4, p.OverlayV6, true
}

// Len reports the number of peers.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// LookupOverlay maps an overlay address to a peer ID. The index is unique by
// construction, so an address can never resolve to two peers.
func (t *Table) LookupOverlay(addr netip.Addr) (uint64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	id, ok := t.byOverlay[addr]
	return id, ok
}

// overlayConflictLocked reports whether v4 or v6 already belongs to another
// peer. The caller must hold the write lock.
func (t *Table) overlayConflictLocked(id uint64, v4, v6 netip.Addr) bool {
	if v4.IsValid() {
		if pid, ok := t.byOverlay[v4]; ok && pid != id {
			return true
		}
	}
	if v6.IsValid() {
		if pid, ok := t.byOverlay[v6]; ok && pid != id {
			return true
		}
	}
	return false
}

// AddCandidate merges one candidate endpoint, keeping the strongest provenance.
func (t *Table) AddCandidate(id uint64, addr netip.AddrPort, source Source) bool {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok || !addr.IsValid() || addr.Port() == 0 {
		return false
	}
	for i, c := range p.Candidates {
		if c.Addr == addr {
			if rank(source) > rank(c.Source) {
				p.Candidates[i].Source = source
			}
			return false
		}
	}
	if len(p.Candidates) >= identity.MaxEndpoints {
		return false
	}
	p.Candidates = append(p.Candidates, Candidate{Addr: addr, Source: source})
	return true
}

func rank(s Source) int {
	switch s {
	case SourceConfig:
		return 4
	case SourceMDNS:
		return 3
	case SourceProbe:
		return 2
	case SourceState:
		return 1
	default:
		return 0
	}
}

// SetName records the name a peer advertises about itself. An empty update
// never clears a known name.
func (t *Table) SetName(id uint64, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.Name = name
	}
}

// SetOverlay records a peer's overlay addresses learned from authenticated
// state. The first claimed value wins so a member cannot move another peer
// around after the fact.
func (t *Table) SetOverlay(id uint64, v4, v6 netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok {
		return
	}
	if v4.IsValid() && !p.OverlayV4.IsValid() && !t.overlayConflictLocked(id, v4, netip.Addr{}) {
		p.OverlayV4 = v4
		t.byOverlay[v4] = id
	}
	if v6.IsValid() && !p.OverlayV6.IsValid() && !t.overlayConflictLocked(id, netip.Addr{}, v6) {
		p.OverlayV6 = v6
		t.byOverlay[v6] = id
	}
}

// UpdateOverlay records a peer's new overlay addresses. It is used when the
// peer itself advertises the change; third-party records still only fill gaps.
func (t *Table) UpdateOverlay(id uint64, v4, v6 netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok {
		return
	}
	if v4.IsValid() && !t.overlayConflictLocked(id, v4, netip.Addr{}) {
		p.OverlayV4 = v4
		t.reindexLocked(id)
	}
	if v6.IsValid() && !t.overlayConflictLocked(id, netip.Addr{}, v6) {
		p.OverlayV6 = v6
		t.reindexLocked(id)
	}
}

// SetActive records a validated direct path. A probe reply can be the up
// transition, so it starts the session the same way a handshake would; a
// later MarkSession then only refreshes LastHandshake.
func (t *Table) SetActive(id uint64, endpoint netip.AddrPort, rtt time.Duration, transport string, seen time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok {
		return
	}
	p.ActiveEndpoint = netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())
	p.Path = "v4"
	if endpoint.Addr().Is6() {
		p.Path = "v6"
	}
	p.RTT = rtt
	// A newly proven path starts a new measurement series: the previous
	// path's samples say nothing about this one.
	t.rtts[id] = []time.Duration{rtt}
	if p.State != StateUp {
		p.SessionStart = seen
	}
	p.State = StateUp
	p.Direct = true
	p.Transport = transport
	p.LastSeen = seen
}

// ClearActive forgets the validated path so a reconnect must re-prove one.
func (t *Table) ClearActive(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.ActiveEndpoint = netip.AddrPort{}
		p.Path = ""
		p.Transport = ""
		p.RTT = 0
		p.Direct = false
		p.SessionStart = time.Time{}
		p.LastHandshake = time.Time{}
	}
}

// MarkSession records an installed handshake. The first handshake after the
// link left the up state starts the session; a rekey while already up only
// refreshes LastHandshake.
func (t *Table) MarkSession(id uint64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok {
		return
	}
	if p.State != StateUp {
		p.SessionStart = at
	}
	p.LastHandshake = at
}

// SetState updates the link state. Leaving the up state ends the session, so
// both timestamps are cleared rather than left to look fresh.
func (t *Table) SetState(id uint64, state State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.State = state
		if state != StateUp {
			p.SessionStart = time.Time{}
			p.LastHandshake = time.Time{}
		}
	}
}

// SetDialer sets the dialer role. The invite path uses it because an invite is
// authoritative: a peer already learned from gossip or mDNS may hold the other
// role, and the joiner is the only side with the inviter's endpoints.
func (t *Table) SetDialer(id uint64, dialer bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.Dialer = dialer
	}
}

// SetRTT records the latest round-trip time measured by control traffic.
// The peer's RTT is the median of the recent series, so one delayed probe
// on a power-saving radio does not become the displayed latency.
func (t *Table) SetRTT(id uint64, rtt time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.peers[id]
	if !ok {
		return
	}
	series := append(t.rtts[id], rtt)
	if len(series) > rttSamples {
		series = series[len(series)-rttSamples:]
	}
	t.rtts[id] = series
	p.RTT = median(series)
}

// SetRxTransport records the transport the peer's frames arrive on, which can
// differ from the transport this node sends with.
func (t *Table) SetRxTransport(id uint64, transport string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.RxTransport = transport
	}
}

// MarkSeen records authenticated traffic from a peer.
func (t *Table) MarkSeen(id uint64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.LastSeen = at
	}
}

// AddTraffic records rx/tx byte counts.
func (t *Table) AddTraffic(id uint64, rx, tx uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.RxBytes += rx
		p.TxBytes += tx
	}
}

func clonePeer(p *Peer) Peer {
	out := *p
	out.PublicKey = append([]byte(nil), p.PublicKey...)
	out.Candidates = append([]Candidate(nil), p.Candidates...)
	return out
}
