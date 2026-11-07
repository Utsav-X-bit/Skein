package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

func TestPublicReadsTheDaemonIdentity(t *testing.T) {
	want := api.Identity{ID: "0123456789abcdef", PublicKey: "pub-key", Name: "box"}
	var gotPath string
	stubControl(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(want)
	})
	got, err := (&IdentityService{}).Public(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/identity" {
		t.Fatalf("path = %s", gotPath)
	}
	if got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
}

// GenerateOffline writes a fresh key into a directory the user owns and
// returns only its public half.
func TestGenerateOfflineWritesOwnerOnlyKey(t *testing.T) {
	dir := t.TempDir()
	got, err := (&IdentityService{}).GenerateOffline(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "identity.key")
	if got.IdentityPath != path {
		t.Fatalf("identity path = %s, want %s", got.IdentityPath, path)
	}
	id, err := identity.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("identity mode = %o, want 600", info.Mode().Perm())
	}
	if got.PeerID != fmt.Sprintf("%016x", id.ID) || got.PublicKey != base64.StdEncoding.EncodeToString(id.PublicKey) {
		t.Fatalf("public projection = %+v", got)
	}
	if len(got.PeerID) != 16 || strings.ToLower(got.PeerID) != got.PeerID {
		t.Fatalf("peer id = %q, want 16 lowercase hex digits", got.PeerID)
	}
	if strings.Contains(got.PublicKey, base64.StdEncoding.EncodeToString(id.PrivateKey)) {
		t.Fatal("private key leaked into the public projection")
	}
	// No config is written: offline generation owns the identity only.
	if _, err := os.Stat(filepath.Join(dir, "skein.toml")); !os.IsNotExist(err) {
		t.Fatalf("offline generation wrote a config: %v", err)
	}
}

// An existing key is reused as-is, so re-running onboarding never rotates an
// identity the user already shared.
func TestGenerateOfflineReusesExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.key")
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Save(path, id); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := (&IdentityService{}).GenerateOffline(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%016x", id.ID); got.PeerID != want {
		t.Fatalf("peer id = %s, want %s", got.PeerID, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("existing key was rewritten")
	}
}

// A directory that already holds a config is a live install; offline
// generation must refuse rather than plant a second identity beside it.
func TestGenerateOfflineRefusesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "skein.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (&IdentityService{}).GenerateOffline(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "identity.key")); !os.IsNotExist(statErr) {
		t.Fatalf("refused generation wrote a key: %v", statErr)
	}
}

// The GUI is unprivileged: a system config directory (or anything below it)
// is refused before any write, even when it happens to be writable.
func TestGenerateOfflineRefusesSystemPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix system paths")
	}
	for _, dir := range []string{"/etc/skein", "/usr", "/usr/local/share"} {
		if _, err := (&IdentityService{}).GenerateOffline(context.Background(), dir); err == nil {
			t.Fatalf("%s was accepted", dir)
		} else if !strings.Contains(err.Error(), "system config directory") {
			t.Fatalf("%s err = %v, want a system-path refusal", dir, err)
		}
	}
}

// A symlink in a user directory must not become a write into a system path.
func TestGenerateOfflineResolvesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlinks")
	}
	home := t.TempDir()
	link := filepath.Join(home, "skein")
	if err := os.Symlink("/usr", link); err != nil {
		t.Fatal(err)
	}
	_, err := (&IdentityService{}).GenerateOffline(context.Background(), link)
	if err == nil || !strings.Contains(err.Error(), "system config directory") {
		t.Fatalf("symlink err = %v", err)
	}
}

func TestGenerateOfflineRefusesRelativePath(t *testing.T) {
	if _, err := (&IdentityService{}).GenerateOffline(context.Background(), "skein-offline"); err == nil {
		t.Fatal("relative path accepted")
	}
}

// A running daemon holds the config lock; offline generation fails with the
// documented ErrLocked instead of waiting or writing under it.
func TestGenerateOfflineReportsLockedConfig(t *testing.T) {
	dir := t.TempDir()
	store, err := configstore.Open(filepath.Join(dir, "skein.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	_, err = (&IdentityService{}).GenerateOffline(context.Background(), dir)
	if !errors.Is(err, configstore.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "identity.key")); !os.IsNotExist(statErr) {
		t.Fatalf("locked generation wrote a key: %v", statErr)
	}
}
