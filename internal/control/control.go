// Package control drives keepalives, state synchronisation, rekeying, peer
// timeout detection, and candidate endpoint probing.
package control

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/activity"
	"github.com/Utsav-X-bit/skein/internal/discovery"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

// Timing rules from the protocol.
const (
	PingInterval  = 15 * time.Second
	PeerTimeout   = 45 * time.Second
	StateInterval = 5 * time.Second
	ProbeTimeout  = 2 * time.Second
	PunchInterval = 30 * time.Second
)

// Transport is the control layer's view of the dataplane.
type Transport interface {
	SendControl(peerID uint64, typ wire.MessageType, payload []byte) error
	SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error
	Rekey(peerID uint64) error
	EnsureSession(peerID uint64) error
	DropSession(peerID uint64)
	Punch(peerID uint64, addr netip.AddrPort) error
}

// Local describes this node's advertisement.
type Local struct {
	ID         uint64
	Name       string
	PublicKey  []byte
	OverlayV4  netip.Addr
	OverlayV6  netip.Addr
	Candidates []netip.AddrPort
}

// Controller owns the periodic control loop.
type Controller struct {
	local  Local
	peers  *peer.Table
	routes *route.Table
	merger *discovery.Merger
	eng    Transport
	now    func() time.Time

	mu          sync.Mutex
	lastPing    map[uint64]time.Time
	lastPunch   map[uint64]time.Time
	lastPunchIn map[uint64]time.Time
	observed    map[uint64]netip.AddrPort
	lastState   time.Time
	seq         uint64
	challenges  map[[16]byte][]chan challengeReply
	trafficPrev map[uint64]trafficSample
	lastRTT     map[uint64]time.Duration
}

type trafficSample struct{ rx, tx uint64 }

// New creates a controller.
func New(local Local, peers *peer.Table, routes *route.Table, merger *discovery.Merger, eng Transport) (*Controller, error) {
	if local.ID == 0 {
		return nil, errors.New("control: local peer ID must not be zero")
	}
	if peers == nil || routes == nil || merger == nil || eng == nil {
		return nil, errors.New("control: all dependencies are required")
	}
	return &Controller{
		local:       local,
		peers:       peers,
		routes:      routes,
		merger:      merger,
		eng:         eng,
		now:         time.Now,
		lastPing:    make(map[uint64]time.Time),
		lastPunch:   make(map[uint64]time.Time),
		lastPunchIn: make(map[uint64]time.Time),
		observed:    make(map[uint64]netip.AddrPort),
		challenges:  make(map[[16]byte][]chan challengeReply),
		trafficPrev: make(map[uint64]trafficSample),
		lastRTT:     make(map[uint64]time.Duration),
	}, nil
}

// SetLocalCandidates changes the advertised underlay endpoints without a
// restart, so a discovered public address takes effect promptly.
func (c *Controller) SetLocalCandidates(cands []netip.AddrPort) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.local.Candidates = append([]netip.AddrPort(nil), cands...)
}

// SetLocalName changes the name advertised in STATE without a restart.
func (c *Controller) SetLocalName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.local.Name = name
}

// SetClock overrides the clock for tests.
func (c *Controller) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Run drives the control loop until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.Tick()
		}
	}
}

// Tick performs one control pass.
func (c *Controller) Tick() {
	now := c.now()
	for _, p := range c.peers.List() {
		// Configured dialers connect without waiting for overlay traffic.
		if p.Dialer {
			_ = c.eng.EnsureSession(p.ID)
		}
		if p.State == peer.StateUp && !p.LastSeen.IsZero() && now.Sub(p.LastSeen) > PeerTimeout {
			// The path is dead: forget the session and the endpoint, or the
			// dialer would keep encrypting for an unreachable address.
			c.peers.SetState(p.ID, peer.StateDown)
			c.peers.ClearActive(p.ID)
			c.routes.RemoveDirect(p.ID)
			c.eng.DropSession(p.ID)
			continue
		}
		if r, ok := c.routes.Lookup(p.ID); ok && r.NextHop != p.ID {
			c.maybePunch(p.ID, r.NextHop, now)
		}
		c.mu.Lock()
		last := c.lastPing[p.ID]
		c.mu.Unlock()
		if last.IsZero() || now.Sub(last) >= PingInterval {
			if err := c.eng.SendControl(p.ID, wire.Ping, pingPayload(now)); err == nil {
				c.mu.Lock()
				c.lastPing[p.ID] = now
				c.mu.Unlock()
			}
		}
		_ = c.eng.Rekey(p.ID)
	}
	if c.lastState.IsZero() || now.Sub(c.lastState) >= StateInterval {
		c.broadcastState(now)
	}
}

func (c *Controller) broadcastState(now time.Time) {
	payload, err := c.statePayload(now)
	if err != nil {
		return
	}
	sent := false
	for _, p := range c.peers.List() {
		if p.State != peer.StateUp {
			continue
		}
		if err := c.eng.SendControl(p.ID, wire.State, payload); err == nil {
			sent = true
		}
	}
	if sent {
		c.lastState = now
	}
}

// statePayload builds a bounded advertisement from local state.
func (c *Controller) statePayload(now time.Time) ([]byte, error) {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	localName := c.local.Name
	localCands := append([]netip.AddrPort(nil), c.local.Candidates...)
	c.mu.Unlock()

	records := []discovery.PeerRecord{{
		ID:         c.local.ID,
		Name:       localName,
		PublicKey:  c.local.PublicKey,
		OverlayV4:  c.local.OverlayV4,
		OverlayV6:  c.local.OverlayV6,
		Candidates: localCands,
	}}
	// Advertise every member this node knows so others can map overlay
	// addresses to peers. Keys still never travel in gossip.
	for _, p := range c.peers.List() {
		if !p.OverlayV4.IsValid() && !p.OverlayV6.IsValid() {
			continue
		}
		rec := discovery.PeerRecord{ID: p.ID, Name: p.Name, PublicKey: p.PublicKey, OverlayV4: p.OverlayV4, OverlayV6: p.OverlayV6}
		for _, cand := range p.Candidates {
			rec.Candidates = append(rec.Candidates, cand.Addr)
		}
		records = append(records, rec)
	}
	routes := make([]discovery.RouteRecord, 0, 8)
	for _, r := range c.routes.Routes() {
		routes = append(routes, discovery.RouteRecord{
			Destination: r.Destination,
			Cost:        r.Cost,
			Hops:        r.Hops,
			Path:        r.Path,
		})
	}
	activities := c.activityRecords(now)
	state, err := discovery.Build(records, routes, activities, seq, discovery.TTLFor, now)
	if err != nil {
		return nil, err
	}
	return discovery.Encode(state)
}

// activityRecords produces aggregate direct-link deltas only. It never
// observes packet contents or individual flows.
func (c *Controller) activityRecords(now time.Time) []activity.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []activity.Record
	for _, p := range c.peers.List() {
		if p.State != peer.StateUp || !p.Direct {
			continue
		}
		prev := c.trafficPrev[p.ID]
		rx, tx := uint64(0), uint64(0)
		if p.RxBytes >= prev.rx {
			rx = p.RxBytes - prev.rx
		}
		if p.TxBytes >= prev.tx {
			tx = p.TxBytes - prev.tx
		}
		c.trafficPrev[p.ID] = trafficSample{p.RxBytes, p.TxBytes}
		jitter := time.Duration(0)
		if old := c.lastRTT[p.ID]; old > 0 && p.RTT > 0 {
			jitter = p.RTT - old
			if jitter < 0 {
				jitter = -jitter
			}
		}
		if p.RTT > 0 {
			c.lastRTT[p.ID] = p.RTT
		}
		if rx == 0 && tx == 0 {
			continue
		}
		out = append(out, activity.Record{Destination: p.ID, RxBytes: rx, TxBytes: tx, RTT: p.RTT, Jitter: jitter, Transport: activity.TransportFromStrings(p.Transport, p.RxTransport), Direct: true, ObservedAt: now, Path: []uint64{p.ID}})
		if len(out) == discovery.MaxActivity {
			break
		}
	}
	return out
}

// HandlePing answers a keepalive with an authenticated Pong.
func (c *Controller) HandlePing(from uint64, payload []byte) []byte {
	c.peers.MarkSeen(from, c.now())
	reply := make([]byte, len(payload))
	copy(reply, payload)
	return reply
}

type challengeReply struct {
	addr netip.AddrPort
	at   time.Time
}

// HandleControl dispatches an encrypted control frame. src is the address the
// frame arrived from.
func (c *Controller) HandleControl(from uint64, src netip.AddrPort, typ wire.MessageType, payload []byte) {
	c.peers.MarkSeen(from, c.now())
	if src.IsValid() {
		c.mu.Lock()
		c.observed[from] = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		c.mu.Unlock()
	}
	switch typ {
	case wire.Pong:
		// The peer echoes the ping payload; its first word is the send time.
		if len(payload) == 16 {
			sent := time.Unix(0, int64(binary.BigEndian.Uint64(payload[0:8])))
			if rtt := c.now().Sub(sent); rtt > 0 && rtt < time.Minute {
				c.peers.SetRTT(from, rtt)
			}
		}
		return
	case wire.State:
		_ = c.merger.Merge(from, payload)
	case wire.Punch:
		c.handlePunch(from, src, payload)
	case wire.Challenge:
		if len(payload) != 16 {
			return
		}
		// Reply over the same path the challenge arrived on.
		_ = c.eng.SendControlTo(from, src, wire.ChallengeReply, payload)
	case wire.ChallengeReply:
		if len(payload) != 16 {
			return
		}
		var token [16]byte
		copy(token[:], payload)
		c.mu.Lock()
		waiters := c.challenges[token]
		delete(c.challenges, token)
		c.mu.Unlock()
		for _, ch := range waiters {
			select {
			case ch <- challengeReply{addr: src, at: c.now()}:
			default:
			}
		}
	}
}

// Probe proves candidate endpoints with a random challenge and moves the
// active path to the fastest validated candidate.
func (c *Controller) Probe(ctx context.Context, peerID uint64) error {
	p, ok := c.peers.Get(peerID)
	if !ok {
		return fmt.Errorf("control: peer %016x is unknown", peerID)
	}
	if len(p.Candidates) == 0 {
		return errors.New("control: peer has no candidate endpoints")
	}

	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	ch := make(chan challengeReply, len(p.Candidates))
	c.mu.Lock()
	c.challenges[token] = append(c.challenges[token], ch)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.challenges, token)
		c.mu.Unlock()
	}()

	sentAt := c.now()
	var (
		sent    int
		lastErr error
	)
	for _, cand := range p.Candidates {
		if err := c.eng.SendControlTo(peerID, cand.Addr, wire.Challenge, token[:]); err == nil {
			sent++
		} else {
			lastErr = err
		}
	}
	if sent == 0 {
		if lastErr != nil {
			return fmt.Errorf("control: cannot probe %016x: %w", peerID, lastErr)
		}
		return errors.New("control: no challenge could be sent")
	}

	timer := time.NewTimer(ProbeTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("control: no candidate answered the challenge")
	case reply := <-ch:
		rtt := reply.at.Sub(sentAt)
		if rtt <= 0 {
			rtt = time.Millisecond
		}
		// The challenge proves the peer is reachable over the path it arrived
		// on, so that source becomes the active endpoint.
		c.peers.SetActive(peerID, reply.addr, rtt, "udp", reply.at)
		c.peers.AddCandidate(peerID, reply.addr, peer.SourceProbe)
		return nil
	}
}

// maybePunch asks the next hop to introduce us to a peer we only reach through
// it, so both sides can try a direct path.
func (c *Controller) maybePunch(peerID, relay uint64, now time.Time) {
	c.mu.Lock()
	last := c.lastPunch[peerID]
	c.mu.Unlock()
	if !last.IsZero() && now.Sub(last) < PunchInterval {
		return
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return
	}
	if err := c.eng.SendControl(relay, wire.Punch, encodePunch(punchRequest{Target: peerID, Nonce: nonce})); err != nil {
		return
	}
	c.mu.Lock()
	c.lastPunch[peerID] = now
	c.mu.Unlock()
}

// handlePunch relays an introduction or dials the endpoint it carries.
func (c *Controller) handlePunch(from uint64, src netip.AddrPort, payload []byte) {
	req, err := decodePunch(payload)
	if err != nil {
		return
	}
	if req.HasAddr {
		// Delivery: only honour it occasionally per peer.
		now := c.now()
		c.mu.Lock()
		last := c.lastPunchIn[req.Target]
		tooSoon := !last.IsZero() && now.Sub(last) < PunchInterval
		if !tooSoon {
			c.lastPunchIn[req.Target] = now
		}
		c.mu.Unlock()
		if tooSoon {
			return
		}
		_ = c.eng.Punch(req.Target, req.Addr)
		return
	}

	// Request: this node is the relay. Report both observed endpoints and ask
	// both sides to dial at once.
	target := req.Target
	if target == 0 || target == from {
		return
	}
	c.mu.Lock()
	fromObs, okFrom := c.observed[from]
	targetObs, okTarget := c.observed[target]
	c.mu.Unlock()
	if !okFrom {
		fromObs = src
	}
	if !okTarget {
		return
	}
	_ = c.eng.SendControl(from, wire.Punch, encodePunch(punchRequest{
		Target: target, Nonce: req.Nonce, Addr: targetObs, HasAddr: true,
	}))
	_ = c.eng.SendControl(target, wire.Punch, encodePunch(punchRequest{
		Target: from, Nonce: req.Nonce, Addr: fromObs, HasAddr: true,
	}))
}

func pingPayload(now time.Time) []byte {
	out := make([]byte, 16)
	binary.BigEndian.PutUint64(out[0:8], uint64(now.UnixNano()))
	binary.BigEndian.PutUint64(out[8:16], uint64(now.UnixNano()))
	return out
}
