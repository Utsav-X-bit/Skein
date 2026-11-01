// Package peer tracks remote nodes: identity, candidate endpoints, direct-link
// health, and counters.
package peer

import (
	"bytes"
	"fmt"
	"net/netip"
	"sort"
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
	PublicKey []byte
	OverlayV4 netip.Addr
	OverlayV6 netip.Addr
	Dialer    bool

	Candidates     []Candidate
	State          State
	ActiveEndpoint netip.AddrPort
	Path           string
	RTT            time.Duration
	RxBytes        uint64
	TxBytes        uint64
	LastSeen       time.Time
	Direct         bool
}

// Table is a concurrency-safe peer store. Every read returns a deep copy.
type Table struct {
	mu    sync.RWMutex
	peers map[uint64]*Peer
}

// NewTable creates an empty peer table.
func NewTable() *Table {
	return &Table{peers: make(map[uint64]*Peer)}
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
	}
	clone := p
	clone.PublicKey = append([]byte(nil), p.PublicKey...)
	clone.Candidates = append([]Candidate(nil), p.Candidates...)
	if clone.State == "" {
		clone.State = StateDown
	}
	t.peers[p.ID] = &clone
	return nil
}

// Remove deletes a peer.
func (t *Table) Remove(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.peers, id)
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

// Len reports the number of peers.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// LookupOverlay maps an overlay address to a peer ID.
func (t *Table) LookupOverlay(addr netip.Addr) (uint64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for id, p := range t.peers {
		if (p.OverlayV4.IsValid() && p.OverlayV4 == addr) || (p.OverlayV6.IsValid() && p.OverlayV6 == addr) {
			return id, true
		}
	}
	return 0, false
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
	if v4.IsValid() && !p.OverlayV4.IsValid() {
		p.OverlayV4 = v4
	}
	if v6.IsValid() && !p.OverlayV6.IsValid() {
		p.OverlayV6 = v6
	}
}

// SetActive records a validated direct path.
func (t *Table) SetActive(id uint64, endpoint netip.AddrPort, rtt time.Duration, seen time.Time) {
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
	p.State = StateUp
	p.Direct = true
	p.LastSeen = seen
}

// SetState updates the link state.
func (t *Table) SetState(id uint64, state State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.peers[id]; ok {
		p.State = state
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
