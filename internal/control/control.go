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

	"github.com/Utsav-X-Bit/skein/internal/discovery"
	"github.com/Utsav-X-Bit/skein/internal/peer"
	"github.com/Utsav-X-Bit/skein/internal/route"
	"github.com/Utsav-X-Bit/skein/internal/wire"
)

// Timing rules from the protocol.
const (
	PingInterval  = 15 * time.Second
	PeerTimeout   = 45 * time.Second
	StateInterval = 5 * time.Second
	ProbeTimeout  = 2 * time.Second
)

// Transport is the control layer's view of the dataplane.
type Transport interface {
	SendControl(peerID uint64, typ wire.MessageType, payload []byte) error
	SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error
	Rekey(peerID uint64) error
	EnsureSession(peerID uint64) error
}

// Local describes this node's advertisement.
type Local struct {
	ID         uint64
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

	mu         sync.Mutex
	lastPing   map[uint64]time.Time
	lastState  time.Time
	seq        uint64
	challenges map[[16]byte][]chan challengeReply
}

// New creates a controller.
func New(local Local, peers *peer.Table, routes *route.Table, merger *discovery.Merger, eng Transport) (*Controller, error) {
	if local.ID == 0 {
		return nil, errors.New("control: local peer ID must not be zero")
	}
	if peers == nil || routes == nil || merger == nil || eng == nil {
		return nil, errors.New("control: all dependencies are required")
	}
	return &Controller{
		local:      local,
		peers:      peers,
		routes:     routes,
		merger:     merger,
		eng:        eng,
		now:        time.Now,
		lastPing:   make(map[uint64]time.Time),
		challenges: make(map[[16]byte][]chan challengeReply),
	}, nil
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
			c.peers.SetState(p.ID, peer.StateDown)
			c.routes.RemoveDirect(p.ID)
			continue
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
	c.mu.Unlock()

	records := []discovery.PeerRecord{{
		ID:         c.local.ID,
		OverlayV4:  c.local.OverlayV4,
		OverlayV6:  c.local.OverlayV6,
		Candidates: c.local.Candidates,
	}}
	routes := make([]discovery.RouteRecord, 0, 8)
	for _, r := range c.routes.Routes() {
		routes = append(routes, discovery.RouteRecord{
			Destination: r.Destination,
			Cost:        r.Cost,
			Hops:        r.Hops,
			Path:        r.Path,
		})
	}
	state, err := discovery.Build(records[0], routes, seq, discovery.TTLFor, now)
	if err != nil {
		return nil, err
	}
	return discovery.Encode(state)
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
	switch typ {
	case wire.Pong:
		return
	case wire.State:
		_ = c.merger.Merge(from, payload)
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
	var sent int
	for _, cand := range p.Candidates {
		if err := c.eng.SendControlTo(peerID, cand.Addr, wire.Challenge, token[:]); err == nil {
			sent++
		}
	}
	if sent == 0 {
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
		c.peers.SetActive(peerID, reply.addr, rtt, reply.at)
		c.peers.AddCandidate(peerID, reply.addr, peer.SourceProbe)
		return nil
	}
}

func pingPayload(now time.Time) []byte {
	out := make([]byte, 16)
	binary.BigEndian.PutUint64(out[0:8], uint64(now.UnixNano()))
	binary.BigEndian.PutUint64(out[8:16], uint64(now.UnixNano()))
	return out
}
