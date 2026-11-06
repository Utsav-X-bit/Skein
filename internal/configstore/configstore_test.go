package configstore

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
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
	// Windows mode bits are synthesised (always 0666): assert the
	// DACL instead — configstore already calls fileperm.Restrict.
	if runtime.GOOS == "windows" {
		ok, err := fileperm.NoWorldAccess(path)
		if err != nil || !ok {
			t.Fatalf("config world access: ok=%v err=%v", ok, err)
		}
	} else if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
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

// lockHeldSignal is printed by TestConfigstoreHelperProcess only after it
// holds the lock. The parent reads it as the handshake.
const lockHeldSignal = "configstore-lock-held"

// TestConfigstoreHelperProcess is the cross-process lock holder for
// TestLockHeldAcrossProcesses. It is never run as a normal test: it
// exits immediately unless SKEIN_CONFIGSTORE_HOLDER is set. Once the
// lock is held it prints one line to stdout, which the parent reads as
// the handshake (no file polling and no arbitrary deadline), then
// blocks until the parent kills it; the OS releases the lock.
func TestConfigstoreHelperProcess(t *testing.T) {
	dir := os.Getenv("SKEIN_CONFIGSTORE_HOLDER")
	if dir == "" {
		return
	}
	s, err := Open(filepath.Join(dir, "skein.toml"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Lock(); err != nil {
		t.Fatalf("holder Lock: %v", err)
	}
	fmt.Println(lockHeldSignal)
	select {} // killed by the parent test; the OS releases the lock
}

// waitForLockSignal reads the holder's stdout until the lock-held line.
// It returns io.EOF if the holder exits before printing it, so a holder
// that dies early fails the test with its stderr instead of being
// polled until an arbitrary deadline.
func waitForLockSignal(r io.Reader) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == lockHeldSignal {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
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
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigstoreHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "SKEIN_CONFIGSTORE_HOLDER="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	// The holder prints the signal only after flock succeeds, so reading it
	// is the lock handshake. EOF means the holder exited before taking the
	// lock; its stderr says why.
	if err := waitForLockSignal(stdout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("holder did not take the lock: %v (stderr: %s)", err, stderr.String())
	}

	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Write(validConfig("intruder")); !IsLocked(err) {
		t.Fatalf("cross-process Write under held lock = %v, want ErrLocked", err)
	}

	// Kill and reap the holder: the OS releases the lock as the process
	// dies, so the next write succeeds without polling or stale-lock
	// cleanup.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Logf("holder wait: %v (ignored)", err)
	}
	if err := other.Write(validConfig("after")); err != nil {
		t.Fatalf("Write after holder death = %v, want nil (OS must release the lock)", err)
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
