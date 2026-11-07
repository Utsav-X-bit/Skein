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
	"strings"
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
	// ErrExpired is returned when the active epoch is past its lifetime
	// (M4 F-04): DATA pauses rather than reusing an expired key. The
	// caller drops the packet; rekey is triggered (lower-ID side) or
	// awaited (higher-ID side, whose control loop rekeys via Tick),
	// and traffic resumes once the new epoch installs.
	ErrExpired = errors.New("dataplane: session epoch expired, rekey in progress")
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
	// fallbackDialers are non-dialer peers the control loop has armed
	// for fallback dialing after a full silence timeout (M4 F-11).
	fallbackDialers map[uint64]bool

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
		cfg:             cfg,
		tun:             deps.TUN,
		link:            deps.Link,
		peers:           deps.Peers,
		routes:          deps.Routes,
		sessions:        deps.Sessions,
		inflight:        make(map[uint64]*inflight),
		started:         make(map[uint64]time.Time),
		fallbackDialers: make(map[uint64]bool),
		now:             time.Now,
		closed:          make(chan struct{}),
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
			time.Sleep(time.Millisecond)
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
		var transport string
		var n int
		var src netip.AddrPort
		var err error
		if tagged, ok := e.link.(interface {
			RecvTagged([]byte) (int, netip.AddrPort, string, error)
		}); ok {
			n, src, transport, err = tagged.RecvTagged(buf)
		} else {
			n, src, err = e.link.Recv(buf)
		}
		if err != nil {
			if errors.Is(err, link.ErrTruncated) {
				e.counters.Oversize.Add(1)
				continue
			}
			if transientReadError(err) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
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
		if debugFrames() {
			if h, _, perr := wire.Parse(buf[:n]); perr == nil {
				log.Printf("skein: recv type=%d from=%016x to_next=%016x to_final=%016x len=%d via %s",
					h.Type, h.From, h.ToNext, h.ToFinal, n, src)
			}
		}
		if err := e.handleFrame(src, buf[:n], transport); err != nil && debugFrames() {
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
	if err := e.checkExpired(peerID); err != nil {
		// Fail closed (M4 F-04): never seal DATA with an expired epoch.
		// checkExpired triggers Rekey on the lower-ID side; the
		// higher-ID side's Rekey is a no-op and its control loop
		// drives the replacement from the other end.
		return err
	}
	adArr := associatedData(wire.Data, peerID, e.cfg.LocalID, epoch)
	ad := adArr[:]
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
		payloadLen := int(binary.BigEndian.Uint16(pkt[4:6]))
		if payloadLen == 0 || 40+payloadLen > len(pkt) {
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
	return e.handleFrame(src, frame, "")
}

// handleFrame processes one datagram and records the transport it arrived on.
func (e *Engine) handleFrame(src netip.AddrPort, frame []byte, transport string) error {
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
		return e.forward(frame, h, src)
	}
	switch h.Type {
	case wire.Handshake:
		return e.handleHandshake(src, h, payload)
	case wire.Confirm:
		return e.handleConfirm(src, h, payload)
	default:
		return e.handleEncrypted(src, h, payload, transport)
	}
}

func (e *Engine) handleHandshake(src netip.AddrPort, h wire.Header, payload []byte) error {
	if e.peers.Exists(h.From) {
		e.mu.Lock()
		e.started[h.From] = e.now()
		e.mu.Unlock()
	}

	reply, attempt, err := e.sessions.HandleHandshake(h.From, payload)
	if err != nil {
		e.counters.HandshakeFail.Add(1)
		return err
	}
	// Seal our confirmation before sending the reply: a synchronous peer can
	// answer the reply with its own confirmation, which installs and retires
	// this handshake before we get a chance to prepare ours.
	var confirm []byte
	if _, err := e.sessions.Confirmation(h.From, attempt); err == nil {
		confirm, err = e.confirmPayload(h.From, attempt)
		if err != nil {
			e.counters.HandshakeFail.Add(1)
			return err
		}
	}
	if reply != nil {
		if err := e.sendHandshakeReply(h.From, attempt, reply, src); err != nil {
			e.counters.HandshakeFail.Add(1)
			return err
		}
	}
	if p, ok := e.peers.Get(h.From); ok && !p.ActiveEndpoint.IsValid() && e.directSource(h.From, src) {
		// The source that completed a handshake is a usable direct endpoint.
		// A relayed handshake arrives from the relay, which is not a candidate
		// for the target peer.
		e.peers.AddCandidate(h.From, src, peer.SourceProbe)
	}
	if confirm != nil {
		if err := e.sendConfirmBody(h.From, confirm, src); err != nil {
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
	adArr := associatedData(wire.Confirm, h.ToFinal, h.From, epoch)
	ad := adArr[:]
	installed, err := e.sessions.HandleConfirmInstalled(h.From, epoch, counter, ad, ct)
	if err != nil {
		e.counters.AuthFailure.Add(1)
		return err
	}
	if !installed {
		// A valid confirmation of a superseded session: no peer_up, no
		// active-endpoint move, no counter increment — the winning
		// session (if any) already reported establishment.
		return nil
	}
	e.established(h.From, src)
	return nil
}

func (e *Engine) handleEncrypted(src netip.AddrPort, h wire.Header, payload []byte, transport string) error {
	epoch, counter, ct, err := wire.ParseNonce(payload)
	if err != nil {
		e.counters.InvalidPacket.Add(1)
		return err
	}
	adArr := associatedData(h.Type, h.ToFinal, h.From, epoch)
	ad := adArr[:]
	plaintext, err := e.sessions.Open(h.From, epoch, counter, ad, ct)
	if err != nil {
		e.counters.AuthFailure.Add(1)
		if debugFrames() {
			log.Printf("skein: auth failure type=%d from=%016x epoch=%d counter=%d: %v", h.Type, h.From, epoch, counter, err)
		}
		if errors.Is(err, session.ErrStaleEpoch) {
			// A known peer is speaking on an epoch this node no
			// longer holds (it restarted and climbed past our
			// session). Drop the frame, but start a fresh handshake
			// so the pair re-converges instead of dropping its
			// traffic until the old epoch expires (bug #14).
			e.recoverStaleEpoch(h.From, epoch)
		}
		return err
	}
	if debugFrames() {
		log.Printf("skein: opened type=%d from=%016x epoch=%d counter=%d len=%d", h.Type, h.From, epoch, counter, len(plaintext))
	}
	e.peers.MarkSeen(h.From, e.now())
	if transport != "" {
		e.peers.SetRxTransport(h.From, transport)
	}

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
	case wire.Pong, wire.State, wire.Challenge, wire.ChallengeReply, wire.Punch:
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
	if len(pkt) < 20 {
		e.counters.InvalidPacket.Add(1)
		return ErrBadPacket
	}
	peerV4, peerV6, ok := e.peers.Overlay(from)
	if !ok {
		e.counters.InvalidPacket.Add(1)
		return ErrUnknownPeer
	}
	switch version := pkt[0] >> 4; {
	case version == 4:
		total := int(binary.BigEndian.Uint16(pkt[2:4]))
		src := netip.AddrFrom4([4]byte(pkt[12:16]))
		dst := netip.AddrFrom4([4]byte(pkt[16:20]))
		if total < 20 || total > len(pkt) || !peerV4.IsValid() || src != peerV4 ||
			!e.cfg.OverlayV4.IsValid() || dst != e.cfg.OverlayV4 {
			e.counters.InvalidPacket.Add(1)
			return ErrBadPacket
		}
		pkt = pkt[:total]
	case version == 6 && len(pkt) >= 40:
		payloadLen := int(binary.BigEndian.Uint16(pkt[4:6]))
		total := 40 + payloadLen
		src := netip.AddrFrom16([16]byte(pkt[8:24]))
		dst := netip.AddrFrom16([16]byte(pkt[24:40]))
		if payloadLen == 0 || total > len(pkt) || !peerV6.IsValid() || src != peerV6 ||
			!e.cfg.OverlayV6.IsValid() || dst != e.cfg.OverlayV6 {
			e.counters.InvalidPacket.Add(1)
			return ErrBadPacket
		}
		pkt = pkt[:total]
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

func (e *Engine) forward(frame []byte, h wire.Header, src netip.AddrPort) error {
	if h.TTL <= 1 {
		e.counters.TTLExhausted.Add(1)
		return ErrTTL
	}
	if _, ok := e.peerForAddr(src); !ok {
		// Only a known neighbour may use this node as a relay; otherwise the
		// daemon is an unauthenticated open relay and amplifier.
		e.counters.InvalidPacket.Add(1)
		return ErrNotForUs
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

// checkExpired fails closed on an expired epoch and triggers a rekey,
// shared by DATA and control sends (M4 F-04).
func (e *Engine) checkExpired(peerID uint64) error {
	if e.sessions.NeedsRekey(peerID, e.now()) {
		_ = e.Rekey(peerID)
		return ErrExpired
	}
	return nil
}

// SendControl encrypts and sends a control frame to peerID.
func (e *Engine) SendControl(peerID uint64, typ wire.MessageType, payload []byte) error {
	epoch, ok := e.sessions.ActiveEpoch(peerID)
	if !ok {
		return ErrNoSession
	}
	if err := e.checkExpired(peerID); err != nil {
		return err
	}
	adArr := associatedData(typ, peerID, e.cfg.LocalID, epoch)
	ad := adArr[:]
	_, counter, ct, err := e.sessions.Seal(peerID, ad, payload)
	if err != nil {
		return err
	}
	body := wire.AppendNonce(nil, epoch, counter)
	body = append(body, ct...)
	return e.sendToPeer(peerID, typ, body)
}

// Punch starts a handshake to a specific address for peerID. A relay uses it
// to introduce two peers that cannot reach each other directly.
func (e *Engine) Punch(peerID uint64, addr netip.AddrPort) error {
	if _, ok := e.peers.Get(peerID); !ok {
		return ErrUnknownPeer
	}
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if !addr.IsValid() || addr.Port() == 0 {
		return ErrNoRoute
	}
	e.markStarted(peerID)
	attempt, msg, err := e.sessions.Begin(peerID)
	if err != nil {
		e.counters.ProbeFail.Add(1)
		return err
	}
	e.mu.Lock()
	e.inflight[peerID] = &inflight{attempt: attempt, sentAt: e.now(), tries: 1}
	e.mu.Unlock()

	h := wire.Header{
		Type:    wire.Handshake,
		ToNext:  peerID,
		ToFinal: peerID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	frame, err := wire.Marshal(h, msg)
	if err != nil {
		return err
	}
	return e.link.Send(addr, frame)
}

// DropSession forgets the session and any handshake state for a peer so the
// next attempt starts from scratch.
func (e *Engine) DropSession(peerID uint64) {
	e.sessions.ResetPeer(peerID)
	e.mu.Lock()
	delete(e.inflight, peerID)
	delete(e.started, peerID)
	e.mu.Unlock()
}

// HasSession reports whether an established session exists for peerID.
func (e *Engine) HasSession(peerID uint64) bool { return e.sessions.HasSession(peerID) }

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
	now := e.now()
	e.mu.Lock()
	last := e.inflight[peerID]
	tries := 0
	if last != nil {
		tries = last.tries
		backoff := minBackoff << min(tries, 5)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		if tries > 0 && now.Sub(last.sentAt) < backoff {
			e.mu.Unlock()
			return nil
		}
	}
	e.mu.Unlock()
	e.markStarted(peerID)
	attempt, msg, err := e.sessions.Begin(peerID)
	if err != nil {
		e.counters.RekeyFail.Add(1)
		return err
	}
	e.mu.Lock()
	e.inflight[peerID] = &inflight{attempt: attempt, sentAt: now, tries: tries + 1}
	e.mu.Unlock()
	if err := e.sendHandshake(p, attempt, msg); err != nil {
		e.counters.RekeyFail.Add(1)
		return err
	}
	return nil
}

// recoverStaleEpoch starts a fresh handshake after a known peer's frames
// arrive on an epoch this node no longer holds (bug #14: restart
// recovery). A restarted peer climbs to a new epoch while this node still
// holds the old session; without a new handshake both sides keep their
// own session and every frame is dropped as a stale epoch until the old
// one expires (up to an hour). Either side may initiate the recovery
// handshake — the attempt tie-break (smaller attempt wins) and Begin's
// epoch climbing converge the pair — so this deliberately does NOT apply
// Rekey's lower-ID-only or expiry gates. The handshake begins ABOVE the
// stale epoch: at the same epoch the attempt tie-break can leave the two
// sides on different keys. Attempts are throttled by the same inflight
// backoff as Rekey/ensureSession, so a stream of stale frames cannot
// handshake-storm the peer, and the handshake goes to the peer's known
// endpoints (never to the frame's source address), so a spoofed peer ID
// cannot redirect it.
func (e *Engine) recoverStaleEpoch(peerID uint64, staleEpoch uint32) {
	p, ok := e.peers.Get(peerID)
	if !ok {
		return
	}
	now := e.now()
	e.mu.Lock()
	last := e.inflight[peerID]
	tries := 0
	if last != nil {
		tries = last.tries
		backoff := minBackoff << min(tries, 5)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		if tries > 0 && now.Sub(last.sentAt) < backoff {
			e.mu.Unlock()
			return
		}
	}
	e.mu.Unlock()
	e.markStarted(peerID)
	attempt, msg, err := e.sessions.BeginAbove(peerID, staleEpoch)
	if err != nil {
		e.counters.RekeyFail.Add(1)
		return
	}
	e.mu.Lock()
	e.inflight[peerID] = &inflight{attempt: attempt, sentAt: now, tries: tries + 1}
	e.mu.Unlock()
	if err := e.sendHandshake(p, attempt, msg); err != nil {
		e.counters.RekeyFail.Add(1)
		return
	}
}

// SendControlTo encrypts a control frame and sends it to a specific address.
// It is used to prove a candidate endpoint over the existing session.
func (e *Engine) SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error {
	epoch, ok := e.sessions.ActiveEpoch(peerID)
	if !ok {
		return ErrNoSession
	}
	if err := e.checkExpired(peerID); err != nil {
		return err
	}
	adArr := associatedData(typ, peerID, e.cfg.LocalID, epoch)
	ad := adArr[:]
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
	toNext := peerID
	if id, ok := e.peerForAddr(src); ok {
		toNext = id
	} else if r, ok := e.routes.Lookup(peerID); ok {
		if ep, ok := e.endpointFor(r.NextHop); ok && ep == src {
			toNext = r.NextHop
		}
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

// directSource reports whether src can be a direct endpoint for peerID: it
// either belongs to that peer or to no known peer (first contact).
func (e *Engine) directSource(peerID uint64, src netip.AddrPort) bool {
	id, ok := e.peerForAddr(src)
	return !ok || id == peerID
}

// peerForAddr maps a datagram source address to a known peer, matching active
// endpoints and candidates. It is how a relayed frame identifies its last hop.
func (e *Engine) peerForAddr(addr netip.AddrPort) (uint64, bool) {
	return e.peers.ByAddr(addr)
}

// confirmPayload builds the encrypted confirmation for a completed handshake.
func (e *Engine) confirmPayload(peerID uint64, attempt [session.AttemptSize]byte) ([]byte, error) {
	payload, err := e.sessions.Confirmation(peerID, attempt)
	if err != nil {
		return nil, err
	}
	epoch, ok := e.sessions.ReadyEpoch(peerID, attempt)
	if !ok {
		return nil, session.ErrNotConfirmed
	}
	adArr := associatedData(wire.Confirm, peerID, e.cfg.LocalID, epoch)
	ad := adArr[:]
	sealedEpoch, counter, ct, err := e.sessions.SealReady(peerID, attempt, ad, payload)
	if err != nil {
		return nil, err
	}
	body := wire.AppendNonce(nil, sealedEpoch, counter)
	return append(body, ct...), nil
}

// sendConfirmBody routes a prepared confirmation. A peer that is not in the
// table yet (first contact through an invite) is answered directly at the
// address its handshake came from.
func (e *Engine) sendConfirmBody(peerID uint64, body []byte, src netip.AddrPort) error {
	toNext := peerID
	if id, ok := e.peerForAddr(src); ok {
		toNext = id
	}
	h := wire.Header{
		Type:    wire.Confirm,
		ToNext:  toNext,
		ToFinal: peerID,
		From:    e.cfg.LocalID,
		TTL:     wire.MaxTTL,
	}
	frame, err := wire.Marshal(h, body)
	if err != nil {
		return err
	}
	if err := e.link.Send(src, frame); err == nil {
		return nil
	}
	return e.sendToPeer(peerID, wire.Confirm, body)
}

// AllowFallbackDial arms fallback dialing for a non-dialer peer (M4
// F-11); it is a no-op for unknown peers. The control loop calls it
// only after a full silence timeout with no inbound contact.
func (e *Engine) AllowFallbackDial(peerID uint64) {
	if _, ok := e.peers.Get(peerID); !ok {
		return
	}
	e.mu.Lock()
	e.fallbackDialers[peerID] = true
	e.mu.Unlock()
}

// ensureSession starts a handshake when this node is the configured dialer,
// or when fallback dialing has been armed for a non-dialer peer.
func (e *Engine) ensureSession(peerID uint64) error {
	if e.sessions.HasSession(peerID) {
		return nil
	}
	p, ok := e.peers.Get(peerID)
	if !ok {
		return ErrUnknownPeer
	}
	if !p.Dialer {
		e.mu.Lock()
		armed := e.fallbackDialers[peerID]
		e.mu.Unlock()
		if !armed {
			return ErrNoSession
		}
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

	// After a couple of silent UDP attempts, open TCP fallbacks so the next
	// handshake can use a transport the network does not block.
	if tries >= 2 {
		if dialer, ok := e.link.(interface{ DialTCP(netip.AddrPort) error }); ok {
			for _, cand := range p.Candidates {
				go func(addr netip.AddrPort) { _ = dialer.DialTCP(addr) }(cand.Addr)
			}
		}
	}

	e.markStarted(peerID)
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
	// Race the route and the direct candidates (M4 F-08): a stale relay
	// route previously starved direct dialing entirely, because the
	// route path returned before any candidate was tried. Now both are
	// attempted; the handshake completes on whichever path works, and
	// only a failure on every path is an error.
	var firstErr error
	sent := false
	if r, ok := e.routes.Lookup(p.ID); ok && r.NextHop != p.ID {
		h.ToNext = r.NextHop
		if ep, ok := e.endpointFor(r.NextHop); ok {
			frame, err := wire.Marshal(h, payload)
			if err != nil {
				return err
			}
			if err := e.link.Send(ep, frame); err != nil {
				firstErr = err
			} else {
				sent = true
			}
		}
	}
	h.ToNext = p.ID
	frame, err := wire.Marshal(h, payload)
	if err != nil {
		return err
	}
	if len(p.Candidates) == 0 {
		if sent {
			return nil
		}
		if firstErr != nil {
			return firstErr
		}
		return ErrNoRoute
	}
	for _, c := range p.Candidates {
		if err := e.link.Send(c.Addr, frame); err != nil && firstErr == nil {
			firstErr = err
		} else if err == nil {
			sent = true
		}
	}
	if sent {
		return nil
	}
	return firstErr
}

// established records a proven path. A handshake that arrived through a relay
// only refreshes the session; it must not create a direct link.
// markStarted records when an outbound handshake began so the confirmed RTT
// covers the whole exchange, not just the inbound half.
func (e *Engine) markStarted(peerID uint64) {
	now := e.now()
	e.mu.Lock()
	e.started[peerID] = now
	if len(e.started) > 1024 {
		for id, at := range e.started {
			if now.Sub(at) > time.Minute {
				delete(e.started, id)
			}
		}
	}
	e.mu.Unlock()
}

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

	// A relayed handshake arrives from the relay's address; a direct one
	// arrives from the peer itself (or an unknown address on first contact).
	relayed := !e.directSource(peerID, src)
	if !relayed {
		transport := "udp"
		if reporter, ok := e.link.(interface{ TransportFor(netip.AddrPort) string }); ok {
			transport = reporter.TransportFor(src)
		}
		if _, ok := e.peers.Get(peerID); ok {
			// The confirmation proved the PSK, so the source is authentic.
			// MarkSession runs before SetActive so a fresh connection records
			// its session start while the peer is still down.
			e.peers.MarkSession(peerID, now)
			e.peers.SetActive(peerID, src, rtt, transport, now)
			e.peers.AddCandidate(peerID, src, peer.SourceProbe)
			_ = e.routes.UpdateDirect(route.DirectLink{PeerID: peerID, RTT: rtt})
		}
	}

	e.handlerMu.RLock()
	fn := e.onSession
	e.handlerMu.RUnlock()
	if fn != nil {
		fn(peerID)
	}
}

func (e *Engine) endpointFor(peerID uint64) (netip.AddrPort, bool) {
	p, ok := e.peers.Get(peerID)
	if !ok || !p.ActiveEndpoint.IsValid() {
		return netip.AddrPort{}, false
	}
	return p.ActiveEndpoint, true
}

// debugFrames reports whether per-frame diagnostics are enabled.
var (
	debugOnce    sync.Once
	debugEnabled bool
)

// transientReadError reports a kernel resource error that a retry can fix.
func transientReadError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no buffer space") ||
		strings.Contains(msg, "cannot allocate memory") ||
		strings.Contains(msg, "temporarily unavailable")
}

func debugFrames() bool {
	debugOnce.Do(func() { debugEnabled = os.Getenv("SKEIN_DEBUG") != "" })
	return debugEnabled
}

// associatedData binds the authenticated frame fields. Relay-mutable to_next
// and ttl are deliberately excluded.
func associatedData(typ wire.MessageType, toFinal, from uint64, epoch uint32) [21]byte {
	var ad [21]byte
	ad[0] = byte(typ)
	binary.BigEndian.PutUint64(ad[1:9], toFinal)
	binary.BigEndian.PutUint64(ad[9:17], from)
	binary.BigEndian.PutUint32(ad[17:21], epoch)
	return ad
}
