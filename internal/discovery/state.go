// Package discovery exchanges authenticated peer and route advertisements and
// publishes this node on the local link over mDNS.
package discovery

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Utsav-X-bit/skein/internal/activity"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
)

// State limits. A STATE datagram must fit one frame.
const (
	// Version 2 adds an optional human-readable name per peer record; version 3
	// adds the peer's self-certifying public key so a mesh can form without
	// every pair exchanging invites.
	Version = 4
	// VersionDeparted (M4 P8) adds a departed-peer section. It is written
	// only when a tombstone is actually being carried, so ordinary gossip
	// stays at version 4 and pre-M4 nodes keep accepting it; a version 5
	// STATE is rejected by pre-M4 decoders, which is the bounded cost
	// of a leave announcement during a mixed-version rollout.
	VersionDeparted = 5
	minVersion      = 1
	MaxPeers        = 5
	MaxCandidates   = 8
	MaxRoutes       = 8
	MaxActivity     = 8
	MaxPath         = route.MaxHops
	headerSize      = 1 + 8 + 8 + 4
	maxStateBytes   = 1400 - 54
)

// PeerRecord describes one node in a STATE payload.
type PeerRecord struct {
	ID         uint64
	Name       string
	PublicKey  []byte
	OverlayV4  netip.Addr
	OverlayV6  netip.Addr
	Candidates []netip.AddrPort
}

// RouteRecord is one route the origin offers.
type RouteRecord struct {
	Destination uint64
	Cost        time.Duration
	Hops        uint8
	Path        []uint64
}

// State is one authenticated advertisement.
type State struct {
	Origin   uint64
	Seq      uint64
	Expires  time.Time
	Peers    []PeerRecord
	Routes   []RouteRecord
	Activity []activity.Record
	// Departed lists peer IDs that have gracefully left the mesh (M4 P8,
	// ADR-002). A departure announced by the departed peer itself
	// (Origin == ID) is trusted immediately; a re-gossiped departure
	// (Origin != ID) needs corroboration at the control layer. Departed
	// is a tombstone, not a ban: the leaver still holds its credential
	// until rotation, and rejoin requires a fresh invite.
	Departed []uint64
}

// Encode serializes a bounded STATE payload.
func Encode(s State) ([]byte, error) {
	version := byte(Version)
	if len(s.Departed) > 0 {
		version = VersionDeparted
	}
	if s.Origin == 0 {
		return nil, errors.New("discovery: origin must not be zero")
	}
	if len(s.Peers) > MaxPeers {
		return nil, fmt.Errorf("discovery: %d peers exceeds %d", len(s.Peers), MaxPeers)
	}
	if len(s.Routes) > MaxRoutes {
		return nil, fmt.Errorf("discovery: %d routes exceeds %d", len(s.Routes), MaxRoutes)
	}
	if len(s.Activity) > MaxActivity {
		return nil, fmt.Errorf("discovery: %d activity records exceeds %d", len(s.Activity), MaxActivity)
	}

	buf := make([]byte, 0, 256)
	buf = append(buf, version)
	buf = binary.BigEndian.AppendUint64(buf, s.Origin)
	buf = binary.BigEndian.AppendUint64(buf, s.Seq)
	ttl := time.Until(s.Expires)
	if ttl < 0 {
		ttl = 0
	}
	buf = binary.BigEndian.AppendUint32(buf, uint32(ttl/time.Second))
	buf = append(buf, byte(len(s.Peers)))

	for _, p := range s.Peers {
		if p.ID == 0 {
			return nil, errors.New("discovery: peer ID must not be zero")
		}
		if len(p.Candidates) > MaxCandidates {
			return nil, fmt.Errorf("discovery: %d candidates exceeds %d", len(p.Candidates), MaxCandidates)
		}
		buf = binary.BigEndian.AppendUint64(buf, p.ID)
		var flags byte
		if p.OverlayV4.IsValid() {
			flags |= 1
		}
		if p.OverlayV6.IsValid() {
			flags |= 2
		}
		buf = append(buf, flags)
		if flags&1 != 0 {
			if !p.OverlayV4.Is4() {
				return nil, fmt.Errorf("discovery: overlay IPv4 %s is not IPv4", p.OverlayV4)
			}
			v4 := p.OverlayV4.As4()
			buf = append(buf, v4[:]...)
		}
		if flags&2 != 0 {
			if !p.OverlayV6.Is6() {
				return nil, fmt.Errorf("discovery: overlay IPv6 %s is not IPv6", p.OverlayV6)
			}
			v6 := p.OverlayV6.As16()
			buf = append(buf, v6[:]...)
		}
		buf = append(buf, byte(len(p.Candidates)))
		for _, c := range p.Candidates {
			encoded, err := appendEndpoint(nil, c)
			if err != nil {
				return nil, err
			}
			buf = append(buf, encoded...)
		}
		name := p.Name
		if len(name) > 32 {
			// The wire length is bytes; cut on a rune boundary.
			cut := 32
			for cut > 0 && !utf8.RuneStart(name[cut]) {
				cut--
			}
			name = name[:cut]
		}
		if err := validateName(name); err != nil {
			return nil, err
		}
		buf = append(buf, byte(len(name)))
		buf = append(buf, name...)
		if len(p.PublicKey) != identity.KeySize {
			return nil, fmt.Errorf("discovery: peer %016x has no public key", p.ID)
		}
		derived, err := identity.PeerID(p.PublicKey)
		if err != nil || derived != p.ID {
			return nil, fmt.Errorf("discovery: peer %016x has a mismatched public key", p.ID)
		}
		buf = append(buf, p.PublicKey...)
	}

	buf = append(buf, byte(len(s.Routes)))
	for _, r := range s.Routes {
		if r.Destination == 0 || len(r.Path) == 0 || len(r.Path) > MaxPath {
			return nil, fmt.Errorf("discovery: invalid route to %016x", r.Destination)
		}
		if r.Path[len(r.Path)-1] != r.Destination {
			return nil, fmt.Errorf("discovery: route path does not end at %016x", r.Destination)
		}
		buf = binary.BigEndian.AppendUint64(buf, r.Destination)
		buf = binary.BigEndian.AppendUint32(buf, uint32(r.Cost/time.Millisecond))
		buf = append(buf, r.Hops, byte(len(r.Path)))
		for _, id := range r.Path {
			buf = binary.BigEndian.AppendUint64(buf, id)
		}
	}
	buf = append(buf, byte(len(s.Activity)))
	for _, r := range s.Activity {
		if r.Destination == 0 || len(r.Path) > MaxPath || (!r.Direct && (len(r.Path) == 0 || r.Path[len(r.Path)-1] != r.Destination)) || r.Transport > activity.TransportMixed || r.ObservedAt.IsZero() {
			return nil, fmt.Errorf("discovery: invalid activity to %016x", r.Destination)
		}
		buf = binary.BigEndian.AppendUint64(buf, r.Destination)
		buf = binary.BigEndian.AppendUint64(buf, r.RxBytes)
		buf = binary.BigEndian.AppendUint64(buf, r.TxBytes)
		buf = binary.BigEndian.AppendUint64(buf, uint64(r.ObservedAt.UnixMilli()))
		buf = binary.BigEndian.AppendUint32(buf, uint32(r.RTT/time.Microsecond))
		buf = binary.BigEndian.AppendUint32(buf, uint32(r.Jitter/time.Microsecond))
		flags := byte(0)
		if r.Direct {
			flags = 1
		}
		buf = append(buf, flags, byte(r.Transport), byte(len(r.Path)))
		for _, id := range r.Path {
			buf = binary.BigEndian.AppendUint64(buf, id)
		}
	}
	if version == VersionDeparted {
		if len(s.Departed) > MaxPeers {
			return nil, fmt.Errorf("discovery: %d departed peers exceeds %d", len(s.Departed), MaxPeers)
		}
		buf = append(buf, byte(len(s.Departed)))
		for _, id := range s.Departed {
			if id == 0 {
				return nil, errors.New("discovery: departed peer ID must not be zero")
			}
			buf = binary.BigEndian.AppendUint64(buf, id)
		}
	}
	if len(buf) > maxStateBytes {
		return nil, fmt.Errorf("discovery: state is %d bytes, maximum is %d", len(buf), maxStateBytes)
	}
	return buf, nil
}

// Decode parses and validates a STATE payload.
func Decode(payload []byte) (State, error) {
	if len(payload) < headerSize {
		return State{}, errors.New("discovery: state is truncated")
	}
	version := payload[0]
	if version < minVersion || version > VersionDeparted {
		return State{}, fmt.Errorf("discovery: unsupported state version %d", version)
	}
	off := 1
	ttl := time.Duration(binary.BigEndian.Uint32(payload[off+16:off+20])) * time.Second
	if ttl > TTLFor {
		// A member cannot keep stale routes alive indefinitely.
		ttl = TTLFor
	}
	s := State{
		Origin:  binary.BigEndian.Uint64(payload[off : off+8]),
		Seq:     binary.BigEndian.Uint64(payload[off+8 : off+16]),
		Expires: time.Now().Add(ttl),
	}
	off += 20
	if s.Origin == 0 {
		return State{}, errors.New("discovery: state origin is zero")
	}
	if off >= len(payload) {
		return State{}, errors.New("discovery: missing peer count")
	}
	peerCount := int(payload[off])
	off++
	if peerCount > MaxPeers {
		return State{}, fmt.Errorf("discovery: %d peers exceeds %d", peerCount, MaxPeers)
	}
	for range peerCount {
		if off+9 > len(payload) {
			return State{}, errors.New("discovery: truncated peer record")
		}
		rec := PeerRecord{ID: binary.BigEndian.Uint64(payload[off : off+8])}
		flags := payload[off+8]
		off += 9
		if rec.ID == 0 {
			return State{}, errors.New("discovery: peer ID is zero")
		}
		if flags&1 != 0 {
			if off+4 > len(payload) {
				return State{}, errors.New("discovery: truncated IPv4 address")
			}
			rec.OverlayV4 = netip.AddrFrom4([4]byte(payload[off : off+4]))
			off += 4
		}
		if flags&2 != 0 {
			if off+16 > len(payload) {
				return State{}, errors.New("discovery: truncated IPv6 address")
			}
			rec.OverlayV6 = netip.AddrFrom16([16]byte(payload[off : off+16]))
			off += 16
		}
		if flags&^byte(3) != 0 {
			return State{}, fmt.Errorf("discovery: unknown peer flags %#x", flags)
		}
		if off >= len(payload) {
			return State{}, errors.New("discovery: missing candidate count")
		}
		candCount := int(payload[off])
		off++
		if candCount > MaxCandidates {
			return State{}, fmt.Errorf("discovery: %d candidates exceeds %d", candCount, MaxCandidates)
		}
		for range candCount {
			addr, n, err := parseEndpoint(payload[off:])
			if err != nil {
				return State{}, err
			}
			off += n
			rec.Candidates = append(rec.Candidates, addr)
		}
		if version >= 2 {
			if off >= len(payload) {
				return State{}, errors.New("discovery: missing peer name")
			}
			nameLen := int(payload[off])
			off++
			if nameLen > 32 || off+nameLen > len(payload) {
				return State{}, errors.New("discovery: invalid peer name length")
			}
			rec.Name = string(payload[off : off+nameLen])
			off += nameLen
			if err := validateName(rec.Name); err != nil {
				return State{}, err
			}
		}
		if version >= 3 {
			if off+identity.KeySize > len(payload) {
				return State{}, errors.New("discovery: missing peer public key")
			}
			rec.PublicKey = append([]byte(nil), payload[off:off+identity.KeySize]...)
			off += identity.KeySize
			derived, err := identity.PeerID(rec.PublicKey)
			if err != nil || derived != rec.ID {
				return State{}, fmt.Errorf("discovery: peer %016x key does not match its ID", rec.ID)
			}
		}
		s.Peers = append(s.Peers, rec)
	}

	if off >= len(payload) {
		return State{}, errors.New("discovery: missing route count")
	}
	routeCount := int(payload[off])
	off++
	if routeCount > MaxRoutes {
		return State{}, fmt.Errorf("discovery: %d routes exceeds %d", routeCount, MaxRoutes)
	}
	for range routeCount {
		if off+14 > len(payload) {
			return State{}, errors.New("discovery: truncated route")
		}
		rec := RouteRecord{
			Destination: binary.BigEndian.Uint64(payload[off : off+8]),
			Cost:        time.Duration(binary.BigEndian.Uint32(payload[off+8:off+12])) * time.Millisecond,
			Hops:        payload[off+12],
		}
		pathLen := int(payload[off+13])
		off += 14
		if pathLen == 0 || pathLen > MaxPath {
			return State{}, fmt.Errorf("discovery: invalid route path length %d", pathLen)
		}
		if off+8*pathLen > len(payload) {
			return State{}, errors.New("discovery: truncated route path")
		}
		for range pathLen {
			rec.Path = append(rec.Path, binary.BigEndian.Uint64(payload[off:off+8]))
			off += 8
		}
		if rec.Destination == 0 || rec.Path[len(rec.Path)-1] != rec.Destination {
			return State{}, errors.New("discovery: route path does not match its destination")
		}
		s.Routes = append(s.Routes, rec)
	}
	if version >= 4 {
		if off >= len(payload) {
			return State{}, errors.New("discovery: missing activity count")
		}
		count := int(payload[off])
		off++
		if count > MaxActivity {
			return State{}, fmt.Errorf("discovery: %d activity records exceeds %d", count, MaxActivity)
		}
		for range count {
			if off+43 > len(payload) {
				return State{}, errors.New("discovery: truncated activity")
			}
			rec := activity.Record{
				Destination: binary.BigEndian.Uint64(payload[off : off+8]),
				RxBytes:     binary.BigEndian.Uint64(payload[off+8 : off+16]),
				TxBytes:     binary.BigEndian.Uint64(payload[off+16 : off+24]),
				ObservedAt:  time.UnixMilli(int64(binary.BigEndian.Uint64(payload[off+24 : off+32]))),
				RTT:         time.Duration(binary.BigEndian.Uint32(payload[off+32:off+36])) * time.Microsecond,
				Jitter:      time.Duration(binary.BigEndian.Uint32(payload[off+36:off+40])) * time.Microsecond,
			}
			flags, transport, pathLen := payload[off+40], activity.Transport(payload[off+41]), int(payload[off+42])
			off += 43
			if rec.Destination == 0 || flags&^byte(1) != 0 || transport > activity.TransportMixed || pathLen > MaxPath || off+8*pathLen > len(payload) {
				return State{}, errors.New("discovery: invalid activity record")
			}
			rec.Direct, rec.Transport = flags&1 != 0, transport
			for range pathLen {
				rec.Path = append(rec.Path, binary.BigEndian.Uint64(payload[off:off+8]))
				off += 8
			}
			if !rec.Direct && (len(rec.Path) == 0 || rec.Path[len(rec.Path)-1] != rec.Destination) {
				return State{}, errors.New("discovery: activity path does not match destination")
			}
			s.Activity = append(s.Activity, rec)
		}
	}
	if version >= VersionDeparted {
		if off >= len(payload) {
			return State{}, errors.New("discovery: missing departed count")
		}
		count := int(payload[off])
		off++
		if count > MaxPeers || off+8*count > len(payload) {
			return State{}, errors.New("discovery: invalid departed section")
		}
		for range count {
			id := binary.BigEndian.Uint64(payload[off : off+8])
			off += 8
			if id == 0 {
				return State{}, errors.New("discovery: departed peer ID is zero")
			}
			s.Departed = append(s.Departed, id)
		}
	}
	if off != len(payload) {
		return State{}, errors.New("discovery: trailing bytes in state")
	}
	return s, nil
}

// Build assembles a bounded advertisement. The first peer record must be the
// local node; later records are hints about other members. Routes are ordered
// by hop count and peer ID, and the payload is trimmed to fit one frame while
// preserving the local record.
func Build(peers []PeerRecord, routes []RouteRecord, records []activity.Record, seq uint64, ttl time.Duration, now time.Time) (State, error) {
	return BuildWithDeparted(peers, routes, records, nil, seq, ttl, now)
}

// BuildWithDeparted is Build plus the tombstones to gossip (M4 P8).
// Departures are trimmed last: a tombstone is small (9 bytes) and losing
// one silently would resurrect a departed peer's routes on recipients.
func BuildWithDeparted(peers []PeerRecord, routes []RouteRecord, records []activity.Record, departed []uint64, seq uint64, ttl time.Duration, now time.Time) (State, error) {
	if len(peers) == 0 {
		return State{}, errors.New("discovery: at least the local peer record is required")
	}
	local := peers[0]
	s := State{
		Origin:   local.ID,
		Seq:      seq,
		Expires:  now.Add(ttl),
		Peers:    append([]PeerRecord(nil), peers...),
		Routes:   append([]RouteRecord(nil), routes...),
		Activity: append([]activity.Record(nil), records...),
		Departed: append([]uint64(nil), departed...),
	}
	sortRoutes(s.Routes)
	// The peer count is a hard limit; trim it before sacrificing routes so a
	// large mesh still advertises its paths.
	if len(s.Peers) > MaxPeers {
		s.Peers = s.Peers[:MaxPeers]
	}
	for {
		if _, err := Encode(s); err == nil {
			return s, nil
		}
		// Telemetry is least valuable, then over-cap candidates, then
		// routes, then peer hints. Candidates were the missing stage
		// (M4 F-02): a host with >8 addresses previously produced no
		// STATE at all, because Encode errors on >MaxCandidates and
		// nothing ever trimmed them.
		if len(s.Activity) > 0 {
			s.Activity = s.Activity[:len(s.Activity)-1]
			continue
		}
		if trimCandidates(&s) {
			continue
		}
		if len(s.Routes) > 0 {
			s.Routes = s.Routes[:len(s.Routes)-1]
			continue
		}
		if len(s.Peers) > 1 {
			s.Peers = s.Peers[:len(s.Peers)-1]
			continue
		}
		return State{}, errors.New("discovery: local record alone exceeds the state size limit")
	}
}

// candidateRank orders endpoints for trimming: a public (global, non-private)
// address is the one a remote peer can actually dial, so it survives ahead
// of LAN addresses, which survive ahead of link-local/loopback. Invalid
// endpoints (unspecified, multicast, port 0) are dropped outright — they
// could never be dialed and Encode would reject the whole STATE for them.
func candidateRank(c netip.AddrPort) int {
	if !c.IsValid() || c.Port() == 0 || c.Addr().IsUnspecified() || c.Addr().IsMulticast() {
		return 99
	}
	a := c.Addr()
	if a.IsLoopback() || a.IsLinkLocalUnicast() {
		return 2
	}
	if a.IsPrivate() {
		return 1
	}
	return 0
}

// trimCandidates dedupes every peer record's candidates, drops invalid ones,
// and caps each record at MaxCandidates, trimming hint peers before the
// local record (peers[0]). It reports whether it changed anything.
func trimCandidates(s *State) bool {
	changed := false
	// Hint peers first, local record last: the local record is the one
	// every recipient needs most.
	for idx := len(s.Peers) - 1; idx >= 0; idx-- {
		rec := &s.Peers[idx]
		seen := make(map[netip.AddrPort]bool, len(rec.Candidates))
		kept := rec.Candidates[:0]
		for _, c := range rec.Candidates {
			c = netip.AddrPortFrom(c.Addr().Unmap(), c.Port())
			if candidateRank(c) == 99 || seen[c] {
				changed = true
				continue
			}
			seen[c] = true
			kept = append(kept, c)
		}
		rec.Candidates = kept
		// Stable sort by rank, then address, so trimming is deterministic
		// and a public candidate is never the one dropped for a LAN one.
		for i := 1; i < len(rec.Candidates); i++ {
			for j := i; j > 0; j-- {
				a, b := rec.Candidates[j], rec.Candidates[j-1]
				if candidateRank(a) < candidateRank(b) ||
					(candidateRank(a) == candidateRank(b) && a.String() < b.String()) {
					rec.Candidates[j], rec.Candidates[j-1] = b, a
					continue
				}
				break
			}
		}
		if len(rec.Candidates) > MaxCandidates {
			rec.Candidates = rec.Candidates[:MaxCandidates]
			changed = true
		}
	}
	return changed
}

func sortRoutes(routes []RouteRecord) {
	for i := 1; i < len(routes); i++ {
		for j := i; j > 0; j-- {
			if lessRoute(routes[j], routes[j-1]) {
				routes[j], routes[j-1] = routes[j-1], routes[j]
				continue
			}
			break
		}
	}
}

func lessRoute(a, b RouteRecord) bool {
	if a.Hops != b.Hops {
		return a.Hops < b.Hops
	}
	if a.Cost != b.Cost {
		return a.Cost < b.Cost
	}
	return a.Destination < b.Destination
}

// Merger applies authenticated STATE payloads to local peer and route state.
type Merger struct {
	LocalID uint64
	Peers   *peer.Table
	Routes  *route.Table
	Now     func() time.Time
	// OnPeer registers a peer learned from gossip. The key is self-certifying
	// (its hash is the peer ID), so a member cannot impersonate another ID.
	OnPeer     func(id uint64, publicKey []byte) error
	OnActivity func(origin, seq uint64, records []activity.Record, expires time.Time)
	// OnDeparted reports tombstones carried by a STATE (M4 P8). The
	// control layer decides trust (self-announced vs corroborated);
	// the merger only delivers them, in STATE order.
	OnDeparted func(origin, seq uint64, departed []uint64, expires time.Time)

	localV4 netip.Addr
	localV6 netip.Addr

	localV4Prefix netip.Prefix
	localV6Prefix netip.Prefix

	mu  sync.Mutex
	seq map[uint64]uint64
}

// SetLocalOverlay tells the merger which overlay addresses belong to this
// node, so a peer record that claims them is ignored.
func (m *Merger) SetLocalOverlay(v4, v6 netip.Addr) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.localV4, m.localV6 = v4, v6
}

// SetLocalPrefixes bounds which overlay addresses a peer record may claim.
func (m *Merger) SetLocalPrefixes(v4, v6 netip.Prefix) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.localV4Prefix, m.localV6Prefix = v4, v6
}

// NewMerger creates a merger.
func NewMerger(localID uint64, peers *peer.Table, routes *route.Table) *Merger {
	return &Merger{LocalID: localID, Peers: peers, Routes: routes, Now: time.Now, seq: map[uint64]uint64{}}
}

// Merge validates a STATE payload received from an authenticated peer.
func (m *Merger) Merge(from uint64, payload []byte) error {
	s, err := Decode(payload)
	if err != nil {
		return err
	}
	if s.Origin != from {
		return fmt.Errorf("discovery: state origin %016x does not match sender %016x", s.Origin, from)
	}
	if s.Expires.Before(m.now()) {
		return errors.New("discovery: state is expired")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.seq[from]; ok && s.Seq <= last {
		return fmt.Errorf("discovery: stale state sequence %d from %016x", s.Seq, from)
	}
	m.seq[from] = s.Seq

	for _, rec := range s.Peers {
		if rec.ID == m.LocalID {
			continue
		}
		if rec.OverlayV4.IsValid() && m.localV4Prefix.IsValid() && !m.localV4Prefix.Contains(rec.OverlayV4) {
			rec.OverlayV4 = netip.Addr{}
		}
		if rec.OverlayV6.IsValid() && m.localV6Prefix.IsValid() && !m.localV6Prefix.Contains(rec.OverlayV6) {
			rec.OverlayV6 = netip.Addr{}
		}
		if rec.OverlayV4.IsValid() && !usableOverlay(rec.OverlayV4) {
			rec.OverlayV4 = netip.Addr{}
		}
		if rec.OverlayV6.IsValid() && !usableOverlay(rec.OverlayV6) {
			rec.OverlayV6 = netip.Addr{}
		}
		if (m.localV4.IsValid() && rec.OverlayV4 == m.localV4) || (m.localV6.IsValid() && rec.OverlayV6 == m.localV6) {
			// A stale identity from an earlier mesh can carry this node's
			// overlay address; never let it shadow the local node.
			continue
		}
		if _, ok := m.Peers.Get(rec.ID); !ok {
			// An unknown peer is registered from its self-certifying key, so
			// the mesh forms without every pair exchanging invites.
			if len(rec.PublicKey) != identity.KeySize || m.OnPeer == nil {
				continue
			}
			if err := m.OnPeer(rec.ID, rec.PublicKey); err != nil {
				continue
			}
		}
		if rec.ID == from {
			// A node may move its own overlay address; nobody else can.
			m.Peers.UpdateOverlay(rec.ID, rec.OverlayV4, rec.OverlayV6)
		} else {
			m.Peers.SetOverlay(rec.ID, rec.OverlayV4, rec.OverlayV6)
		}
		m.Peers.SetName(rec.ID, rec.Name)
		for _, cand := range rec.Candidates {
			if (m.localV4Prefix.IsValid() && m.localV4Prefix.Contains(cand.Addr())) ||
				(m.localV6Prefix.IsValid() && m.localV6Prefix.Contains(cand.Addr())) {
				// An underlay endpoint inside the overlay would loop the
				// datagram back through the tunnel.
				continue
			}
			m.Peers.AddCandidate(rec.ID, cand, peer.SourceState)
		}
	}

	present := make(map[uint64]bool, len(s.Routes))
	for _, rec := range s.Routes {
		present[rec.Destination] = true
		if rec.Destination == m.LocalID {
			continue
		}
		path := make([]uint64, 0, len(rec.Path)+1)
		path = append(path, from)
		path = append(path, rec.Path...)
		adv := route.Advertisement{
			Destination: rec.Destination,
			Cost:        rec.Cost,
			Hops:        uint8(len(path) - 1),
			Path:        path,
			Seq:         s.Seq,
			Expires:     s.Expires,
		}
		if err := m.Routes.MergeAdvertisement(from, adv); err != nil {
			continue
		}
	}
	// Snapshot reconciliation (M4 F-08): routes this origin advertised
	// before but omits now are withdrawn, promptly.
	m.Routes.ReconcileOrigin(from, s.Seq, present)
	if m.OnActivity != nil && len(s.Activity) > 0 {
		records := make([]activity.Record, len(s.Activity))
		copy(records, s.Activity)
		for i := range records {
			records[i].Path = append([]uint64(nil), records[i].Path...)
		}
		m.OnActivity(s.Origin, s.Seq, records, s.Expires)
	}
	if m.OnDeparted != nil && len(s.Departed) > 0 {
		m.OnDeparted(s.Origin, s.Seq, append([]uint64(nil), s.Departed...), s.Expires)
	}
	return nil
}

// Reset forgets a peer's STATE sequence after a new authenticated session is
// established. A restarted daemon begins its controller sequence at one; its
// state must be accepted on the new session rather than held behind a sequence
// value from the peer's previous session.
func (m *Merger) Reset(origin uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seq, origin)
}

// validateName mirrors the configuration rule for node names.
func validateName(name string) error {
	if name == "" {
		return nil
	}
	if len([]rune(name)) > 32 {
		return fmt.Errorf("discovery: peer name is %d characters, maximum is 32", len([]rune(name)))
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("discovery: peer name contains a control or formatting character")
		}
	}
	return nil
}

// usableOverlay rejects addresses that cannot identify a unicast host.
func usableOverlay(a netip.Addr) bool {
	return !a.IsUnspecified() && !a.IsMulticast() && !a.IsLoopback() && !a.IsLinkLocalUnicast()
}

func (m *Merger) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func appendEndpoint(dst []byte, addr netip.AddrPort) ([]byte, error) {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if !addr.IsValid() || addr.Port() == 0 || addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() {
		return nil, fmt.Errorf("discovery: invalid endpoint %s", addr)
	}
	if addr.Addr().Is4() {
		v4 := addr.Addr().As4()
		dst = append(dst, 4)
		dst = append(dst, v4[:]...)
	} else {
		v6 := addr.Addr().As16()
		dst = append(dst, 6)
		dst = append(dst, v6[:]...)
	}
	return binary.BigEndian.AppendUint16(dst, addr.Port()), nil
}

func parseEndpoint(raw []byte) (netip.AddrPort, int, error) {
	if len(raw) < 1 {
		return netip.AddrPort{}, 0, errors.New("discovery: missing address family")
	}
	switch raw[0] {
	case 4:
		if len(raw) < 7 {
			return netip.AddrPort{}, 0, errors.New("discovery: truncated IPv4 endpoint")
		}
		addr := netip.AddrFrom4([4]byte(raw[1:5]))
		port := binary.BigEndian.Uint16(raw[5:7])
		return validateEndpoint(addr, port, 7)
	case 6:
		if len(raw) < 19 {
			return netip.AddrPort{}, 0, errors.New("discovery: truncated IPv6 endpoint")
		}
		addr := netip.AddrFrom16([16]byte(raw[1:17]))
		port := binary.BigEndian.Uint16(raw[17:19])
		return validateEndpoint(addr, port, 19)
	default:
		return netip.AddrPort{}, 0, fmt.Errorf("discovery: unknown address family %d", raw[0])
	}
}

func validateEndpoint(addr netip.Addr, port uint16, size int) (netip.AddrPort, int, error) {
	if port == 0 || addr.IsUnspecified() || addr.IsMulticast() {
		return netip.AddrPort{}, 0, fmt.Errorf("discovery: invalid endpoint %s", netip.AddrPortFrom(addr, port))
	}
	return netip.AddrPortFrom(addr.Unmap(), port), size, nil
}
