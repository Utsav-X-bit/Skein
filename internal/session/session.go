// Package session implements the Noise IKpsk2 end-to-end session used between
// overlay peers: handshake attempts, key epochs, explicit nonces, replay
// windows, confirmation, and rekeying.
//
// Handshake payloads are [attempt:16][epoch:4][noise message]. The attempt and
// epoch are bound into the Noise prologue along with both peer IDs, and a
// session is installed only after an encrypted confirmation proves the peer
// derived the same keys.
package session

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/flynn/noise"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

const (
	// Protocol is the Noise prologue prefix.
	Protocol = "skein/1"
	// AttemptSize is the size of a handshake attempt identifier.
	AttemptSize = 16
	// HandshakePrefixSize is [attempt:16][epoch:4].
	HandshakePrefixSize = AttemptSize + 4
	// RekeyPackets is the per-epoch packet budget before a fresh handshake.
	RekeyPackets = 1 << 32
	// RekeyInterval is the maximum lifetime of an epoch.
	RekeyInterval = time.Hour
	// ReplayWindow is the reordering window per epoch.
	ReplayWindow = 64
)

// Errors returned by the session layer.
var (
	ErrNoSession     = errors.New("session: no established session for peer")
	ErrStaleEpoch    = errors.New("session: stale or unknown key epoch")
	ErrReplay        = errors.New("session: replayed or out-of-window counter")
	ErrExhausted     = errors.New("session: nonce counter exhausted")
	ErrNotConfirmed  = errors.New("session: confirmation does not match a completed handshake")
	ErrPeerKey       = errors.New("session: peer static key does not match the configured key")
	ErrBadHandshake  = errors.New("session: malformed handshake message")
	ErrHandshakeDone = errors.New("session: handshake is already complete")
)

var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// Session is one key epoch of an established end-to-end session.
type Session struct {
	Epoch uint32

	mu      sync.Mutex
	send    *noise.CipherState
	sendSeq uint64
	recv    *noise.CipherState
	recvWin replayWindow
	created time.Time
}

func newSession(epoch uint32, send, recv *noise.CipherState) *Session {
	return &Session{Epoch: epoch, send: send, recv: recv, created: time.Now()}
}

// Seal encrypts plaintext with the epoch's send key and returns the explicit
// nonce fields and ciphertext (which includes the AEAD tag).
func (s *Session) Seal(ad, plaintext []byte) (epoch uint32, counter uint64, ciphertext []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.send == nil {
		return 0, 0, nil, ErrNoSession
	}
	if s.sendSeq == math.MaxUint64 {
		return 0, 0, nil, ErrExhausted
	}
	counter = s.sendSeq
	s.send.SetNonce(counter)
	out, err := s.send.Encrypt(nil, ad, plaintext)
	if err != nil {
		return 0, 0, nil, err
	}
	s.sendSeq++
	return s.Epoch, counter, out, nil
}

// Open authenticates and decrypts ciphertext for the given counter. The replay
// window is updated only after successful authentication.
func (s *Session) Open(counter uint64, ad, ciphertext []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recv == nil {
		return nil, ErrNoSession
	}
	if !s.recvWin.acceptable(counter) {
		return nil, ErrReplay
	}
	s.recv.SetNonce(counter)
	out, err := s.recv.Decrypt(nil, ad, ciphertext)
	if err != nil {
		return nil, err
	}
	s.recvWin.accept(counter)
	return out, nil
}

// PacketsSent reports how many packets this epoch has encrypted.
func (s *Session) PacketsSent() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendSeq
}

// Expired reports whether the epoch should be replaced.
func (s *Session) Expired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendSeq >= RekeyPackets || now.Sub(s.created) >= RekeyInterval
}

// replayWindow is a 64-packet sliding bitmap where bit 0 is the highest
// counter seen.
type replayWindow struct {
	seen    bool
	highest uint64
	bits    uint64
}

func (w *replayWindow) acceptable(counter uint64) bool {
	if !w.seen {
		return true
	}
	if counter > w.highest {
		return true
	}
	delta := w.highest - counter
	if delta >= ReplayWindow {
		return false
	}
	return w.bits&(1<<delta) == 0
}

func (w *replayWindow) accept(counter uint64) {
	if !w.seen {
		w.seen, w.highest, w.bits = true, counter, 1
		return
	}
	if counter > w.highest {
		delta := counter - w.highest
		if delta >= ReplayWindow {
			w.bits = 1
		} else {
			w.bits = w.bits<<delta | 1
		}
		w.highest = counter
		return
	}
	w.bits |= 1 << (w.highest - counter)
}

// Handshake is one in-flight Noise IKpsk2 handshake.
type Handshake struct {
	hs        *noise.HandshakeState
	attempt   [AttemptSize]byte
	epoch     uint32
	initiator bool
	remote    []byte
	started   time.Time
	done      bool
	doneAt    time.Time
	session   *Session
}

// NewHandshake starts an initiator or responder handshake bound to attempt and
// epoch. A responder may pass a nil remote key; the key is then learned from
// the initiator's message and must match the ID bound into the prologue.
func NewHandshake(local identity.Identity, remotePublic, psk []byte, initiator bool, attempt [AttemptSize]byte, epoch uint32) (*Handshake, error) {
	if initiator && len(remotePublic) != identity.KeySize {
		return nil, fmt.Errorf("remote public key must be %d bytes", identity.KeySize)
	}
	if !initiator && len(remotePublic) != 0 && len(remotePublic) != identity.KeySize {
		return nil, fmt.Errorf("remote public key must be %d bytes", identity.KeySize)
	}
	var remoteID uint64
	if len(remotePublic) == identity.KeySize {
		var err error
		if remoteID, err = identity.PeerID(remotePublic); err != nil {
			return nil, err
		}
	}
	return newHandshake(local, remotePublic, remoteID, psk, initiator, attempt, epoch)
}

// NewResponderHandshake builds a responder handshake for a peer whose key is
// not yet known. The claimed ID is bound into the prologue and verified against
// the peer's static key once message one is read.
func NewResponderHandshake(local identity.Identity, claimedID uint64, psk []byte, attempt [AttemptSize]byte, epoch uint32) (*Handshake, error) {
	if claimedID == 0 {
		return nil, errors.New("claimed peer ID must not be zero")
	}
	return newHandshake(local, nil, claimedID, psk, false, attempt, epoch)
}

func newHandshake(local identity.Identity, remotePublic []byte, remoteID uint64, psk []byte, initiator bool, attempt [AttemptSize]byte, epoch uint32) (*Handshake, error) {
	if len(psk) != identity.KeySize {
		return nil, fmt.Errorf("network secret must be %d bytes", identity.KeySize)
	}
	localID, err := identity.PeerID(local.PublicKey)
	if err != nil {
		return nil, err
	}
	if remoteID == 0 {
		return nil, errors.New("remote peer ID must not be zero")
	}
	initiatorID, responderID := localID, remoteID
	if !initiator {
		initiatorID, responderID = remoteID, localID
	}

	cfg := noise.Config{
		CipherSuite:           cipherSuite,
		Pattern:               noise.HandshakeIK,
		Initiator:             initiator,
		Prologue:              prologue(attempt, epoch, initiatorID, responderID),
		StaticKeypair:         noise.DHKey{Private: local.PrivateKey, Public: local.PublicKey},
		PresharedKey:          psk,
		PresharedKeyPlacement: 2,
	}
	if initiator {
		cfg.PeerStatic = remotePublic
	}
	hs, err := noise.NewHandshakeState(cfg)
	if err != nil {
		return nil, fmt.Errorf("noise handshake: %w", err)
	}
	return &Handshake{hs: hs, attempt: attempt, epoch: epoch, initiator: initiator, remote: remotePublic, started: time.Now()}, nil
}

func prologue(attempt [AttemptSize]byte, epoch uint32, initiatorID, responderID uint64) []byte {
	out := make([]byte, 0, len(Protocol)+AttemptSize+4+16)
	out = append(out, Protocol...)
	out = append(out, attempt[:]...)
	out = binary.BigEndian.AppendUint32(out, epoch)
	out = binary.BigEndian.AppendUint64(out, initiatorID)
	out = binary.BigEndian.AppendUint64(out, responderID)
	return out
}

// Attempt returns the handshake attempt ID.
func (h *Handshake) Attempt() [AttemptSize]byte { return h.attempt }

// Epoch returns the key epoch this handshake derives.
func (h *Handshake) Epoch() uint32 { return h.epoch }

// Write produces the next handshake payload: [attempt][epoch][noise message].
func (h *Handshake) Write() ([]byte, error) {
	if h.done {
		return nil, ErrHandshakeDone
	}
	out, cs1, cs2, err := h.hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if cs1 != nil {
		if err := h.complete(cs1, cs2); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, 0, HandshakePrefixSize+len(out))
	payload = append(payload, h.attempt[:]...)
	payload = binary.BigEndian.AppendUint32(payload, h.epoch)
	return append(payload, out...), nil
}

// Read consumes a handshake payload and reports whether the handshake completed.
func (h *Handshake) Read(payload []byte) (bool, error) {
	if len(payload) < HandshakePrefixSize {
		return false, ErrBadHandshake
	}
	if !bytes.Equal(payload[:AttemptSize], h.attempt[:]) {
		return false, fmt.Errorf("%w: attempt ID mismatch", ErrBadHandshake)
	}
	if got := binary.BigEndian.Uint32(payload[AttemptSize:HandshakePrefixSize]); got != h.epoch {
		return false, fmt.Errorf("%w: epoch %d does not match %d", ErrBadHandshake, got, h.epoch)
	}
	_, cs1, cs2, err := h.hs.ReadMessage(nil, payload[HandshakePrefixSize:])
	if err != nil {
		return false, err
	}
	if cs1 == nil {
		return false, nil
	}
	return true, h.complete(cs1, cs2)
}

func (h *Handshake) complete(cs1, cs2 *noise.CipherState) error {
	peer := h.hs.PeerStatic()
	if len(peer) == 0 {
		return ErrPeerKey
	}
	if len(h.remote) == 0 {
		h.remote = append([]byte(nil), peer...)
	} else if !bytes.Equal(peer, h.remote) {
		return ErrPeerKey
	}
	send, recv := cs2, cs1
	if h.initiator {
		send, recv = cs1, cs2
	}
	h.session = newSession(h.epoch, send, recv)
	h.done = true
	h.doneAt = time.Now()
	return nil
}

// Session returns the completed session, or nil before completion.
func (h *Handshake) Session() *Session { return h.session }

// Remote returns the peer's static key, learned during the handshake.
func (h *Handshake) Remote() []byte { return h.remote }

// PeerKey returns the peer's static key as soon as it is known, which for a
// responder is right after message one is read.
func (h *Handshake) PeerKey() []byte {
	if len(h.remote) > 0 {
		return h.remote
	}
	return h.hs.PeerStatic()
}

// Confirmation builds the encrypted confirmation payload for this attempt.
func (h *Handshake) Confirmation() ([]byte, error) {
	if !h.done {
		return nil, ErrNotConfirmed
	}
	return append([]byte(nil), h.attempt[:]...), nil
}

// ParseConfirmation extracts the attempt ID from a confirmation payload.
func ParseConfirmation(payload []byte) ([AttemptSize]byte, error) {
	var attempt [AttemptSize]byte
	if len(payload) != AttemptSize {
		return attempt, fmt.Errorf("%w: confirmation payload is %d bytes", ErrBadHandshake, len(payload))
	}
	copy(attempt[:], payload)
	return attempt, nil
}

// maxPending bounds in-flight handshakes per peer.
const maxPending = 4

// maxPeers bounds the peer table, including peers learned but not confirmed.
const maxPeers = 256

// handshakeTimeout bounds how long an incomplete handshake may linger.
const handshakeTimeout = 30 * time.Second

type peerState struct {
	mu sync.Mutex

	public   []byte
	verified bool
	created  time.Time
	pending  map[[AttemptSize]byte]*Handshake
	ready    map[[AttemptSize]byte]*Session

	send          *Session
	sendAttempt   [AttemptSize]byte
	recv          map[uint32]*Session
	lastInstalled time.Time
}

// Manager owns per-peer handshakes and established sessions.
type Manager struct {
	local identity.Identity
	psk   []byte

	mu            sync.Mutex
	peers         map[uint64]*peerState
	acceptUnknown bool
	baseEpoch     uint32
	onLearned     func(id uint64, publicKey []byte)
}

// NewManager creates a session manager for the local identity and network secret.
func NewManager(local identity.Identity, psk []byte) (*Manager, error) {
	if len(psk) != identity.KeySize {
		return nil, fmt.Errorf("network secret must be %d bytes", identity.KeySize)
	}
	return &Manager{
		local:     local,
		psk:       append([]byte(nil), psk...),
		peers:     make(map[uint64]*peerState),
		baseEpoch: uint32(time.Now().Unix()),
	}, nil
}

// SetAcceptUnknown allows a responder to accept a handshake from a peer that is
// not configured yet. The peer must still prove the network secret, and its ID
// must match the static key it presents.
func (m *Manager) SetAcceptUnknown(accept bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acceptUnknown = accept
}

// SetPeerLearnedHandler is called when a previously unknown peer is accepted.
func (m *Manager) SetPeerLearnedHandler(fn func(id uint64, publicKey []byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onLearned = fn
}

func (m *Manager) learnedHandler() func(uint64, []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onLearned
}

// SetPeer registers or updates a peer's static public key. A peer ID that
// resolves to a different key is rejected.
func (m *Manager) SetPeer(id uint64, publicKey []byte) error {
	return m.setPeer(id, publicKey, true)
}

func (m *Manager) setPeer(id uint64, publicKey []byte, verified bool) error {
	derived, err := identity.PeerID(publicKey)
	if err != nil {
		return err
	}
	if derived != id {
		return fmt.Errorf("peer ID %016x does not match public key %016x", id, derived)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ps, ok := m.peers[id]; ok {
		if !bytes.Equal(ps.public, publicKey) {
			return fmt.Errorf("peer %016x is already bound to a different key", id)
		}
		if verified {
			ps.mu.Lock()
			ps.verified = true
			ps.mu.Unlock()
		}
		return nil
	}
	if len(m.peers) >= maxPeers {
		// Unverified entries are unauthenticated message-one state. Evict the
		// oldest instead of letting a stranger fill the table.
		var evict uint64
		var oldest time.Time
		for pid, ps := range m.peers {
			ps.mu.Lock()
			unverified, created := !ps.verified, ps.created
			ps.mu.Unlock()
			if unverified && (evict == 0 || created.Before(oldest)) {
				evict, oldest = pid, created
			}
		}
		if evict == 0 {
			return fmt.Errorf("peer table is full (%d entries)", maxPeers)
		}
		delete(m.peers, evict)
	}
	m.peers[id] = &peerState{
		public:   append([]byte(nil), publicKey...),
		verified: verified,
		created:  time.Now(),
		pending:  make(map[[AttemptSize]byte]*Handshake),
		ready:    make(map[[AttemptSize]byte]*Session),
		recv:     make(map[uint32]*Session),
	}
	return nil
}

// ResetPeer drops sessions and handshakes but keeps the registered key, so a
// timed-out peer can reconnect without being re-added.
func (m *Manager) ResetPeer(id uint64) {
	m.mu.Lock()
	ps, ok := m.peers[id]
	m.mu.Unlock()
	if !ok {
		return
	}
	ps.mu.Lock()
	ps.pending = make(map[[AttemptSize]byte]*Handshake)
	ps.ready = make(map[[AttemptSize]byte]*Session)
	ps.recv = make(map[uint32]*Session)
	ps.send = nil
	ps.sendAttempt = [AttemptSize]byte{}
	ps.lastInstalled = time.Time{}
	ps.mu.Unlock()
}

// RemovePeer forgets all handshake and session state for a peer.
func (m *Manager) RemovePeer(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, id)
}

// Begin starts an initiator handshake for peerID at the next epoch.
func (m *Manager) Begin(peerID uint64) (attempt [AttemptSize]byte, payload []byte, err error) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return attempt, nil, fmt.Errorf("peer %016x is not registered", peerID)
	}
	if _, err := rand.Read(attempt[:]); err != nil {
		return attempt, nil, err
	}

	ps.mu.Lock()
	ps.pruneLocked()
	if len(ps.pending) >= maxPending {
		ps.mu.Unlock()
		return attempt, nil, fmt.Errorf("%w: too many handshake attempts in flight", ErrBadHandshake)
	}
	epoch := m.baseEpoch
	if epoch == 0 || epoch == ^uint32(0) {
		epoch = 1
	}
	if ps.send != nil && ps.send.Epoch >= epoch {
		epoch = ps.send.Epoch + 1
		if epoch == 0 {
			epoch = 1
		}
	}
	ps.mu.Unlock()

	hs, err := NewHandshake(m.local, ps.public, m.psk, true, attempt, epoch)
	if err != nil {
		return attempt, nil, err
	}
	payload, err = hs.Write()
	if err != nil {
		return attempt, nil, err
	}
	ps.mu.Lock()
	ps.pending[attempt] = hs
	ps.mu.Unlock()
	return attempt, payload, nil
}

// HandleReply consumes the responder's handshake message for a local attempt.
func (m *Manager) HandleReply(peerID uint64, attempt [AttemptSize]byte, payload []byte) error {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("peer %016x is not registered", peerID)
	}
	ps.mu.Lock()
	hs, ok := ps.pending[attempt]
	ps.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: unknown attempt", ErrBadHandshake)
	}
	done, err := hs.Read(payload)
	if err != nil {
		ps.mu.Lock()
		delete(ps.pending, attempt)
		ps.mu.Unlock()
		return err
	}
	if !done {
		return fmt.Errorf("%w: handshake did not complete", ErrBadHandshake)
	}
	ps.mu.Lock()
	ps.ready[attempt] = hs.Session()
	ps.mu.Unlock()
	return nil
}

// HandleHandshake consumes an inbound handshake payload. When the handshake
// completes it returns the reply to send and the attempt that became ready.
func (m *Manager) HandleHandshake(peerID uint64, payload []byte) (reply []byte, attempt [AttemptSize]byte, err error) {
	if len(payload) < HandshakePrefixSize {
		return nil, attempt, ErrBadHandshake
	}
	copy(attempt[:], payload[:AttemptSize])
	epoch := binary.BigEndian.Uint32(payload[AttemptSize:HandshakePrefixSize])
	if epoch == 0 || epoch == ^uint32(0) {
		return nil, attempt, fmt.Errorf("%w: invalid epoch %d", ErrBadHandshake, epoch)
	}

	m.mu.Lock()
	ps, ok := m.peers[peerID]
	acceptUnknown := m.acceptUnknown
	m.mu.Unlock()
	if !ok {
		if !acceptUnknown {
			return nil, attempt, fmt.Errorf("peer %016x is not registered", peerID)
		}
		hs, err := NewResponderHandshake(m.local, peerID, m.psk, attempt, epoch)
		if err != nil {
			return nil, attempt, err
		}
		if _, err := hs.Read(payload); err != nil {
			return nil, attempt, err
		}
		pub := hs.PeerKey()
		derived, err := identity.PeerID(pub)
		if err != nil || derived != peerID {
			return nil, attempt, fmt.Errorf("%w: claimed ID %016x does not match the handshake key", ErrPeerKey, peerID)
		}
		// Register the key but do not trust the peer yet: the PSK is only
		// proven when the initiator decrypts message two and confirms.
		if err := m.setPeer(peerID, pub, false); err != nil {
			return nil, attempt, err
		}
		m.mu.Lock()
		ps = m.peers[peerID]
		m.mu.Unlock()
		ps.mu.Lock()
		ps.pending[attempt] = hs
		ps.mu.Unlock()
		reply, err := hs.Write()
		if err != nil {
			return nil, attempt, err
		}
		if sess := hs.Session(); sess != nil {
			ps.mu.Lock()
			ps.ready[attempt] = sess
			ps.mu.Unlock()
		}
		return reply, attempt, nil
	}

	ps.mu.Lock()
	ps.pruneLockedExcept(attempt)
	hs, ok := ps.pending[attempt]
	full := len(ps.pending) >= maxPending
	ps.mu.Unlock()
	if !ok {
		if full {
			return nil, attempt, fmt.Errorf("%w: too many handshake attempts in flight", ErrBadHandshake)
		}
		var err error
		hs, err = NewHandshake(m.local, ps.public, m.psk, false, attempt, epoch)
		if err != nil {
			return nil, attempt, err
		}
		ps.mu.Lock()
		ps.pending[attempt] = hs
		ps.mu.Unlock()
	}
	done, err := hs.Read(payload)
	if err != nil {
		if !hs.initiator {
			// Never let a forged message-one delete our own outbound attempt.
			ps.mu.Lock()
			delete(ps.pending, attempt)
			ps.mu.Unlock()
		}
		return nil, attempt, err
	}
	if !done {
		reply, err = hs.Write()
		if err != nil {
			return nil, attempt, err
		}
		if sess := hs.Session(); sess != nil {
			ps.mu.Lock()
			ps.ready[attempt] = sess
			ps.mu.Unlock()
		}
		return reply, attempt, nil
	}
	ps.mu.Lock()
	ps.ready[attempt] = hs.Session()
	ps.mu.Unlock()
	return nil, attempt, nil
}

// Confirmation returns the attempt ID to send for a completed handshake.
func (m *Manager) Confirmation(peerID uint64, attempt [AttemptSize]byte) ([]byte, error) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("peer %016x is not registered", peerID)
	}
	ps.mu.Lock()
	hs, ok := ps.pending[attempt]
	ps.mu.Unlock()
	if !ok || !hs.done {
		return nil, ErrNotConfirmed
	}
	return hs.Confirmation()
}

// SealReady encrypts with a completed but not yet installed handshake session.
// It is used to carry the confirmation that installs that session.
func (m *Manager) SealReady(peerID uint64, attempt [AttemptSize]byte, ad, plaintext []byte) (epoch uint32, counter uint64, ciphertext []byte, err error) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return 0, 0, nil, fmt.Errorf("peer %016x is not registered", peerID)
	}
	ps.mu.Lock()
	sess := ps.ready[attempt]
	ps.mu.Unlock()
	if sess == nil {
		return 0, 0, nil, ErrNotConfirmed
	}
	return sess.Seal(ad, plaintext)
}

// HandleConfirm decrypts a confirmation with a completed handshake session and
// installs it when the attempt matches. Because the session is not yet
// installed, every ready session for the epoch is tried.
func (m *Manager) HandleConfirm(peerID uint64, epoch uint32, counter uint64, ad, ciphertext []byte) error {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("peer %016x is not registered", peerID)
	}

	type candidate struct {
		attempt [AttemptSize]byte
		sess    *Session
	}
	ps.mu.Lock()
	candidates := make([]candidate, 0, len(ps.ready))
	for attempt, sess := range ps.ready {
		if sess.Epoch == epoch {
			candidates = append(candidates, candidate{attempt: attempt, sess: sess})
		}
	}
	ps.mu.Unlock()

	for _, c := range candidates {
		plaintext, err := c.sess.Open(counter, ad, ciphertext)
		if err != nil {
			continue
		}
		attempt, err := ParseConfirmation(plaintext)
		if err != nil || attempt != c.attempt {
			continue
		}
		ps.mu.Lock()
		if ps.consider(c.sess, c.attempt) {
			ps.install(c.sess, c.attempt)
		}
		firstContact := !ps.verified
		ps.verified = true
		pub := append([]byte(nil), ps.public...)
		ps.mu.Unlock()
		if firstContact {
			// The peer proved the network secret by confirming the session.
			if fn := m.learnedHandler(); fn != nil {
				fn(peerID, pub)
			}
		}
		return nil
	}
	return ErrNotConfirmed
}

// consider reports whether candidate should replace the active send session.
func (ps *peerState) consider(candidate *Session, attempt [AttemptSize]byte) bool {
	if candidate.Epoch == 0 {
		return false
	}
	if ps.send == nil {
		return true
	}
	if candidate.Epoch != ps.send.Epoch {
		if candidate.Epoch > ps.send.Epoch {
			return true
		}
		// A lower epoch is only acceptable once the current key has expired.
		return ps.send.Expired(time.Now())
	}
	return bytes.Compare(attempt[:], ps.sendAttempt[:]) < 0
}

func (ps *peerState) install(sess *Session, attempt [AttemptSize]byte) {
	ps.send = sess
	ps.sendAttempt = attempt
	ps.lastInstalled = time.Now()
	ps.recv[sess.Epoch] = sess
	for epoch := range ps.recv {
		if epoch+1 < sess.Epoch {
			delete(ps.recv, epoch)
		}
	}
	// The confirmation has arrived, so this attempt is no longer in flight.
	delete(ps.pending, attempt)
	delete(ps.ready, attempt)
	ps.pruneLocked()
}

// pruneLocked drops completed handshakes once the pending map is over budget,
// and expires handshakes that never completed.
func (ps *peerState) pruneLocked() {
	ps.pruneLockedExcept([AttemptSize]byte{})
}

// pruneLockedExcept expires stale attempts, keeping the attempt currently
// being processed so a late but valid message is not discarded first.
func (ps *peerState) pruneLockedExcept(keep [AttemptSize]byte) {
	now := time.Now()
	for attempt, hs := range ps.pending {
		if attempt == keep {
			continue
		}
		age := now.Sub(hs.started)
		if hs.done {
			age = now.Sub(hs.doneAt)
		}
		if age > handshakeTimeout {
			delete(ps.pending, attempt)
			delete(ps.ready, attempt)
		}
	}
	for attempt, sess := range ps.ready {
		if attempt != keep && now.Sub(sess.created) > 2*handshakeTimeout {
			delete(ps.ready, attempt)
		}
	}
}

// HasSession reports whether a confirmed session exists for peerID.
func (m *Manager) HasSession(peerID uint64) bool {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.send != nil
}

// ReadyEpoch reports the epoch of a completed but uninstalled handshake.
func (m *Manager) ReadyEpoch(peerID uint64, attempt [AttemptSize]byte) (uint32, bool) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return 0, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	sess := ps.ready[attempt]
	if sess == nil {
		return 0, false
	}
	return sess.Epoch, true
}

// ActiveAttempt returns the attempt ID backing the current send session.
func (m *Manager) ActiveAttempt(peerID uint64) ([AttemptSize]byte, bool) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return [AttemptSize]byte{}, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.send == nil {
		return [AttemptSize]byte{}, false
	}
	return ps.sendAttempt, true
}

// ActiveEpoch returns the current send epoch.
func (m *Manager) ActiveEpoch(peerID uint64) (uint32, bool) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return 0, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.send == nil {
		return 0, false
	}
	return ps.send.Epoch, true
}

// NeedsRekey reports whether the active epoch should be replaced now.
func (m *Manager) NeedsRekey(peerID uint64, now time.Time) bool {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.send != nil && ps.send.Expired(now)
}

// Seal encrypts plaintext with the peer's active send session.
func (m *Manager) Seal(peerID uint64, ad, plaintext []byte) (epoch uint32, counter uint64, ciphertext []byte, err error) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return 0, 0, nil, ErrNoSession
	}
	ps.mu.Lock()
	sess := ps.send
	ps.mu.Unlock()
	if sess == nil {
		return 0, 0, nil, ErrNoSession
	}
	return sess.Seal(ad, plaintext)
}

// Open decrypts a payload for the current or previous epoch of peerID.
func (m *Manager) Open(peerID uint64, epoch uint32, counter uint64, ad, ciphertext []byte) ([]byte, error) {
	m.mu.Lock()
	ps, ok := m.peers[peerID]
	m.mu.Unlock()
	if !ok {
		return nil, ErrNoSession
	}
	ps.mu.Lock()
	sess, ok := ps.recv[epoch]
	ps.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: epoch %d", ErrStaleEpoch, epoch)
	}
	return sess.Open(counter, ad, ciphertext)
}
