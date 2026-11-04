package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

func mintForTest(t *testing.T, dir, caller, verb string) (string, []byte) {
	t.Helper()
	// Mint returns raw bytes; tests encode like the helper does.
	raw, err := Mint(filepath.Join(dir, StateFileName), caller, verb)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return filepath.Join(dir, StateFileName), raw
}

func TestMintStateFileIsHashOnly(t *testing.T) {
	dir := t.TempDir()
	path, token := mintForTest(t, dir, "uid:1000", VerbInit)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), string(token)) {
		t.Fatal("state file contains the plaintext token")
	}
	sum := sha256.Sum256(token)
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.TokenHash != hex.EncodeToString(sum[:]) {
		t.Fatal("state file does not carry the token's SHA-256 hash")
	}
	if st.Caller != "uid:1000" || st.Verb != VerbInit || st.Used || st.Attempts != 0 {
		t.Fatalf("unexpected state: %+v", st)
	}
	if d := time.Until(st.ExpiresAt); d < 9*time.Minute || d > TTL {
		t.Fatalf("expiry is not ~10 minutes: %v from now", d)
	}
	// Windows mode bits are synthesised (always 0666): assert the
	// DACL instead — no well-known non-administrator may access it.
	if runtime.GOOS == "windows" {
		ok, err := fileperm.NoWorldAccess(path)
		if err != nil || !ok {
			t.Fatalf("state file world access: ok=%v err=%v", ok, err)
		}
	} else if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %o, want 0600", info.Mode().Perm())
	}
	clear(token)
}

func TestMintRefusesSecondValidToken(t *testing.T) {
	dir := t.TempDir()
	if _, err := Mint(filepath.Join(dir, StateFileName), "uid:1000", VerbInit); err != nil {
		t.Fatal(err)
	}
	if _, err := Mint(filepath.Join(dir, StateFileName), "uid:1000", VerbJoin); err == nil {
		t.Fatal("second mint while a valid token exists must fail")
	}
}

func TestVerifyHappyAndRejectionMatrix(t *testing.T) {
	dir := t.TempDir()
	path, token := mintForTest(t, dir, "uid:1000", VerbInit)
	defer clear(token)
	mgr := NewManager(path)

	if err := mgr.Verify("uid:1000", VerbInit, token); err != nil {
		t.Fatalf("correct token must verify: %v", err)
	}
	if err := mgr.Verify("uid:1001", VerbInit, token); err == nil {
		t.Fatal("wrong uid must fail")
	}
	if err := mgr.Verify("uid:1000", VerbJoin, token); err == nil {
		t.Fatal("wrong verb must fail")
	}
	wrong := make([]byte, len(token))
	copy(wrong, token)
	wrong[0] ^= 0xff
	if err := mgr.Verify("uid:1000", VerbInit, wrong); err == nil {
		t.Fatal("wrong token must fail")
	}
	st, _ := mgr.Load()
	if st.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3 after three failures", st.Attempts)
	}
}

func TestVerifyBurnsOnFifthFailure(t *testing.T) {
	dir := t.TempDir()
	path, token := mintForTest(t, dir, "uid:1000", VerbInit)
	defer clear(token)
	mgr := NewManager(path)
	wrong := make([]byte, len(token))
	copy(wrong, token)
	wrong[0] ^= 0xff
	for i := 0; i < MaxAttempts; i++ {
		err := mgr.Verify("uid:1000", VerbInit, wrong)
		if i < MaxAttempts-1 && err == nil {
			t.Fatalf("attempt %d must fail", i+1)
		}
		if i == MaxAttempts-1 && err != ErrBurned {
			t.Fatalf("5th attempt must burn, got %v", err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("burned token's state file must be deleted")
	}
	if err := mgr.Verify("uid:1000", VerbInit, token); err == nil {
		t.Fatal("correct token after burn must fail")
	}
}

func TestVerifyExpiredAndUsed(t *testing.T) {
	dir := t.TempDir()
	path, token := mintForTest(t, dir, "uid:1000", VerbInit)
	defer clear(token)
	mgr := NewManager(path)
	// Expire the state by rewriting it with a past expiry.
	st, _ := mgr.Load()
	st.ExpiresAt = time.Now().Add(-time.Minute)
	if err := writeState(path, st); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Verify("uid:1000", VerbInit, token); err != ErrExpired {
		t.Fatalf("expired token must return ErrExpired, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expired token's state file must be deleted")
	}

	// Used token: mint fresh in a new directory, mark used, verify
	// must refuse.
	sub := t.TempDir()
	path3, token3 := mintForTest(t, sub, "uid:1000", VerbInit)
	defer clear(token3)
	mgr3 := NewManager(path3)
	if err := mgr3.MarkUsed(); err != nil {
		t.Fatal(err)
	}
	if err := mgr3.Verify("uid:1000", VerbInit, token3); err != ErrUsed {
		t.Fatalf("used token must return ErrUsed, got %v", err)
	}
	if err := mgr3.MarkUsed(); err != ErrUsed {
		t.Fatalf("second MarkUsed must return ErrUsed, got %v", err)
	}
}

func TestGrantSecretPreservesOtherGrants(t *testing.T) {
	dir := t.TempDir()
	access := filepath.Join(dir, "access")
	if err := os.WriteFile(access, []byte("1001:read\n1002:mutate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := GrantSecret(access, "uid:1000"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(access)
	if string(raw) != "1000:secret\n1001:read\n1002:mutate\n" {
		t.Fatalf("access file = %q", raw)
	}
	if err := GrantSecret(access, "bearer"); err == nil {
		t.Fatal("granting secret to a bearer caller must fail")
	}
}
