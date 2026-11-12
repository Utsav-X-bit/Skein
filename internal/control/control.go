// Package control drives keepalives, state synchronisation, rekeying, peer
// timeout detection, and candidate endpoint probing.
package control

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
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
	// TombstoneRetention bounds how long a departure is re-gossiped
	// (M4 P8): two STATE TTLs (180 s) from learning it. An offline peer
	// that reconnects within that window to any node carrying the
	// tombstone learns the departure; after it, the peer is gone from
	// the mesh by ordinary timeout/expiry anyway. A live STATE from
	// the tombstoned peer itself clears its tombstone immediately
	// (rejoin proof), regardless of the retention clock.
	TombstoneRetention = 2 * discovery.TTLFor
	// DialerFallbackAfter is how long a non-dialer peer may go with no
	// session and no inbound contact at all before this node is allowed
	// to initiate anyway (M4 F-11). One full PeerTimeout of silence is
	// the dead-by-construction signature: the sole dialer has never
	// reached us, so waiting longer cannot help. Once fallback has
	// fired for a peer it stays armed (the pair has proven the
	// configured role does not work); simultaneous handshakes are
	// resolved by the session attempt tie-break.
	DialerFallbackAfter = PeerTimeout
)

// Transport is the control layer's view of the dataplane.
type Transport interface {
	SendControl(peerID uint64, typ wire.MessageType, payload []byte) error
	SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error
	Rekey(peerID uint64) error
	HasSession(peerID uint64) bool
	EnsureSession(peerID uint64) error
	DropSession(peerID uint64)
	Punch(peerID uint64, addr netip.AddrPort) error
	// AllowFallbackDial arms the dataplane to initiate a handshake to a
	// non-dialer peer (M4 F-11); without it ensureSession refuses
	// non-dialers. It is a no-op for unknown peers.
	AllowFallbackDial(peerID uint64)
	// ProbeMTU measures the largest frame that can reach a directly
	// connected peer without IP fragmentation.
	ProbeMTU(peerID uint64, ceiling int) (int, error)
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
	firstSeen   map[uint64]time.Time
	fallbackOn  map[uint64]bool
	tombstones  map[uint64]tombstone
	pendingTomb map[uint64]map[uint64]bool
	lastPunch   map[uint64]time.Time
	lastPunchIn map[punchKey]time.Time
	// punchPending holds nonces this node sent to a relay for a target,
	// so a delivery can be verified (M4 F-07). Single-use: consumed on
	// the first verified delivery, expired after 2 punch intervals.
	punchPending map[punchKey]pendingNonce
	observed     map[uint64]netip.AddrPort
	lastState    time.Time
	seq          uint64
	challenges   map[[16]byte][]chan challengeReply
	trafficPrev  map[uint64]trafficSample
	lastRTT      map[uint64]time.Duration
	// mtuProbed records peers whose path MTU has been measured for this
	// connection, so a probe runs once per peer, not every tick.
	mtuProbed map[uint64]bool
	onEvent   func(typ string, peerID uint64)
	onMTU     func(peerID uint64, frame int)
	paused    bool
}

type trafficSample struct{ rx, tx uint64 }

// punchKey identifies one punch direction: relay -> target.
type punchKey struct {
	from, target uint64
}

// tombstone is a confirmed departure (M4 P8): the peer has left, its
// routes must not be used, and the fact is re-gossiped until expiry.
type tombstone struct {
	seq     uint64
	expires time.Time
}

type pendingNonce struct {
	nonce [16]byte
	at    time.Time
}

// New creates a controller.
func New(local Local, peers *peer.Table, routes *route.Table, merger *discovery.Merger, eng Transport) (*Controller, error) {
	if local.ID == 0 {
		return nil, errors.New("control: local peer ID must not be zero")
	}
	if peers == nil || routes == nil || merger == nil || eng == nil {
		return nil, errors.New("control: all dependencies are required")
	}
	ctl := &Controller{
		local:        local,
		peers:        peers,
		routes:       routes,
		merger:       merger,
		eng:          eng,
		now:          time.Now,
		lastPing:     make(map[uint64]time.Time),
		firstSeen:    make(map[uint64]time.Time),
		fallbackOn:   make(map[uint64]bool),
		tombstones:   make(map[uint64]tombstone),
		pendingTomb:  make(map[uint64]map[uint64]bool),
		lastPunch:    make(map[uint64]time.Time),
		lastPunchIn:  make(map[punchKey]time.Time),
		punchPending: make(map[punchKey]pendingNonce),
		observed:     make(map[uint64]netip.AddrPort),
		challenges:   make(map[[16]byte][]chan challengeReply),
		trafficPrev:  make(map[uint64]trafficSample),
		lastRTT:      make(map[uint64]time.Duration),
		mtuProbed:    make(map[uint64]bool),
	}
	merger.OnDeparted = ctl.handleDeparted
	return ctl, nil
}

// SetLocalCandidates changes the advertised underlay endpoints without a
// restart, so a discovered public address takes effect promptly.
func (c *Controller) SetLocalCandidates(cands []netip.AddrPort) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.local.Candidates = append([]netip.AddrPort(nil), cands...)
}

// LocalName returns the name currently advertised in STATE.
func (c *Controller) LocalName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.local.Name
}

// SetLocalName changes the name advertised in STATE without a restart.
func (c *Controller) SetLocalName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.local.Name = name
}

// SetLocalOverlay changes the overlay addresses advertised in STATE. The
// daemon uses it when the host turns out to be unable to carry an IPv6
// overlay, so peers stop being told to use an address that cannot answer.
func (c *Controller) SetLocalOverlay(v4, v6 netip.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.local.OverlayV4 = v4
	c.local.OverlayV6 = v6
}

// SetEventHandler reports control transitions (peer_down) to the daemon.
func (c *Controller) SetEventHandler(fn func(typ string, peerID uint64)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onEvent = fn
}

// SetMTUHandler registers the callback that receives a peer's measured path
// frame size (0 when probing failed).
func (c *Controller) SetMTUHandler(fn func(peerID uint64, frame int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMTU = fn
}

// ResetMTUProbes forgets measured paths, so a new dataplane connection
// measures them again.
func (c *Controller) ResetMTUProbes() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mtuProbed = make(map[uint64]bool)
}

// probeMTU measures one peer's path in the background and reports the result.
func (c *Controller) probeMTU(peerID uint64) {
	frame, err := c.eng.ProbeMTU(peerID, wire.MaxFrameSize)
	if err != nil {
		if !c.eng.HasSession(peerID) {
			// The session dropped between the tick and the probe; this says
			// nothing about the path, so let the next tick try again.
			c.mu.Lock()
			delete(c.mtuProbed, peerID)
			c.mu.Unlock()
			return
		}
		frame = 0
	}
	c.mu.Lock()
	fn := c.onMTU
	c.mu.Unlock()
	if fn != nil {
		fn(peerID, frame)
	}
}

func (c *Controller) event(typ string, peerID uint64) {
	c.mu.Lock()
	fn := c.onEvent
	c.mu.Unlock()
	if fn != nil {
		fn(typ, peerID)
	}
}

// SetPaused stops dialing, keepalives, rekeying, and punching while the
// daemon is disconnected. State advertisements and probes may continue.
func (c *Controller) SetPaused(paused bool) {
	c.mu.Lock()
	c.paused = paused
	c.mu.Unlock()
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
	c.mu.Lock()
	paused := c.paused
	c.mu.Unlock()
	if paused {
		return
	}
	for _, p := range c.peers.List() {
		// The path MTU is measured once per direct connection: it decides
		// the largest segment the inner TCP may build.
		c.mu.Lock()
		probed := c.mtuProbed[p.ID]
		if p.State != peer.StateUp || !p.Direct {
			delete(c.mtuProbed, p.ID)
		} else if !probed && c.onMTU != nil {
			c.mtuProbed[p.ID] = true
			go c.probeMTU(p.ID)
		}
		c.mu.Unlock()
		// Configured dialers connect without waiting for overlay traffic.
		if p.Dialer {
			_ = c.eng.EnsureSession(p.ID)
		} else if !c.eng.HasSession(p.ID) {
			// M4 F-11 dialer fallback: a non-dialer peer that has
			// never been contacted at all, for a full silence
			// timeout, is a dead-by-construction pair. Arm fallback
			// dialing once and report it, instead of waiting forever.
			c.mu.Lock()
			first, ok := c.firstSeen[p.ID]
			if !ok {
				first = now
				c.firstSeen[p.ID] = first
			}
			armed := c.fallbackOn[p.ID]
			c.mu.Unlock()
			if !armed && p.LastSeen.IsZero() && now.Sub(first) >= DialerFallbackAfter {
				c.eng.AllowFallbackDial(p.ID)
				_ = c.eng.EnsureSession(p.ID)
				c.mu.Lock()
				c.fallbackOn[p.ID] = true
				c.mu.Unlock()
				log.Printf("skein: dialer fallback for peer %016x: non-dialer never contacted after %s, initiating anyway", p.ID, DialerFallbackAfter)
				c.event("dialer_fallback", p.ID)
			}
		} else {
			c.mu.Lock()
			delete(c.firstSeen, p.ID)
			c.mu.Unlock()
		}
		if p.State == peer.StateUp && !p.LastSeen.IsZero() && now.Sub(p.LastSeen) > PeerTimeout {
			// The path is dead: forget the session and the endpoint, or the
			// dialer would keep encrypting for an unreachable address.
			c.peers.SetState(p.ID, peer.StateDown)
			c.peers.ClearActive(p.ID)
			c.routes.RemoveDirect(p.ID)
			c.eng.DropSession(p.ID)
			c.event("peer_down", p.ID)
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

func (c *Controller) broadcastState(now time.Time) bool {
	payload, err := c.statePayload(now)
	if err != nil {
		// F-02: this error was previously swallowed silently, so a
		// gossip outage looked like "routes never appear". Log it so a
		// STATE build failure is diagnosable from the daemon log.
		log.Printf("skein: state broadcast failed: %v", err)
		return false
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
	return sent
}

// BroadcastNow sends one STATE immediately and reports whether any peer
// received it. The daemon's LeaveSystem uses it so Propagated is true
// only when the leave announcement actually left this node (M4 P8).
func (c *Controller) BroadcastNow() bool {
	return c.broadcastState(c.now())
}

// AnnounceDeparted records this node's own departure as a confirmed
// tombstone to be gossiped (M4 P8). It does not send anything itself;
// the caller broadcasts next and only then stops the dataplane.
func (c *Controller) AnnounceDeparted(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tombstones[id] = tombstone{seq: c.seq, expires: c.now().Add(TombstoneRetention)}
	delete(c.pendingTomb, id)
}

// handleDeparted applies tombstones from a STATE (M4 P8 trust rules):
// a self-announcement (origin == departed, authenticated as that peer by
// the session + the merger's origin check) is confirmed immediately;
// a re-gossiped tombstone needs a second distinct origin before it is
// applied or re-gossiped, so one member cannot kill another peer's
// routes with a forged departure. A confirmed tombstone marks the peer
// down, withdraws every route to it, drops its session, and is carried
// in this node's own STATE until retention expires.
func (c *Controller) handleDeparted(origin, seq uint64, departed []uint64, expires time.Time) {
	_ = expires
	now := c.now()
	c.mu.Lock()
	var confirm []uint64
	for _, id := range departed {
		if id == 0 || id == c.local.ID {
			continue
		}
		if origin == id {
			confirm = append(confirm, id)
			continue
		}
		origins := c.pendingTomb[id]
		if origins == nil {
			origins = make(map[uint64]bool)
			c.pendingTomb[id] = origins
		}
		origins[origin] = true
		if len(origins) >= 2 {
			confirm = append(confirm, id)
		}
	}
	for _, id := range confirm {
		if old, ok := c.tombstones[id]; ok && old.seq >= seq {
			continue
		}
		c.tombstones[id] = tombstone{seq: seq, expires: now.Add(TombstoneRetention)}
		delete(c.pendingTomb, id)
	}
	c.mu.Unlock()
	for _, id := range confirm {
		c.peers.SetState(id, peer.StateDown)
		c.routes.WithdrawDestination(id)
		c.eng.DropSession(id)
		c.event("peer_departed", id)
	}
}

// clearTombstoneIfAlive drops a tombstone when the tombstoned peer itself
// sends a STATE that does not announce its own departure: the peer is
// demonstrably back (rejoin proof), so the tombstone must not outlive it.
func (c *Controller) clearTombstoneIfAlive(from uint64, departedInState []uint64) {
	for _, id := range departedInState {
		if id == from {
			return
		}
	}
	c.mu.Lock()
	delete(c.tombstones, from)
	delete(c.pendingTomb, from)
	c.mu.Unlock()
}

// liveDeparted returns confirmed, unexpired tombstone IDs in sorted order.
func (c *Controller) liveDeparted(now time.Time) []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []uint64
	for id, t := range c.tombstones {
		if now.After(t.expires) {
			delete(c.tombstones, id)
			continue
		}
		out = append(out, id)
	}
	sortIDs(out)
	return out
}

func sortIDs(ids []uint64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
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
	state, err := discovery.BuildWithDeparted(records, routes, activities, c.liveDeparted(now), seq, discovery.TTLFor, now)
	if err != nil {
		return nil, err
	}
	return discovery.Encode(state)
}

// activityRecords produces aggregate direct-link deltas only. It never
// observes packet contents or individual flows.
func (c *Controller) activityRecords(now time.Time) []activity.Record {
	// Copy the peer list before taking c.mu: HandleControl takes the peer
	// lock and then c.mu, so the reverse order here would deadlock.
	peers := c.peers.List()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []activity.Record
	for _, p := range peers {
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
		// Record only direct arrivals as the peer's observed endpoint
		// (M4 F-07 companion): for a relayed frame src is the last
		// hop's address, not the origin's, and recording it would
		// make punch dial the relay instead of the peer. A source
		// that is another peer's candidate/active endpoint is a
		// last hop; anything else is treated as a direct arrival
		// (including a NAT mapping not in the candidate list).
		record := true
		for _, q := range c.peers.List() {
			if q.ID == from {
				continue
			}
			if q.ActiveEndpoint.IsValid() && q.ActiveEndpoint == src {
				record = false
				break
			}
			for _, cand := range q.Candidates {
				if cand.Addr == src {
					record = false
					break
				}
			}
			if !record {
				break
			}
		}
		if record {
			c.mu.Lock()
			c.observed[from] = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
			c.mu.Unlock()
		}
	}
	switch typ {
	case wire.Pong:
		// The peer echoes the ping payload; its first word is the send time.
		if len(payload) == 16 {
			sent := time.Unix(0, int64(binary.BigEndian.Uint64(payload[0:8])))
			if rtt := c.now().Sub(sent); rtt > 0 && rtt < time.Minute {
				c.peers.SetRTT(from, rtt)
				// Route costs drive mesh path selection. Only a proven direct link
				// may contribute its Pong RTT; a relayed Pong is end-to-end and
				// must not be installed as a one-hop link cost.
				if p, ok := c.peers.Get(from); ok && p.Direct {
					_ = c.routes.UpdateDirect(route.DirectLink{PeerID: from, RTT: rtt})
				}
			}
		}
		return
	case wire.State:
		_ = c.merger.Merge(from, payload)
		// Rejoin proof: a live STATE from a peer that does not
		// announce its own departure clears its tombstone.
		if st, err := discovery.Decode(payload); err == nil {
			c.clearTombstoneIfAlive(from, st.Departed)
		}
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
	if !c.eng.HasSession(peerID) {
		// A challenge is encrypted with the session key, so a down peer can
		// only be tested by starting a handshake and waiting for a confirm.
		c.eng.DropSession(peerID)
		if err := c.eng.EnsureSession(peerID); err != nil {
			return fmt.Errorf("control: handshake to %016x: %w", peerID, err)
		}
		deadline := time.NewTimer(ProbeTimeout)
		defer deadline.Stop()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("control: no handshake reply from any candidate; UDP to the peer is likely blocked")
			case <-ticker.C:
				if c.eng.HasSession(peerID) {
					return nil
				}
			}
		}
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
	c.punchPending[punchKey{from: relay, target: peerID}] = pendingNonce{nonce: nonce, at: now}
	c.mu.Unlock()
}

// handlePunch relays an introduction or dials the endpoint it carries.
func (c *Controller) handlePunch(from uint64, src netip.AddrPort, payload []byte) {
	req, err := decodePunch(payload)
	if err != nil {
		return
	}
	if req.HasAddr {
		// Delivery (M4 F-07): verify before dialing anything.
		addr := netip.AddrPortFrom(req.Addr.Addr().Unmap(), req.Addr.Port())
		if addr.Addr().IsLoopback() || addr.Addr().IsLinkLocalUnicast() ||
			addr.Addr() == c.local.OverlayV4 || addr.Addr() == c.local.OverlayV6 {
			// Never emit handshake msg1s to loopback, link-local, or
			// our own overlay address on a member's say-so.
			return
		}
		now := c.now()
		key := punchKey{from: from, target: req.Target}
		c.mu.Lock()
		pending, hasPending := c.punchPending[key]
		if hasPending && now.Sub(pending.at) > 2*PunchInterval {
			delete(c.punchPending, key)
			hasPending = false
		}
		c.mu.Unlock()
		if hasPending && pending.nonce != req.Nonce {
			// Forged or replayed delivery for a request we made to
			// this relay: the nonce is the proof, and it does not
			// match. Do not consume the rate-limit slot either.
			return
		}
		if !hasPending {
			// No outstanding request to this relay for this target:
			// only the peer that is actually the route next hop to
			// the target may introduce it. Any other member's
			// delivery is rejected, however well-formed.
			r, ok := c.routes.Lookup(req.Target)
			if !ok || r.NextHop != from {
				return
			}
		}
		// Rate-limit per (relay, target), not per target alone, so
		// one requester cannot crowd out another's introductions.
		c.mu.Lock()
		last := c.lastPunchIn[key]
		tooSoon := !last.IsZero() && now.Sub(last) < PunchInterval
		if !tooSoon {
			c.lastPunchIn[key] = now
			if hasPending {
				delete(c.punchPending, key)
			}
		}
		c.mu.Unlock()
		if tooSoon {
			return
		}
		// Try the relay's observed endpoint and every candidate this node
		// knows for the peer, so a reachable public address is not missed.
		addrs := []netip.AddrPort{req.Addr}
		if p, ok := c.peers.Get(req.Target); ok {
			for _, cand := range p.Candidates {
				addrs = append(addrs, cand.Addr)
			}
		}
		seen := make(map[netip.AddrPort]bool, len(addrs))
		for _, addr := range addrs {
			addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
			if seen[addr] {
				continue
			}
			seen[addr] = true
			_ = c.eng.Punch(req.Target, addr)
		}
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
