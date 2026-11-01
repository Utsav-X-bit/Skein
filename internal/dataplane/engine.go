// Package dataplane moves overlay IP packets through end-to-end sessions and
// forwards opaque frames for other peers.
package dataplane

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/link"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/session"
	"github.com/Utsav-X-bit/skein/internal/tun"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

// Engine errors.
var (
	ErrNotForUs    = errors.New("dataplane: frame is not addressed to this node")
	ErrNoRoute     = errors.New("dataplane: no route to destination")
	ErrNoSession   = errors.New("dataplane: no established session")
	ErrBadPacket   = errors.New("dataplane: invalid overlay packet")
	ErrTTL         = errors.New("dataplane: ttl exhausted")
	ErrOversize    = errors.New("dataplane: packet exceeds the MTU")
	ErrUnknownPeer = errors.New("dataplane: destination is not a known peer")
)

// handshakeRetry bounds how often a failed handshake is retried.
const (
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Config is the immutable dataplane configuration.
type Config struct {
	LocalID   uint64
	Identity  identity.Identity
	OverlayV4 netip.Addr
	OverlayV6 netip.Addr
	PrefixV4  netip.Prefix
	PrefixV6  netip.Prefix
	MTU       int
}

// Deps are the dataplane collaborators.
type Deps struct {
	TUN      tun.Device
	Link     link.Link
	Peers    *peer.Table
	Routes   *route.Table
	Sessions *session.Manager
}

// Counters are monotonic dataplane statistics.
type Counters struct {
	RxPackets     atomic.Uint64
	TxPackets     atomic.Uint64
	RxBytes       atomic.Uint64
	TxBytes       atomic.Uint64
	Forwarded     atomic.Uint64
	RouteMiss     atomic.Uint64
	TTLExhausted  atomic.Uint64
	Oversize      atomic.Uint64
	InvalidPacket atomic.Uint64
	AuthFailure   atomic.Uint64
	HandshakeFail atomic.Uint64
	ProbeFail     atomic.Uint64
	RekeyFail     atomic.Uint64
}

// Snapshot is a point-in-time copy of the counters.
type Snapshot struct {
	RxPackets     uint64
	TxPackets     uint64
	RxBytes       uint64
	TxBytes       uint64
	Forwarded     uint64
	RouteMiss     uint64
	TTLExhausted  uint64
	Oversize      uint64
	InvalidPacket uint64
	AuthFailure   uint64
	HandshakeFail uint64
	ProbeFail     uint64
	RekeyFail     uint64
}

// Engine owns the TUN and link loops.
type Engine struct {
	cfg      Config
	tun      tun.Device
	link     link.Link
	peers    *peer.Table
	routes   *route.Table
	sessions *session.Manager

	counters Counters

	mu       sync.Mutex
	inflight map[uint64]*inflight
	started  map[uint64]time.Time

	now func() time.Time

	handlerMu sync.RWMutex
	onControl func(from uint64, src netip.AddrPort, typ wire.MessageType, payload []byte)
	onPing    func(from uint64, payload []byte) []byte
	onSession func(peerID uint64)

	closeOnce sync.Once
	closed    chan struct{}
}

type inflight struct {
	attempt  [session.AttemptSize]byte
	sentAt   time.Time
	tries    int
	viaRoute bool
}

// New creates an engine. It does not start any goroutines.
func New(cfg Config, deps Deps) (*Engine, error) {
	if cfg.LocalID == 0 {
		return nil, errors.New("dataplane: local peer ID must not be zero")
	}
	if deps.TUN == nil || deps.Link == nil || deps.Peers == nil || deps.Routes == nil || deps.Sessions == nil {
		return nil, errors.New("dataplane: all dependencies are required")
	}
	if cfg.MTU <= 0 {
		return nil, errors.New("dataplane: mtu must be positive")
	}
	return &Engine{
		cfg:      cfg,
		tun:      deps.TUN,
		link:     deps.Link,
		peers:    deps.Peers,
		routes:   deps.Routes,
		sessions: deps.Sessions,
		inflight: make(map[uint64]*inflight),
		started:  make(map[uint64]time.Time),
		now:      time.Now,
		closed:   make(chan struct{}),
	}, nil
}

// SetClock overrides the clock for tests.
func (e *Engine) SetClock(now func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = now
}

// SetControlHandler registers the encrypted control-frame callback.
func (e *Engine) SetControlHandler(fn func(from uint64, src netip.AddrPort, typ wire.MessageType, payload []byte)) {
	e.handlerMu.Lock()
	defer e.handlerMu.Unlock()
	e.onControl = fn
}

// SetSessionHandler registers a callback invoked when a session is confirmed.
func (e *Engine) SetSessionHandler(fn func(peerID uint64)) {
	e.handlerMu.Lock()
	defer e.handlerMu.Unlock()
	e.onSession = fn
}

// SetPingHandler registers the keepalive callback. A non-nil return is sent
// back as a Pong.
func (e *Engine) SetPingHandler(fn func(from uint64, payload []byte) []byte) {
	e.handlerMu.Lock()
	defer e.handlerMu.Unlock()
	e.onPing = fn
}

// Counters returns a snapshot.
func (e *Engine) Counters() Snapshot {
	c := &e.counters
	return Snapshot{
		RxPackets:     c.RxPackets.Load(),
		TxPackets:     c.TxPackets.Load(),
		RxBytes:       c.RxBytes.Load(),
		TxBytes:       c.TxBytes.Load(),
		Forwarded:     c.Forwarded.Load(),
		RouteMiss:     c.RouteMiss.Load(),
		TTLExhausted:  c.TTLExhausted.Load(),
		Oversize:      c.Oversize.Load(),
		InvalidPacket: c.InvalidPacket.Load(),
		AuthFailure:   c.AuthFailure.Load(),
		HandshakeFail: c.HandshakeFail.Load(),
		ProbeFail:     c.ProbeFail.Load(),
		RekeyFail:     c.RekeyFail.Load(),
	}
}

// Run owns the TUN and link read loops until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	errs := make(chan error, 2)
	go func() { errs <- e.readTUN(ctx) }()
	go func() { errs <- e.readLink(ctx) }()
	select {
	case <-ctx.Done():
		e.Close()
		return nil
	case err := <-errs:
		e.Close()
		return err
	}
}

// Close stops the loops. It is safe to call more than once.
func (e *Engine) Close() error {
	e.closeOnce.Do(func() { close(e.closed) })
	return nil
}

func (e *Engine) readTUN(ctx context.Context) error {
	buf := make([]byte, e.cfg.MTU)
	for {
		n, err := e.tun.Read(buf)
		if err != nil {
			select {
			case <-e.closed:
				return nil
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("dataplane: read tun: %w", err)
			}
		}
		if n <= 0 {
			continue
		}
		if err := e.SendOverlayPacket(buf[:n]); err != nil {
			if debugFrames() {
				log.Printf("skein: outbound packet dropped (%d bytes): %v", n, err)
			}
			continue
		}
	}
}

func (e *Engine) readLink(ctx context.Context) error {
	buf := make([]byte, wire.MaxFrameSize+1)
	for {
		n, src, err := e.link.Recv(buf)
		if err != nil {
			select {
			case <-e.closed:
				return nil
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("dataplane: read link: %w", err)
			}
		}
		if n > wire.MaxFrameSize {
			e.counters.Oversize.Add(1)
			continue
		}
		e.counters.RxPackets.Add(1)
		e.counters.RxBytes.Add(uint64(n))
		if debugFrames() {
			if h, _, perr := wire.Parse(buf[:n]); perr == nil {
				log.Printf("skein: recv type=%d from=%016x to_next=%016x to_final=%016x len=%d via %s",
					h.Type, h.From, h.ToNext, h.ToFinal, n, src)
			}
		}
		if err := e.HandleFrame(src, buf[:n]); err != nil && debugFrames() {
			h, _, perr := wire.Parse(buf[:n])
			if perr != nil {
				log.Printf("skein: drop %d bytes from %s: %v", n, src, err)
			} else {
				log.Printf("skein: drop type=%d from=%016x to_next=%016x to_final=%016x ttl=%d via %s: %v",
					h.Type, h.From, h.ToNext, h.ToFinal, h.TTL, src, err)
			}
		}
	}
}

// SendOverlayPacket validates and encrypts one IP packet from the TUN.
func (e *Engine) SendOverlayPacket(pkt []byte) error {
	dst, err := e.validateOutbound(pkt)
	if err != nil {
		e.counters.InvalidPacket.Add(1)
		return err
	}
	if len(pkt) > e.cfg.MTU {
		e.counters.Oversize.Add(1)
		return ErrOversize
	}
	peerID, ok := e.peers.LookupOverlay(dst)
	if !ok {
		e.counters.RouteMiss.Add(1)
		return ErrUnknownPeer
	}
	if _, ok := e.routes.Lookup(peerID); !ok {
		e.counters.RouteMiss.Add(1)
		if err := e.ensureSession(peerID); err == nil {
			// The handshake is under way; this packet is dropped.
			return ErrNoSession
		}
		return ErrNoRoute
	}
	epoch, ok := e.sessions.ActiveEpoch(peerID)
	if !ok {
		_ = e.ensureSession(peerID)
		return ErrNoSession
	}
	ad := associatedData(wire.Data, peerID, e.cfg.LocalID, epoch)
	_, counter, ct, err := e.sessions.Seal(peerID, ad, pkt)
	if err != nil {
		e.counters.AuthFailure.Add(1)
		return err
	}
	payload := wire.AppendNonce(nil, epoch, counter)
	payload = append(payload, ct...)
	if err := e.sendToPeer(peerID, wire.Data, payload); err != nil {
		e.counters.RouteMiss.Add(1)
		return err
	}
	e.counters.TxPackets.Add(1)
	e.counters.TxBytes.Add(uint64(len(pkt)))
	e.peers.AddTraffic(peerID, 0, uint64(len(pkt)))
	return nil
}

func (e *Engine) validateOutbound(pkt []byte) (netip.Addr, error) {
	if len(pkt) < 20 {
		return netip.Addr{}, ErrBadPacket
	}
	switch version := pkt[0] >> 4; version {
	case 4:
		ihl := int(pkt[0]&0x0f) * 4
		if ihl < 20 || len(pkt) < ihl {
			return netip.Addr{}, ErrBadPacket
		}
		total := int(binary.BigEndian.Uint16(pkt[2:4]))
		if total < ihl || total > len(pkt) {
			return netip.Addr{}, ErrBadPacket
		}
		src := netip.AddrFrom4([4]byte(pkt[12:16]))
		dst := netip.AddrFrom4([4]byte(pkt[16:20]))
		if !e.cfg.OverlayV4.IsValid() || src != e.cfg.OverlayV4 {
			return netip.Addr{}, ErrBadPacket
		}
		if !e.cfg.PrefixV4.Contains(dst) || dst.IsMulticast() || dst.IsUnspecified() || dst == e.cfg.PrefixV4.Addr() {
			return netip.Addr{}, ErrBadPacket
		}
		return dst, nil
	case 6:
		if len(pkt) < 40 {
			return netip.Addr{}, ErrBadPacket
		}
		total := int(binary.BigEndian.Uint16(pkt[4:6]))
		if total < 40 || total > len(pkt) {
			return netip.Addr{}, ErrBadPacket
		}
		src := netip.AddrFrom16([16]byte(pkt[8:24]))
		dst := netip.AddrFrom16([16]byte(pkt[24:40]))
		if !e.cfg.OverlayV6.IsValid() || src != e.cfg.OverlayV6 {
			return netip.Addr{}, ErrBadPacket
		}
		if !e.cfg.PrefixV6.Contains(dst) || dst.IsMulticast() || dst.IsUnspecified() {
			return netip.Addr{}, ErrBadPacket
		}
		return dst, nil
	default:
		return netip.Addr{}, ErrBadPacket
	}
}

// HandleFrame processes one datagram received from src.
func (e *Engine) HandleFrame(src netip.AddrPort, frame []byte) error {
	h, payload, err := wire.Parse(frame)
	if err != nil {
		e.counters.InvalidPacket.Add(1)
		return err
	}
	if h.ToNext != e.cfg.LocalID {
		e.counters.InvalidPacket.Add(1)
		return ErrNotForUs
	}
	if h.ToFinal != e.cfg.LocalID {
		return e.forward(frame, h)
	}
	switch h.Type {
	case wire.Handshake:
		return e.handleHandshake(src, h, payload)
	case wire.Confirm:
		return e.handleConfirm(src, h, payload)
	default:
		return e.handleEncrypted(src, h, payload)
	}
}

func (e *Engine) handleHandshake(src netip.AddrPort, h wire.Header, payload []byte) error {
	e.mu.Lock()
	if _, ok := e.started[h.From]; !ok {
		e.started[h.From] = e.now()
	}
	e.mu.Unlock()

	reply, attempt, err := e.sessions.HandleHandshake(h.From, payload)
	if err != nil {
		e.counters.HandshakeFail.Add(1)
		return err
	}
	if reply != nil {
		if err := e.sendHandshakeReply(h.From, attempt, reply, src); err != nil {
			e.counters.HandshakeFail.Add(1)
			return err
		}
	}
	if p, ok := e.peers.Get(h.From); ok && !p.ActiveEndpoint.IsValid() {
		// The source that completed a handshake is a usable direct endpoint.
		e.peers.AddCandidate(h.From, src, peer.SourceProbe)
	}
	if _, err := e.sessions.Confirmation(h.From, attempt); err == nil {
		// Record the path first so the confirmation can be routed.
		e.established(h.From, src)
		if err := e.sendConfirm(h.From, attempt, src); err != nil {
			e.counters.HandshakeFail.Add(1)
			return err
		}
	}
	return nil
}

func (e *Engine) handleConfirm(src netip.AddrPort, h wire.Header, payload []byte) error {
	epoch, counter, ct, err := wire.ParseNonce(payload)
	if err != nil {
		e.counters.InvalidPacket.Add(1)
		return err
	}
	ad := associatedData(wire.Confirm, h.ToFinal, h.From, epoch)
	if err := e.sessions.HandleConfirm(h.From, epoch, counter, ad, ct); err != nil {
		e.counters.AuthFailure.Add(1)
		return err
	}
	e.established(h.From, src)
	return nil
}

func (e *Engine) handleEncrypted(src netip.AddrPort, h wire.Header, payload []byte) error {
	epoch, counter, ct, err := wire.ParseNonce(payload)
	if err != nil {
		e.counters.InvalidPacket.Add(1)
		return err
	}
	ad := associatedData(h.Type, h.ToFinal, h.From, epoch)
	plaintext, err := e.sessions.Open(h.From, epoch, counter, ad, ct)
	if err != nil {
		e.counters.AuthFailure.Add(1)
		if debugFrames() {
			log.Printf("skein: auth failure type=%d from=%016x epoch=%d counter=%d: %v", h.Type, h.From, epoch, counter, err)
		}
		return err
	}
	if debugFrames() {
		log.Printf("skein: opened type=%d from=%016x epoch=%d counter=%d len=%d", h.Type, h.From, epoch, counter, len(plaintext))
	}
	e.peers.MarkSeen(h.From, e.now())

	switch h.Type {
	case wire.Data:
		return e.deliver(h.From, plaintext)
	case wire.Ping:
		e.handlerMu.RLock()
		fn := e.onPing
		e.handlerMu.RUnlock()
		if fn == nil {
			return nil
		}
		if reply := fn(h.From, plaintext); reply != nil {
			return e.SendControl(h.From, wire.Pong, reply)
		}
		return nil
	case wire.Pong, wire.State, wire.Challenge, wire.ChallengeReply:
		e.handlerMu.RLock()
		fn := e.onControl
		e.handlerMu.RUnlock()
		if fn != nil {
			fn(h.From, src, h.Type, plaintext)
		}
		return nil
	default:
		e.counters.InvalidPacket.Add(1)
		return fmt.Errorf("dataplane: unexpected encrypted type %d", h.Type)
	}
}

// deliver validates the inner packet source and writes it to the TUN.
func (e *Engine) deliver(from uint64, pkt []byte) error {
	p, ok := e.peers.Get(from)
	if !ok {
		e.counters.InvalidPacket.Add(1)
		return ErrUnknownPeer
	}
	var src netip.Addr
	switch version := pkt[0] >> 4; {
	case version == 4 && len(pkt) >= 20:
		src = netip.AddrFrom4([4]byte(pkt[12:16]))
		if !p.OverlayV4.IsValid() || src != p.OverlayV4 {
			e.counters.InvalidPacket.Add(1)
			return ErrBadPacket
		}
	case version == 6 && len(pkt) >= 40:
		src = netip.AddrFrom16([16]byte(pkt[8:24]))
		if !p.OverlayV6.IsValid() || src != p.OverlayV6 {
			e.counters.InvalidPacket.Add(1)
			return ErrBadPacket
		}
	default:
		e.counters.InvalidPacket.Add(1)
		return ErrBadPacket
	}
	if _, err := e.tun.Write(pkt); err != nil {
		if debugFrames() {
			log.Printf("skein: tun write failed (%d bytes from %016x): %v", len(pkt), from, err)
		}
		return err
	}
	if debugFrames() {
		log.Printf("skein: delivered %d-byte packet from %016x", len(pkt), from)
	}
	e.counters.RxPackets.Add(1)
	e.counters.RxBytes.Add(uint64(len(pkt)))
	e.peers.AddTraffic(from, uint64(len(pkt)), 0)
	return nil
}

func (e *Engine) forward(frame []byte, h wire.Header) error {
	if h.TTL <= 1 {
		e.counters.TTLExhausted.Add(1)
		return ErrTTL
	}
	r, ok := e.routes.Lookup(h.ToFinal)
	if !ok {
		e.counters.RouteMiss.Add(1)
		return ErrNoRoute
	}
	ep, ok := e.endpointFor(r.NextHop)
	if !ok {
		e.counters.RouteMiss.Add(1)
		return ErrNoRoute
	}
	if err := wire.SetToNext(frame, r.NextHop); err != nil {
		return err
	}
	if err := wire.SetTTL(frame, h.TTL-1); err != nil {
		return err
	}
	if err := e.link.Send(ep, frame); err != nil {
		if debugFrames() {
			log.Printf("skein: forward to %s failed: %v", ep, err)
		}
		return err
	}
	if debugFrames() {
		log.Printf("skein: forwarded type=%d from=%016x to_final=%016x next=%016x via %s", h.Type, h.From, h.ToFinal, r.NextHop, ep)
	}
	e.counters.Forwarded.Add(1)
	e.counters.TxPackets.Add(1)
	e.counters.TxBytes.Add(uint64(len(frame)))
	return nil
}

// SendControl encrypts and sends a control frame to peerID.
func (e *Engine) SendControl(peerID uint64, typ wire.MessageType, payload []byte) error {
	epoch, ok := e.sessions.ActiveEpoch(peerID)
	if !ok {
		return ErrNoSession
	}
	ad := associatedData(typ, peerID, e.cfg.LocalID, epoch)
	_, counter, ct, err := e.sessions.Seal(peerID, ad, payload)
	if err != nil {
		return err
	}
	body := wire.AppendNonce(nil, epoch, counter)
	body = append(body, ct...)
	return e.sendToPeer(peerID, typ, body)
}

// EnsureSession starts a handshake when one is missing.
func (e *Engine) EnsureSession(peerID uint64) error { return e.ensureSession(peerID) }

// Rekey starts a fresh handshake at the next epoch. Only the lower peer ID
// initiates, and only when the current epoch is due for replacement.
func (e *Engine) Rekey(peerID uint64) error {
	if e.cfg.LocalID > peerID {
		return nil
	}
	if !e.sessions.NeedsRekey(peerID, e.now()) {
		return nil
	}
	p, ok := e.peers.Get(peerID)
	if !ok {
		return ErrUnknownPeer
	}
	attempt, msg, err := e.sessions.Begin(peerID)
	if err != nil {
		e.counters.RekeyFail.Add(1)
		return err
	}
	e.mu.Lock()
	e.inflight[peerID] = &inflight{attempt: attempt, sentAt: e.now()}
	e.mu.Unlock()
	if err := e.sendHandshake(p, attempt, msg); err != nil {
		e.counters.RekeyFail.Add(1)
		return err
	}
	return nil
}

// SendControlTo encrypts a control frame and sends it to a specific address.
// It is used to prove a candidate endpoint over the existing session.
func (e *Engine) SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error {
	epoch, ok := e.sessions.ActiveEpoch(peerID)
	if !ok {
		return ErrNoSession
	}
	ad := associatedData(typ, peerID, e.cfg.LocalID, epoch)
	_, counter, ct, err := e.sessions.Seal(peerID, ad, payload)
	if err != nil {
		return err
	}
	body := wire.AppendNonce(nil, epoch, counter)
	body = append(body, ct...)
	h := wire.Header{
		Type:    typ,
		ToNext:  peerID,
		ToFinal: peerID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	frame, err := wire.Marshal(h, body)
	if err != nil {
		return err
	}
	return e.link.Send(addr, frame)
}

// sendToPeer routes a frame to its final destination.
func (e *Engine) sendToPeer(finalID uint64, typ wire.MessageType, payload []byte) error {
	r, ok := e.routes.Lookup(finalID)
	if !ok {
		return ErrNoRoute
	}
	ep, ok := e.endpointFor(r.NextHop)
	if !ok {
		return ErrNoRoute
	}
	h := wire.Header{
		Type:    typ,
		ToNext:  r.NextHop,
		ToFinal: finalID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	frame, err := wire.Marshal(h, payload)
	if err != nil {
		return err
	}
	return e.link.Send(ep, frame)
}

// sendHandshakeReply returns the responder message along the path it arrived on.
// The next hop must be the node that actually sent us the frame, otherwise a
// relay would drop it: the source address is matched to a known peer first, and
// the route table is only a fallback.
func (e *Engine) sendHandshakeReply(peerID uint64, attempt [session.AttemptSize]byte, payload []byte, src netip.AddrPort) error {
	toNext := uint64(0)
	if id, ok := e.peerForAddr(src); ok {
		toNext = id
	} else if r, ok := e.routes.Lookup(peerID); ok {
		toNext = r.NextHop
	} else {
		// First contact from a peer we do not know yet: answer the source
		// directly. A relayed handshake always matches peerForAddr above.
		toNext = peerID
	}
	h := wire.Header{
		Type:    wire.Handshake,
		ToNext:  toNext,
		ToFinal: peerID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	frame, err := wire.Marshal(h, payload)
	if err != nil {
		return err
	}
	return e.link.Send(src, frame)
}

// peerForAddr maps a datagram source address to a known peer, matching active
// endpoints and candidates. It is how a relayed frame identifies its last hop.
func (e *Engine) peerForAddr(addr netip.AddrPort) (uint64, bool) {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	for _, p := range e.peers.List() {
		if p.ActiveEndpoint.IsValid() && p.ActiveEndpoint == addr {
			return p.ID, true
		}
		for _, c := range p.Candidates {
			if c.Addr == addr {
				return p.ID, true
			}
		}
	}
	return 0, false
}

// sendConfirm proves the new session to the peer and asks it to install it.
// A peer that is not in the table yet (first contact through an invite) is
// answered directly at the address its handshake came from.
func (e *Engine) sendConfirm(peerID uint64, attempt [session.AttemptSize]byte, src netip.AddrPort) error {
	payload, err := e.sessions.Confirmation(peerID, attempt)
	if err != nil {
		return err
	}
	epoch, ok := e.sessions.ReadyEpoch(peerID, attempt)
	if !ok {
		return session.ErrNotConfirmed
	}
	ad := associatedData(wire.Confirm, peerID, e.cfg.LocalID, epoch)
	sealedEpoch, counter, ct, err := e.sessions.SealReady(peerID, attempt, ad, payload)
	if err != nil {
		return err
	}
	body := wire.AppendNonce(nil, sealedEpoch, counter)
	body = append(body, ct...)
	if err := e.sendToPeer(peerID, wire.Confirm, body); err == nil {
		return nil
	}
	h := wire.Header{
		Type:    wire.Confirm,
		ToNext:  peerID,
		ToFinal: peerID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	frame, err := wire.Marshal(h, body)
	if err != nil {
		return err
	}
	return e.link.Send(src, frame)
}

// ensureSession starts a handshake when this node is the configured dialer.
func (e *Engine) ensureSession(peerID uint64) error {
	if e.sessions.HasSession(peerID) {
		return nil
	}
	p, ok := e.peers.Get(peerID)
	if !ok {
		return ErrUnknownPeer
	}
	if !p.Dialer {
		return ErrNoSession
	}

	_, routeExists := e.routes.Lookup(peerID)

	now := e.now()
	e.mu.Lock()
	last := e.inflight[peerID]
	tries := 0
	if last != nil {
		tries = last.tries
		// A newly available route changes the path entirely: retry at once
		// instead of waiting out a backoff earned against unreachable
		// candidates.
		if routeExists && !last.viaRoute {
			tries = 0
		}
		if tries > 0 {
			backoff := minBackoff << min(tries-1, 5)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			if now.Sub(last.sentAt) < backoff {
				e.mu.Unlock()
				return ErrNoSession
			}
		}
	}
	e.mu.Unlock()

	attempt, msg, err := e.sessions.Begin(peerID)
	if err != nil {
		e.counters.HandshakeFail.Add(1)
		return err
	}

	e.mu.Lock()
	e.inflight[peerID] = &inflight{attempt: attempt, sentAt: now, tries: tries + 1, viaRoute: routeExists}
	e.mu.Unlock()

	if err := e.sendHandshake(p, attempt, msg); err != nil {
		e.counters.HandshakeFail.Add(1)
		return err
	}
	return nil
}

// sendHandshake sends message one to the peer, directly or through a route.
func (e *Engine) sendHandshake(p peer.Peer, attempt [session.AttemptSize]byte, payload []byte) error {
	h := wire.Header{
		Type:    wire.Handshake,
		ToNext:  p.ID,
		ToFinal: p.ID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	if r, ok := e.routes.Lookup(p.ID); ok && r.NextHop != p.ID {
		h.ToNext = r.NextHop
		if ep, ok := e.endpointFor(r.NextHop); ok {
			frame, err := wire.Marshal(h, payload)
			if err != nil {
				return err
			}
			return e.link.Send(ep, frame)
		}
	}
	frame, err := wire.Marshal(h, payload)
	if err != nil {
		return err
	}
	if len(p.Candidates) == 0 {
		return ErrNoRoute
	}
	var firstErr error
	for _, c := range p.Candidates {
		if err := e.link.Send(c.Addr, frame); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// established records a proven path. A handshake that arrived through a relay
// only refreshes the session; it must not create a direct link.
func (e *Engine) established(peerID uint64, src netip.AddrPort) {
	now := e.now()

	e.mu.Lock()
	start, ok := e.started[peerID]
	rtt := time.Millisecond
	if ok && now.After(start) {
		rtt = now.Sub(start)
		if rtt < time.Millisecond {
			rtt = time.Millisecond
		}
	}
	delete(e.inflight, peerID)
	delete(e.started, peerID)
	e.mu.Unlock()

	if r, ok := e.routes.Lookup(peerID); ok && r.NextHop != peerID {
		return
	}
	p, ok := e.peers.Get(peerID)
	if ok && e.acceptEndpoint(p, src) {
		e.peers.SetActive(peerID, src, rtt, now)
		e.peers.AddCandidate(peerID, src, peer.SourceProbe)
	}
	_ = e.routes.UpdateDirect(route.DirectLink{PeerID: peerID, RTT: rtt})

	e.handlerMu.RLock()
	fn := e.onSession
	e.handlerMu.RUnlock()
	if fn != nil {
		fn(peerID)
	}
}

// acceptEndpoint decides whether a handshake source may become the active path.
func (e *Engine) acceptEndpoint(p peer.Peer, src netip.AddrPort) bool {
	if !p.ActiveEndpoint.IsValid() {
		return true
	}
	for _, c := range p.Candidates {
		if c.Addr == src {
			return true
		}
	}
	return false
}

func (e *Engine) endpointFor(peerID uint64) (netip.AddrPort, bool) {
	p, ok := e.peers.Get(peerID)
	if !ok || !p.ActiveEndpoint.IsValid() {
		return netip.AddrPort{}, false
	}
	return p.ActiveEndpoint, true
}

// debugFrames reports whether per-frame diagnostics are enabled.
func debugFrames() bool { return os.Getenv("SKEIN_DEBUG") != "" }

// associatedData binds the authenticated frame fields. Relay-mutable to_next
// and ttl are deliberately excluded.
func associatedData(typ wire.MessageType, toFinal, from uint64, epoch uint32) []byte {
	ad := make([]byte, 0, 21)
	ad = append(ad, byte(typ))
	ad = binary.BigEndian.AppendUint64(ad, toFinal)
	ad = binary.BigEndian.AppendUint64(ad, from)
	ad = binary.BigEndian.AppendUint32(ad, epoch)
	return ad
}
