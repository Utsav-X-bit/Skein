package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

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
