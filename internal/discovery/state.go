// Package discovery exchanges authenticated peer and route advertisements and
// publishes this node on the local link over mDNS.
package discovery

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
)

// State limits. A STATE datagram must fit one frame.
const (
	Version       = 1
	MaxPeers      = 5
	MaxCandidates = 8
	MaxRoutes     = 8
	MaxPath       = route.MaxHops
	headerSize    = 1 + 8 + 8 + 4
	maxStateBytes = 1400 - 54
)

// PeerRecord describes one node in a STATE payload.
type PeerRecord struct {
	ID         uint64
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
	Origin  uint64
	Seq     uint64
	Expires time.Time
	Peers   []PeerRecord
	Routes  []RouteRecord
}

// Encode serializes a bounded STATE payload.
func Encode(s State) ([]byte, error) {
	if s.Origin == 0 {
		return nil, errors.New("discovery: origin must not be zero")
	}
	if len(s.Peers) > MaxPeers {
		return nil, fmt.Errorf("discovery: %d peers exceeds %d", len(s.Peers), MaxPeers)
	}
	if len(s.Routes) > MaxRoutes {
		return nil, fmt.Errorf("discovery: %d routes exceeds %d", len(s.Routes), MaxRoutes)
	}

	buf := make([]byte, 0, 256)
	buf = append(buf, Version)
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
	if payload[0] != Version {
		return State{}, fmt.Errorf("discovery: unsupported state version %d", payload[0])
	}
	off := 1
	s := State{
		Origin:  binary.BigEndian.Uint64(payload[off : off+8]),
		Seq:     binary.BigEndian.Uint64(payload[off+8 : off+16]),
		Expires: time.Now().Add(time.Duration(binary.BigEndian.Uint32(payload[off+16:off+20])) * time.Second),
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
	if off != len(payload) {
		return State{}, errors.New("discovery: trailing bytes in state")
	}
	return s, nil
}

// Build assembles a bounded advertisement. The first peer record must be the
// local node; later records are hints about other members. Routes are ordered
// by hop count and peer ID, and the payload is trimmed to fit one frame while
// preserving the local record.
func Build(peers []PeerRecord, routes []RouteRecord, seq uint64, ttl time.Duration, now time.Time) (State, error) {
	if len(peers) == 0 {
		return State{}, errors.New("discovery: at least the local peer record is required")
	}
	local := peers[0]
	s := State{
		Origin:  local.ID,
		Seq:     seq,
		Expires: now.Add(ttl),
		Peers:   append([]PeerRecord(nil), peers...),
		Routes:  append([]RouteRecord(nil), routes...),
	}
	sortRoutes(s.Routes)
	for {
		if _, err := Encode(s); err == nil {
			return s, nil
		}
		// Drop the least useful route, then the least useful peer hint.
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

	seq map[uint64]uint64
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
	if last, ok := m.seq[from]; ok && s.Seq <= last {
		return fmt.Errorf("discovery: stale state sequence %d from %016x", s.Seq, from)
	}
	m.seq[from] = s.Seq

	for _, rec := range s.Peers {
		if rec.ID == m.LocalID {
			continue
		}
		if _, ok := m.Peers.Get(rec.ID); !ok {
			// Unknown peers are accepted as hints; the key is learned from the
			// invite or a future handshake, never from gossip.
			continue
		}
		// Overlay addresses are needed to route packets and can only come from
		// an authenticated peer. Keys are never taken from gossip.
		m.Peers.SetOverlay(rec.ID, rec.OverlayV4, rec.OverlayV6)
		for _, cand := range rec.Candidates {
			m.Peers.AddCandidate(rec.ID, cand, peer.SourceState)
		}
	}

	for _, rec := range s.Routes {
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
	return nil
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
