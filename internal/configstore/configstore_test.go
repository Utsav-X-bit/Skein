package configstore

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

func validConfig(name string) conf.Config {
	cfg := conf.Default()
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.API.Token = make([]byte, 32)
	cfg.Node.Name = name
	return cfg
}

func TestWriteRoundTripsAndRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Write(validConfig("first")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := conf.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Node.Name != "first" {
		t.Fatalf("name = %q, want first", got.Node.Name)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v, want 0600", info.Mode(), err)
	}

	// An invalid write must leave the old config untouched, and must not
	// leave a temp file behind.
	bad := validConfig("bad")
	bad.Node.MTU = 1
	if err := s.Write(bad); err == nil {
		t.Fatal("invalid Write = nil, want error")
	}
	got, err = conf.Load(path)
	if err != nil {
		t.Fatalf("Load after failed write: %v", err)
	}
	if got.Node.Name != "first" {
		t.Fatalf("after failed write name = %q, want first (old config intact)", got.Node.Name)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	// Read under the store agrees with Load.
	got, err = s.Read()
	if err != nil || got.Node.Name != "first" {
		t.Fatalf("Read = %q, err = %v, want first", got.Node.Name, err)
	}
}

func TestConcurrentGoroutineWritersProduceOneValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write(validConfig("seed")); err != nil {
		t.Fatal(err)
	}

	const writers = 16
	names := make(map[string]bool, writers)
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		name := fmt.Sprintf("writer-%02d", i)
		names[name] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Write(validConfig(name))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Write: %v", err)
		}
	}
	got, err := conf.Load(path)
	if err != nil {
		t.Fatalf("final config does not parse (torn write): %v", err)
	}
	if !names[got.Node.Name] {
		t.Fatalf("final name = %q, not one of the writers' names (interleaved write)", got.Node.Name)
	}
}

func TestLockHeldBlocksSecondWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	holder, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := holder.Write(validConfig("held")); err != nil {
		t.Fatal(err)
	}
	if err := holder.Lock(); err != nil {
		t.Fatalf("holder Lock: %v", err)
	}

	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Lock(); !IsLocked(err) {
		t.Fatalf("second Lock = %v, want ErrLocked", err)
	}
	if err := other.Write(validConfig("intruder")); !IsLocked(err) {
		t.Fatalf("Write under held lock = %v, want ErrLocked", err)
	}
	got, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Name != "held" {
		t.Fatalf("name = %q, want held (second writer must never overwrite)", got.Node.Name)
	}

	// Lock is idempotent on the holder and released on Close.
	if err := holder.Lock(); err != nil {
		t.Fatalf("holder re-Lock: %v", err)
	}
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Lock(); err != nil {
		t.Fatalf("Lock after holder Close: %v (lock must release on close)", err)
	}
}

// TestConfigstoreHelperProcess is the cross-process lock holder for
// TestLockHeldAcrossProcesses. It is never run as a normal test: it
// exits immediately unless SKEIN_CONFIGSTORE_HOLDER is set.
func TestConfigstoreHelperProcess(t *testing.T) {
	dir := os.Getenv("SKEIN_CONFIGSTORE_HOLDER")
	if dir == "" {
		return
	}
	path := filepath.Join(dir, "skein.toml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Lock(); err != nil {
		t.Fatalf("holder Lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {} // killed by the parent test; the OS releases the lock
}

func TestLockHeldAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(validConfig("seed")); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestConfigstoreHelperProcess", "-test.count=1")
	cmd.Env = append(os.Environ(), "SKEIN_CONFIGSTORE_HOLDER="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("holder process did not acquire the lock in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Write(validConfig("intruder")); !IsLocked(err) {
		t.Fatalf("cross-process Write under held lock = %v, want ErrLocked", err)
	}

	// Kill the holder: the OS must release the lock, so a new writer
	// succeeds without any stale-lock cleanup.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Logf("holder wait: %v (ignored)", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		if err := other.Write(validConfig("after")); err == nil {
			break
		} else if !IsLocked(err) {
			t.Fatalf("Write after holder death: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("lock not released after holder death")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, err := conf.Load(path)
	if err != nil || got.Node.Name != "after" {
		t.Fatalf("final name = %q, err = %v, want after", got.Node.Name, err)
	}
}

func TestWriteIdentityRefusesOverwriteAndWriteRawReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIdentity(keyPath, id); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	other, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIdentity(keyPath, other); err == nil {
		t.Fatal("second WriteIdentity = nil, want refuse-to-overwrite error")
	}
	loaded, err := identity.Load(keyPath)
	if err != nil || loaded.ID != id.ID {
		t.Fatalf("key after refused overwrite: id match = %v, err = %v", loaded.ID == id.ID, err)
	}
	// Import replaces a backed-up key: WriteRaw overwrites by design.
	if err := s.WriteRaw(keyPath, other.PrivateKey); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	loaded, err = identity.Load(keyPath)
	if err != nil || loaded.ID != other.ID {
		t.Fatalf("key after WriteRaw: id match = %v, err = %v", loaded.ID == other.ID, err)
	}
}

func TestWriteTextCreateOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cfg := validConfig("text")
	text, err := conf.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	commented := append([]byte("# generated header, preserved byte-for-byte\n"), text...)
	if err := s.WriteText(commented, true); err != nil {
		t.Fatalf("WriteText create: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != string(commented) {
		t.Fatalf("WriteText changed bytes (header lost or re-rendered)")
	}
	if err := s.WriteText(commented, true); err == nil {
		t.Fatal("second create-only WriteText = nil, want refuse-to-overwrite error")
	}
	if err := s.WriteText([]byte("not toml ["), false); err == nil {
		t.Fatal("invalid WriteText = nil, want validation error")
	}
}
