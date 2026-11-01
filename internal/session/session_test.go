package session

import (
	"bytes"
	"crypto/rand"
	"math"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

type pair struct {
	a, b   *Manager
	aID    uint64
	bID    uint64
	aIdent identity.Identity
	bIdent identity.Identity
	psk    []byte
}

func newPair(t *testing.T) pair {
	t.Helper()
	a, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	psk := make([]byte, identity.KeySize)
	if _, err := rand.Read(psk); err != nil {
		t.Fatal(err)
	}
	am, err := NewManager(a, psk)
	if err != nil {
		t.Fatal(err)
	}
	bm, err := NewManager(b, psk)
	if err != nil {
		t.Fatal(err)
	}
	if err := am.SetPeer(b.ID, b.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := bm.SetPeer(a.ID, a.PublicKey); err != nil {
		t.Fatal(err)
	}
	return pair{a: am, b: bm, aID: a.ID, bID: b.ID, aIdent: a, bIdent: b, psk: psk}
}

// establish runs one full handshake initiated by p.a and installs both sides.
func establish(t *testing.T, p pair) [AttemptSize]byte {
	t.Helper()
	attempt, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	reply, _, err := p.b.HandleHandshake(p.aID, msg1)
	if err != nil {
		t.Fatalf("HandleHandshake: %v", err)
	}
	if reply == nil {
		t.Fatal("responder produced no reply")
	}
	if err := p.a.HandleReply(p.bID, attempt, reply); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	if err := confirmBoth(t, p, attempt); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return attempt
}

func confirmBoth(t *testing.T, p pair, attempt [AttemptSize]byte) error {
	t.Helper()
	return confirmPair(t, p.a, p.bID, p.b, p.aID, attempt)
}

func confirmPair(t *testing.T, a *Manager, aPeer uint64, b *Manager, bPeer uint64, attempt [AttemptSize]byte) error {
	t.Helper()
	ad := []byte("confirm-ad")
	// Prepare both confirmations before either side installs and forgets the
	// completed handshake state.
	aPayload, err := a.Confirmation(aPeer, attempt)
	if err != nil {
		return err
	}
	aEpoch, aCounter, aCT, err := a.SealReady(aPeer, attempt, ad, aPayload)
	if err != nil {
		return err
	}
	bPayload, err := b.Confirmation(bPeer, attempt)
	if err != nil {
		return err
	}
	bEpoch, bCounter, bCT, err := b.SealReady(bPeer, attempt, ad, bPayload)
	if err != nil {
		return err
	}
	if err := b.HandleConfirm(bPeer, aEpoch, aCounter, ad, aCT); err != nil {
		return err
	}
	return a.HandleConfirm(aPeer, bEpoch, bCounter, ad, bCT)
}

func sealConfirm(t *testing.T, m *Manager, peerID uint64, attempt [AttemptSize]byte) (uint32, uint64, []byte) {
	t.Helper()
	payload, err := m.Confirmation(peerID, attempt)
	if err != nil {
		t.Fatalf("Confirmation: %v", err)
	}
	epoch, counter, ct, err := m.SealReady(peerID, attempt, []byte("confirm-ad"), payload)
	if err != nil {
		t.Fatalf("SealReady: %v", err)
	}
	return epoch, counter, ct
}

func attemptEpoch(t *testing.T, m *Manager, peerID uint64, attempt [AttemptSize]byte) uint32 {
	t.Helper()
	m.mu.Lock()
	ps := m.peers[peerID]
	m.mu.Unlock()
	ps.mu.Lock()
	defer ps.mu.Unlock()
	hs, ok := ps.pending[attempt]
	if !ok {
		t.Fatalf("attempt %x not pending", attempt)
	}
	return hs.Epoch()
}

func TestHandshakeAndDataRoundTrip(t *testing.T) {
	p := newPair(t)
	establish(t, p)

	ad := []byte("associated data")
	epoch, counter, ct, err := p.a.Seal(p.bID, ad, []byte("hello"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := p.b.Open(p.aID, epoch, counter, ad, ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("plaintext = %q", got)
	}
}

func TestWrongPSKFailsHandshake(t *testing.T) {
	p := newPair(t)
	// Rebuild B's manager with a different secret.
	other := make([]byte, identity.KeySize)
	if _, err := rand.Read(other); err != nil {
		t.Fatal(err)
	}
	bm, err := NewManager(p.bIdent, other)
	if err != nil {
		t.Fatal(err)
	}
	if err := bm.SetPeer(p.aID, p.aIdent.PublicKey); err != nil {
		t.Fatal(err)
	}
	attempt, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := bm.HandleHandshake(p.aID, msg1)
	if err != nil {
		// An impostor cannot even decrypt the initiator's static key.
		return
	}
	// The PSK is mixed into message two, so the initiator detects the mismatch.
	if err := p.a.HandleReply(p.bID, attempt, reply); err == nil {
		t.Fatal("expected handshake with the wrong PSK to fail")
	}
}

func TestWrongResponderKeyIsRejected(t *testing.T) {
	p := newPair(t)
	impostor, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bm, err := NewManager(impostor, mustSecret(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := bm.SetPeer(p.aID, p.aIdent.PublicKey); err != nil {
		t.Fatal(err)
	}
	// A still expects B's real key.
	attempt, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := bm.HandleHandshake(p.aID, msg1)
	if err != nil {
		// An impostor cannot even decrypt the initiator's static key.
		return
	}
	if err := p.a.HandleReply(p.bID, attempt, reply); err == nil {
		t.Fatal("expected a responder with the wrong static key to be rejected")
	}
}

func TestAlteredDataIsRejected(t *testing.T) {
	p := newPair(t)
	establish(t, p)
	ad := []byte("ad")
	epoch, counter, ct, err := p.a.Seal(p.bID, ad, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}

	altered := append([]byte(nil), ct...)
	altered[len(altered)-1] ^= 0xff
	if _, err := p.b.Open(p.aID, epoch, counter, ad, altered); err == nil {
		t.Fatal("expected tampered ciphertext to be rejected")
	}
	if _, err := p.b.Open(p.aID, epoch, counter, []byte("different ad"), ct); err == nil {
		t.Fatal("expected altered associated data to be rejected")
	}
	if _, err := p.b.Open(p.aID, epoch+7, counter, ad, ct); err == nil {
		t.Fatal("expected unknown epoch to be rejected")
	}
}

func TestReplayIsRejected(t *testing.T) {
	p := newPair(t)
	establish(t, p)
	epoch, counter, ct, err := p.a.Seal(p.bID, nil, []byte("once"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.b.Open(p.aID, epoch, counter, nil, ct); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := p.b.Open(p.aID, epoch, counter, nil, ct); err == nil {
		t.Fatal("expected replay to be rejected")
	}
}

func TestReplayWindowReordering(t *testing.T) {
	p := newPair(t)
	establish(t, p)

	type packet struct {
		epoch   uint32
		counter uint64
		ct      []byte
	}
	packets := make([]packet, 128)
	for i := range packets {
		epoch, counter, ct, err := p.a.Seal(p.bID, nil, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		packets[i] = packet{epoch, counter, ct}
	}
	// Deliver the highest counter first, then the window in reverse order.
	for i := len(packets) - 1; i >= 64; i-- {
		if _, err := p.b.Open(p.aID, packets[i].epoch, packets[i].counter, nil, packets[i].ct); err != nil {
			t.Fatalf("counter %d: %v", packets[i].counter, err)
		}
	}
	if _, err := p.b.Open(p.aID, packets[100].epoch, packets[100].counter, nil, packets[100].ct); err == nil {
		t.Fatal("expected duplicate counter 100 to be rejected")
	}
	// Counter 63 is exactly one past the 64-packet window.
	if _, err := p.b.Open(p.aID, packets[63].epoch, packets[63].counter, nil, packets[63].ct); err == nil {
		t.Fatal("expected counter 63, one past the 64-packet window, to be rejected")
	}
}

func TestRekeySupersedesAndKeepsPreviousEpoch(t *testing.T) {
	p := newPair(t)
	establish(t, p)

	oldEpoch, oldCounter, oldCT, err := p.a.Seal(p.bID, nil, []byte("epoch one"))
	if err != nil {
		t.Fatal(err)
	}

	establish(t, p)
	newEpoch, _, _, err := p.a.Seal(p.bID, nil, []byte("epoch two"))
	if err != nil {
		t.Fatal(err)
	}
	if newEpoch != oldEpoch+1 {
		t.Fatalf("epoch = %d, want %d", newEpoch, oldEpoch+1)
	}
	// The previous epoch stays readable during the bounded overlap.
	if _, err := p.b.Open(p.aID, oldEpoch, oldCounter, nil, oldCT); err != nil {
		t.Fatalf("previous epoch rejected: %v", err)
	}
}

func TestHigherEpochWinsConfirmationRace(t *testing.T) {
	p := newPair(t)
	establish(t, p)

	// Start epoch two but hold the confirmation.
	attempt2, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := p.b.HandleHandshake(p.aID, msg1)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.a.HandleReply(p.bID, attempt2, reply); err != nil {
		t.Fatal(err)
	}
	// A late epoch-one confirmation must not displace epoch two.
	epoch2 := attemptEpoch(t, p.a, p.bID, attempt2)
	if epoch2 != 2 {
		t.Fatalf("epoch = %d, want 2", epoch2)
	}
	if err := confirmBoth(t, p, attempt2); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.a.ActiveEpoch(p.bID); got != 2 {
		t.Fatalf("active epoch = %d, want 2", got)
	}
	if got, _ := p.b.ActiveEpoch(p.aID); got != 2 {
		t.Fatalf("responder active epoch = %d, want 2", got)
	}
}

func TestSimultaneousAttemptsAgreeOnWinner(t *testing.T) {
	p := newPair(t)

	attemptA, msgA, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	attemptB, msgB, err := p.b.Begin(p.aID)
	if err != nil {
		t.Fatal(err)
	}
	replyToA, _, err := p.b.HandleHandshake(p.aID, msgA)
	if err != nil {
		t.Fatal(err)
	}
	replyToB, _, err := p.a.HandleHandshake(p.bID, msgB)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.a.HandleReply(p.bID, attemptA, replyToA); err != nil {
		t.Fatal(err)
	}
	if err := p.b.HandleReply(p.aID, attemptB, replyToB); err != nil {
		t.Fatal(err)
	}

	// Seal every confirmation before any side installs and forgets its ready set.
	type conf struct {
		epoch   uint32
		counter uint64
		ct      []byte
	}
	sealed := map[string]conf{}
	for _, c := range []struct {
		key     string
		m       *Manager
		peerID  uint64
		attempt [AttemptSize]byte
	}{
		{"aA", p.a, p.bID, attemptA},
		{"aB", p.a, p.bID, attemptB},
		{"bA", p.b, p.aID, attemptA},
		{"bB", p.b, p.aID, attemptB},
	} {
		epoch, counter, ct := sealConfirm(t, c.m, c.peerID, c.attempt)
		sealed[c.key] = conf{epoch, counter, ct}
	}
	// Deliver confirmations in opposite orders on each side: A's confirmations
	// go to B, B's go to A.
	if err := p.b.HandleConfirm(p.aID, sealed["aA"].epoch, sealed["aA"].counter, []byte("confirm-ad"), sealed["aA"].ct); err != nil {
		t.Fatal(err)
	}
	if err := p.a.HandleConfirm(p.bID, sealed["bA"].epoch, sealed["bA"].counter, []byte("confirm-ad"), sealed["bA"].ct); err != nil {
		t.Fatal(err)
	}
	if err := p.b.HandleConfirm(p.aID, sealed["aB"].epoch, sealed["aB"].counter, []byte("confirm-ad"), sealed["aB"].ct); err != nil {
		t.Fatal(err)
	}
	if err := p.a.HandleConfirm(p.bID, sealed["bB"].epoch, sealed["bB"].counter, []byte("confirm-ad"), sealed["bB"].ct); err != nil {
		t.Fatal(err)
	}

	want := attemptA
	if bytes.Compare(attemptB[:], attemptA[:]) < 0 {
		want = attemptB
	}
	gotA, okA := p.a.ActiveAttempt(p.bID)
	gotB, okB := p.b.ActiveAttempt(p.aID)
	if !okA || !okB {
		t.Fatal("missing active attempt")
	}
	if gotA != want || gotB != want {
		t.Fatalf("winners = %x/%x, want %x", gotA, gotB, want)
	}
	// The agreed session must actually carry data.
	e, c, ct, err := p.a.Seal(p.bID, nil, []byte("simultaneous"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.b.Open(p.aID, e, c, nil, ct); err != nil {
		t.Fatalf("agreed session cannot decrypt: %v", err)
	}
}

func TestConfirmationIsRequiredBeforeUse(t *testing.T) {
	p := newPair(t)
	attempt, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := p.b.HandleHandshake(p.aID, msg1)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.a.HandleReply(p.bID, attempt, reply); err != nil {
		t.Fatal(err)
	}
	if p.a.HasSession(p.bID) || p.b.HasSession(p.aID) {
		t.Fatal("session installed before confirmation")
	}
	if _, _, _, err := p.a.Seal(p.bID, nil, []byte("x")); err == nil {
		t.Fatal("expected Seal to fail without a confirmed session")
	}
	// A forged confirmation for an unknown attempt must be rejected.
	if err := p.b.HandleConfirm(p.aID, 1, 0, nil, make([]byte, AttemptSize)); err == nil {
		t.Fatal("expected unknown confirmation to be rejected")
	}
}

func TestCounterExhaustion(t *testing.T) {
	p := newPair(t)
	establish(t, p)
	p.a.mu.Lock()
	ps := p.a.peers[p.bID]
	p.a.mu.Unlock()
	ps.mu.Lock()
	ps.send.sendSeq = math.MaxUint64
	ps.mu.Unlock()
	if _, _, _, err := p.a.Seal(p.bID, nil, []byte("x")); err == nil {
		t.Fatal("expected counter exhaustion error")
	}
}

func TestExpiry(t *testing.T) {
	s := newSession(1, nil, nil)
	if s.Expired(time.Now()) {
		t.Fatal("fresh session reported expired")
	}
	s.created = time.Now().Add(-2 * RekeyInterval)
	if !s.Expired(time.Now()) {
		t.Fatal("old session reported fresh")
	}
}

func TestReplayWindowUnit(t *testing.T) {
	var w replayWindow
	if !w.acceptable(0) {
		t.Fatal("first counter rejected")
	}
	w.accept(0)
	if w.acceptable(0) {
		t.Fatal("duplicate counter accepted")
	}
	for i := uint64(1); i < 64; i++ {
		if !w.acceptable(i) {
			t.Fatalf("counter %d rejected", i)
		}
		w.accept(i)
	}
	if w.acceptable(0) {
		t.Fatal("counter 0 still inside window after advancing to 63")
	}
	if !w.acceptable(64) {
		t.Fatal("counter beyond highest rejected")
	}
}

func mustSecret(t *testing.T) []byte {
	t.Helper()
	psk := make([]byte, identity.KeySize)
	if _, err := rand.Read(psk); err != nil {
		t.Fatal(err)
	}
	return psk
}

func TestUnknownPeerIsAcceptedWhenItProvesTheSecret(t *testing.T) {
	p := newPair(t)
	// Only the initiator knows the responder; the responder accepts newcomers.
	var (
		learnedID  uint64
		learnedKey []byte
	)
	// A fresh responder that has never seen the initiator.
	bm, err := NewManager(p.bIdent, p.psk)
	if err != nil {
		t.Fatal(err)
	}
	bm.SetAcceptUnknown(true)
	bm.SetPeerLearnedHandler(func(id uint64, pub []byte) {
		learnedID = id
		learnedKey = append([]byte(nil), pub...)
	})

	attempt, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := bm.HandleHandshake(p.aID, msg1)
	if err != nil {
		t.Fatalf("HandleHandshake: %v", err)
	}
	if learnedID != 0 {
		t.Fatal("peer was trusted before the secret was proven")
	}
	if err := p.a.HandleReply(p.bID, attempt, reply); err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	if err := confirmPair(t, p.a, p.bID, bm, p.aID, attempt); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if learnedID != p.aID || !bytes.Equal(learnedKey, p.aIdent.PublicKey) {
		t.Fatalf("learned %016x key=%x", learnedID, learnedKey)
	}
	if !bm.HasSession(p.aID) {
		t.Fatal("responder did not install the session")
	}
}

func TestUnknownPeerWithMismatchedIDIsRejected(t *testing.T) {
	p := newPair(t)
	bm, err := NewManager(p.bIdent, p.psk)
	if err != nil {
		t.Fatal(err)
	}
	bm.SetAcceptUnknown(true)
	_, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	// Claim a different peer ID than the key in the handshake.
	if _, _, err := bm.HandleHandshake(p.bID, msg1); err == nil {
		t.Fatal("expected the claimed ID to be rejected")
	}
}

func TestUnknownPeerWithWrongSecretNeverConfirms(t *testing.T) {
	p := newPair(t)
	bm, err := NewManager(p.bIdent, mustSecret(t))
	if err != nil {
		t.Fatal(err)
	}
	bm.SetAcceptUnknown(true)
	var learned uint64
	bm.SetPeerLearnedHandler(func(id uint64, _ []byte) { learned = id })

	attempt, msg1, err := p.a.Begin(p.bID)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := bm.HandleHandshake(p.aID, msg1)
	if err != nil {
		// A responder may also reject message one outright; either is fine.
		return
	}
	// The initiator holds a different secret, so it cannot accept message two.
	if err := p.a.HandleReply(p.bID, attempt, reply); err == nil {
		t.Fatal("expected the initiator to reject a responder with a different secret")
	}
	if learned != 0 {
		t.Fatal("peer was trusted without proving the secret")
	}
	if bm.HasSession(p.aID) {
		t.Fatal("session installed without a confirmed secret")
	}
}
