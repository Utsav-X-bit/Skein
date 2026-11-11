package app

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// M9 §2c: "reset, then skein system init" failed with "a bootstrap is
// already in progress". A mint whose token was never handed to a caller
// (service register/start or control-plane wait failed) left its state
// behind, so every retry was refused for the full 10-minute TTL and the
// state file is root/admin-owned, leaving no way out. A mint that fails
// before returning its token must clear the state so the next elevation
// can mint immediately.
func TestMintBootstrapClearsStateWhenDeliveryFails(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	deliverErr := errors.New("service start: access is denied")
	if _, err := mintBootstrap("init", cfgPath, keyPath, statePath, func(string) error {
		return deliverErr
	}); !errors.Is(err, deliverErr) {
		t.Fatalf("failed delivery must fail the mint with its own error, got %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("a mint whose delivery failed must not leave a state file (it wedges the next mint)")
	}
	// The next elevation must succeed without waiting out a TTL.
	if _, err := mintBootstrap("join", cfgPath, keyPath, statePath, func(string) error { return nil }); err != nil {
		t.Fatalf("retry after a failed delivery: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("delivered token lost its state file: %v", err)
	}
}

// The cancel is only safe under the config lock: a running daemon holds
// that lock for its lifetime, so a failed acquisition must leave the
// state alone rather than cancel a live daemon's token. With the lock
// free, only the token named by the caller may be removed — a token that
// replaced it after the mint must survive.
func TestCancelBootstrapTokenIsLockedAndTokenBound(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	ours, err := mintBootstrapToken("init", cfgPath, keyPath, statePath)
	if err != nil {
		t.Fatal(err)
	}

	// A daemon (or another elevation) owns the config lock.
	holder, err := configstore.Open(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Lock(); err != nil {
		t.Fatal(err)
	}
	cancelBootstrapToken(cfgPath, statePath, ours)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatal("cancel must fail closed while another holder owns the config lock")
	}
	if err := holder.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}

	// A foreign token never cancels ours, even with the lock free.
	foreign := base64.RawURLEncoding.EncodeToString(make([]byte, bootstrap.TokenSize))
	cancelBootstrapToken(cfgPath, statePath, foreign)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatal("a foreign token must not cancel the minted token")
	}

	// Our own token, lock free, is removed.
	cancelBootstrapToken(cfgPath, statePath, ours)
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("cancel must remove the state of the token it names")
	}
}

// mintBootstrapToken is the helper's mint core without the service
// install/start or root-ownership steps, so it is testable against
// temp paths. The system-path production wrapper (MintBootstrap)
// needs a real install and is covered by M8, not here.
func TestMintBootstrapTokenRefusesExistingConfig(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	if err := os.WriteFile(cfgPath, []byte("# existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mintBootstrapToken("init", cfgPath, keyPath, statePath); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("mint with existing config must fail with already-exists, got %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("refused mint must not leave a state file")
	}
}

// M9 §2b: fresh-system bootstrap failed with ENOENT on the lock path
// because mintBootstrapToken opened the configstore before the system
// config directory existed. Minting must create it.
func TestMintBootstrapTokenCreatesMissingConfigDir(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := filepath.Join(t.TempDir(), "missing", "skein")
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	if _, err := mintBootstrapToken("init", cfgPath, keyPath, statePath); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("config dir missing after mint: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing after mint: %v", err)
	}
}

func TestMintBootstrapTokenHappyAndSecondMintRefused(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	token, err := mintBootstrapToken("join", cfgPath, keyPath, statePath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != bootstrap.TokenSize {
		t.Fatalf("token decodes to %d bytes: %v", len(raw), err)
	}
	clear(raw)
	if _, err := mintBootstrapToken("init", cfgPath, keyPath, statePath); err == nil {
		t.Fatal("second mint while a valid token exists must fail")
	}
	if _, err := mintBootstrapToken("bogus", cfgPath, keyPath, statePath); err == nil {
		t.Fatal("unknown verb must fail")
	}
}

// reset --keep-identity leaves identity.key in place. The helper must
// still be able to mint a token: the daemon reuses the preserved key
// for init/join/import rather than generating a new identity.
func TestMintBootstrapTokenAllowsPreservedIdentity(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, id.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mintBootstrapToken("init", cfgPath, keyPath, statePath); err != nil {
		t.Fatalf("mint with preserved identity: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing after mint: %v", err)
	}
}

// A malformed preserved key must fail the mint before any elevation,
// instead of being discovered by the daemon after the token is spent.
func TestMintBootstrapTokenRejectsInvalidPreservedIdentity(t *testing.T) {
	t.Setenv("PKEXEC_UID", "1000")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	statePath := filepath.Join(dir, "bootstrap.json")
	if err := os.WriteFile(keyPath, []byte("not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := mintBootstrapToken("init", cfgPath, keyPath, statePath)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mint with invalid identity = %v, want an identity error", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("refused mint must not leave a state file")
	}
}
