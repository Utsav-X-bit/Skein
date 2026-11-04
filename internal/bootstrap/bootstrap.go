// Package bootstrap implements the ADR-001 Step 4 single-use bootstrap
// token: the only mechanism by which an unconfigured daemon accepts its
// first config/identity. It is deliberately small and free of daemon
// dependencies, so the minimal helper (which mints) and the daemon
// (which consumes) share exactly one state format and one verification
// order, and so the token logic can be tested without a running mesh.
//
// Security invariants (approved by the user, 2026-11-04; do not weaken):
//   - The plaintext token is never written to disk, never logged, and
//     never appears in an error message. The state file carries only
//     its SHA-256 hash.
//   - A token is bound to one caller (kernel uid on Unix, SID on
//     Windows) and one verb (init, join, or import). A token minted
//     for join cannot init, and vice versa.
//   - A token lives 10 minutes, may be used exactly once, and dies
//     after 5 failed verification attempts (the 5th attempt returns
//     ErrBurned and the state file is deleted).
//   - Verification order is fixed: caller match, constant-time hash
//     match, expiry, unused. Expiry and used are terminal states
//     (the state file is deleted on expiry; a used token cannot be
//     replayed because success deletes the state file).
//   - The token is not a password and grants no ongoing access: on
//     success the daemon grants the bootstrap caller the secret role
//     in the access file, and the token itself is dead.
//
// Explicitly rejected and NOT implemented here: unauthenticated
// bootstrap from any local uid, a long-lived bootstrap password, a
// plaintext token on disk, and a bearer-token dev-TCP bootstrap (the
// daemon's bootstrap handler rejects non-kernel transports before
// this package is even consulted).
package bootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

const (
	// TokenSize is the token entropy: 256 bits from crypto/rand.
	TokenSize = 32
	// TTL is the token lifetime from minting.
	TTL = 10 * time.Minute
	// MaxAttempts is the number of failed verifications that burn a token.
	MaxAttempts = 5
	// StateFileName is the bootstrap-state file name inside the system
	// directory (e.g. /etc/skein/bootstrap.json).
	StateFileName = "bootstrap.json"
)

// Verbs a token may be minted for. The verb is part of the token's
// binding: the daemon endpoint's verb must equal the minted verb.
const (
	VerbInit   = "init"
	VerbJoin   = "join"
	VerbImport = "import"
)

// ValidVerb reports whether v is a bootstrap verb.
func ValidVerb(v string) bool {
	switch v {
	case VerbInit, VerbJoin, VerbImport:
		return true
	}
	return false
}

// ErrNoState is returned when no bootstrap-state file exists: there is
// no bootstrap in progress, and the daemon must not enter bootstrap mode.
var ErrNoState = errors.New("bootstrap: no bootstrap state")

// ErrBurned is returned when a token has been burned by too many failed
// attempts. The state file is deleted by then; a fresh elevation is
// required.
var ErrBurned = errors.New("bootstrap: token burned after too many failed attempts")

// ErrExpired is returned for an expired token. The state file is
// deleted; a fresh elevation is required.
var ErrExpired = errors.New("bootstrap: token expired")

// ErrUsed is returned when a token was already consumed.
var ErrUsed = errors.New("bootstrap: token already used")

// State is the on-disk bootstrap state. It contains the token's hash
// only — never the plaintext token.
type State struct {
	// TokenHash is the hex SHA-256 of the plaintext token.
	TokenHash string `json:"token_hash"`
	// Caller is the kernel caller identity the token is bound to:
	// "uid:<n>" on Unix, a SID on Windows (api.Caller.ID() format).
	Caller string `json:"caller"`
	// Verb is the single bootstrap operation this token authorises.
	Verb string `json:"verb"`
	// ExpiresAt is the mint time + TTL.
	ExpiresAt time.Time `json:"expires_at"`
	// Attempts counts failed verifications against this token.
	Attempts int `json:"attempts"`
	// Used is set atomically before any config work on success, so a
	// crashed bootstrap cannot be replayed.
	Used bool `json:"used"`
}

// StatePath returns the bootstrap-state path inside dir.
func StatePath(dir string) string { return filepath.Join(dir, StateFileName) }

// hashToken returns the hex SHA-256 of the plaintext token.
func hashToken(token []byte) string {
	sum := sha256.Sum256(token)
	return hex.EncodeToString(sum[:])
}

// Mint generates a fresh token, writes its state file atomically
// (0600, temp + rename in the same directory), and returns the
// plaintext token. The caller owns the returned bytes and must zero
// them after use; Mint itself never logs or retains them. Mint refuses
// to overwrite a still-valid, unused state file: two concurrent
// elevations must not silently kill each other's token.
func Mint(statePath, caller, verb string) ([]byte, error) {
	if caller == "" {
		return nil, errors.New("bootstrap: cannot mint a token without an invoking caller identity")
	}
	if !ValidVerb(verb) {
		return nil, fmt.Errorf("bootstrap: unknown verb %q (want init, join, or import)", verb)
	}
	if st, err := Load(statePath); err == nil && !st.Used && time.Now().Before(st.ExpiresAt) {
		return nil, errors.New("bootstrap: a bootstrap is already in progress; wait for it to expire or complete before minting a new token")
	} else if err != nil && !errors.Is(err, ErrNoState) {
		return nil, err
	}
	token := make([]byte, TokenSize)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("bootstrap: random token: %w", err)
	}
	st := State{
		TokenHash: hashToken(token),
		Caller:    caller,
		Verb:      verb,
		ExpiresAt: time.Now().Add(TTL),
	}
	if err := writeState(statePath, st); err != nil {
		clear(token)
		return nil, err
	}
	return token, nil
}

// Load reads a bootstrap-state file. A missing file is ErrNoState.
func Load(statePath string) (State, error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, ErrNoState
		}
		return State{}, fmt.Errorf("bootstrap: read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("bootstrap: corrupt state file: %w", err)
	}
	if st.TokenHash == "" || st.Caller == "" || !ValidVerb(st.Verb) {
		return State{}, errors.New("bootstrap: corrupt state file: missing hash, caller, or verb")
	}
	return st, nil
}

// Valid reports whether a loaded state authorises bootstrap mode:
// present, unused, and unexpired. It does not check the token value.
func (st State) Valid() bool {
	return !st.Used && time.Now().Before(st.ExpiresAt)
}

// writeState writes st atomically (0600, temp + rename, temp removed on
// error) so a crash cannot leave a half-written state file.
func writeState(statePath string, st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	dir := filepath.Dir(statePath)
	tmp, err := os.CreateTemp(dir, "bootstrap-*.tmp")
	if err != nil {
		return fmt.Errorf("bootstrap: write state: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("bootstrap: write state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("bootstrap: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("bootstrap: write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("bootstrap: write state: %w", err)
	}
	// Chmod is a no-op on Windows, so restrict the DACL explicitly:
	// creator + SYSTEM + Administrators, nothing else.
	if err := fileperm.Restrict(name); err != nil {
		return fmt.Errorf("bootstrap: write state: %w", err)
	}
	return os.Rename(name, statePath)
}

// Manager serialises verification against one state file. The daemon
// holds one Manager for its bootstrap lifetime; the helper never uses
// one (it mints and then hands the daemon the state file).
type Manager struct {
	path string
	mu   sync.Mutex
}

// NewManager binds a Manager to a state file path.
func NewManager(statePath string) *Manager { return &Manager{path: statePath} }

// Path returns the state file path.
func (m *Manager) Path() string { return m.path }

// Load returns the current state without verifying a token.
func (m *Manager) Load() (State, error) { return Load(m.path) }

// DeleteState removes a state file by path. Missing is not an error.
func DeleteState(statePath string) error {
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: delete state: %w", err)
	}
	return nil
}

// Delete removes the state file. Missing is not an error.
func (m *Manager) Delete() error {
	if err := os.Remove(m.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: delete state: %w", err)
	}
	return nil
}

// Verify checks a presented token against the state, in the approved
// order: caller match, constant-time hash match, expiry, unused.
// Failed caller/hash checks increment the persisted attempt counter;
// the attempt that reaches MaxAttempts burns the token (state file
// deleted, ErrBurned). An expired token is terminal: the state file
// is deleted and ErrExpired is returned. A used token returns
// ErrUsed without counting an attempt (it is already dead).
// The token bytes are the caller's to zero; Verify never logs them.
func (m *Manager) Verify(caller, verb string, token []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := Load(m.path)
	if err != nil {
		return err
	}
	fail := func() error {
		st.Attempts++
		if st.Attempts >= MaxAttempts {
			_ = os.Remove(m.path)
			return ErrBurned
		}
		_ = writeState(m.path, st)
		return errors.New("bootstrap: token verification failed")
	}
	// Root may present any binding: this mirrors the normal API's
	// "uid 0 implicit secret" rule. It is not a bypass — the token
	// hash, verb, expiry, and single-use checks below still apply,
	// and root can already mint its own token via the helper. Without
	// it, the documented `sudo skein system init` flow is broken:
	// the helper binds the token to SUDO_UID (the invoking user),
	// but the sudo'd CLI posts as uid 0 — caught by the M9 §2b
	// packaged-install test. A non-root caller must still match the
	// binding exactly.
	if caller != st.Caller && caller != "uid:0" {
		return fail()
	}
	if verb != st.Verb {
		return fail()
	}
	// Constant-time hash comparison: both sides are hex SHA-256 of
	// the same length, so a length mismatch also fails closed here.
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(st.TokenHash)) != 1 {
		return fail()
	}
	if time.Now().After(st.ExpiresAt) {
		_ = os.Remove(m.path)
		return ErrExpired
	}
	if st.Used {
		return ErrUsed
	}
	return nil
}

// MarkUsed atomically marks the token used, BEFORE any config work, so
// a crashed bootstrap cannot be replayed. The caller must delete the
// state file after a successful bootstrap; a failed bootstrap leaves
// the used state in place, which is what blocks the replay.
func (m *Manager) MarkUsed() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := Load(m.path)
	if err != nil {
		return err
	}
	if st.Used {
		return ErrUsed
	}
	st.Used = true
	return writeState(m.path, st)
}
