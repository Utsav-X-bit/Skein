// Package configstore is the single writer for Skein configuration files.
//
// ADR-001 §4: the daemon is the only config writer. Every system-config
// write in the tree goes through this package, under a cross-process lock
// held beside the config file (<config>.lock). The lock is the
// single-writer enforcement: a second process that tries to write while a
// daemon holds the lock gets ErrLocked instead of interleaving with the
// daemon's write, and every write is temp + fsync + rename, so a crash
// mid-write leaves the old config intact, never a torn one.
//
// Lock discipline:
//   - Daemon: Open the store for its config path and hold Lock() for the
//     lifetime of the process. Write then takes the fast path (no
//     re-locking: re-locking the same file from the same process is
//     denied on Linux flock and would deadlock the daemon's own persist).
//   - Helper / CLI: do not hold the lock. Each Write takes a brief lock
//     (acquire, write, release) and fails with ErrLocked if a daemon
//     holds it. A caller that needs several files written as a unit
//     (cmdInit: identity + config) may hold Lock() explicitly across
//     them; Write detects the held lock and does not re-acquire.
//   - In-process serialisation: a mutex serialises Writes on one Store,
//     so concurrent goroutines produce a last-writer-consistent file,
//     never an interleaved one.
//
// Format is unchanged: Write validates with conf.Validate (via
// conf.Render) and renders the same TOML conf.Load parses. Identity
// writes go through the same lock and the same atomic pattern, so config
// and identity are never again written by two different mechanisms.
// The access file (uid:role grants) is NOT a config file and is NOT
// written here; it stays with the grant verbs, which write it atomically
// on their own. Daemon runtime state (state.json) is likewise not config.
package configstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// ErrLocked is returned when another process holds the config lock — in
// practice, a running daemon. The message carries the remediation so API
// (423 in Step 7) and CLI callers can surface it without inventing one.
var ErrLocked = errors.New("configstore: config is locked by a running daemon; stop the daemon first (Linux: sudo systemctl stop skein, Windows: Stop-Service skein) or use the daemon API")

// IsLocked reports whether err is or wraps ErrLocked.
func IsLocked(err error) bool { return errors.Is(err, ErrLocked) }

// Store is bound to one config path for its whole life. A Store must not
// be copied after first use.
type Store struct {
	path     string
	lockPath string
	lock     *osLock

	mu     sync.Mutex // guards locked
	locked bool

	writeMu sync.Mutex // serialises Writes on this Store
}

// Open binds a store to configPath. The lock file (<config>.lock) is
// created if missing (0600); the config directory must already exist —
// callers that create a fresh config (cmdInit, helper init) create the
// directory first, so a typo'd path fails here instead of silently
// creating a directory tree. Open does not lock; call Lock for that.
func Open(configPath string) (*Store, error) {
	if configPath == "" {
		return nil, errors.New("configstore: empty config path")
	}
	clean := filepath.Clean(configPath)
	lk, err := openLock(clean + ".lock")
	if err != nil {
		return nil, fmt.Errorf("configstore: open lock %s: %w", clean+".lock", err)
	}
	return &Store{path: clean, lockPath: clean + ".lock", lock: lk}, nil
}

// Path returns the config path this store writes.
func (s *Store) Path() string { return s.path }

// LockPath returns the lock file path, for diagnostics only.
func (s *Store) LockPath() string { return s.lockPath }

// Lock acquires the cross-process lock, non-blocking. A second caller —
// same process or another — gets ErrLocked, never a silent wait and never
// a silent overwrite. Lock is idempotent on the holding Store.
func (s *Store) Lock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return nil
	}
	if err := s.lock.lock(); err != nil {
		return ErrLocked
	}
	s.locked = true
	return nil
}

// Unlock releases a lock held by this Store. Unlock on an unlocked Store
// is a no-op.
func (s *Store) Unlock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.locked {
		return nil
	}
	if err := s.lock.unlock(); err != nil {
		return err
	}
	s.locked = false
	return nil
}

// Close releases the lock (if held) and the lock file. It is safe to
// call more than once.
func (s *Store) Close() error {
	_ = s.Unlock()
	return s.lock.close()
}

// held reports whether this Store currently holds the lock.
func (s *Store) held() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked
}

// withLock runs fn with the cross-process lock held: the fast path when
// this Store already holds it (daemon lifetime lock), otherwise a brief
// acquisition that fails with ErrLocked when another process holds it.
func (s *Store) withLock(fn func() error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.held() {
		return fn()
	}
	if err := s.Lock(); err != nil {
		return err
	}
	defer func() { _ = s.Unlock() }()
	return fn()
}

// Write validates and renders cfg and writes it atomically to the
// store's path. It is the only function that writes a config file.
// cfg.Path, if set differently, is ignored: the store's path wins, so a
// caller cannot accidentally write a config to a path it did not lock.
func (s *Store) Write(cfg conf.Config) error {
	return s.withLock(func() error {
		cfg.Path = s.path
		text, err := conf.Render(cfg)
		if err != nil {
			return err
		}
		return atomicWrite(s.path, text)
	})
}

// WriteText validates text as a config (conf.Parse) and writes those
// exact bytes atomically. It exists for cmdInit, whose generated text
// carries a header comment that Render would drop; the bytes are
// validated, not trusted. With createOnly, an existing config is an
// error, not an overwrite — the check happens under the lock, so two
// racing inits cannot both pass it.
func (s *Store) WriteText(text []byte, createOnly bool) error {
	return s.withLock(func() error {
		if _, err := conf.Parse(string(text)); err != nil {
			return fmt.Errorf("configstore: invalid config text: %w", err)
		}
		if createOnly {
			if _, err := os.Stat(s.path); err == nil {
				return fmt.Errorf("configstore: %s already exists; refusing to overwrite", s.path)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		return atomicWrite(s.path, text)
	})
}

// WriteIdentity writes a private key atomically under the config lock,
// refusing to overwrite an existing file — the same no-overwrite rule
// as identity.Save, but with the temp + rename pattern instead of a
// direct create, so a crash cannot leave a half-written key.
func (s *Store) WriteIdentity(path string, id identity.Identity) error {
	return s.withLock(func() error {
		if len(id.PrivateKey) != identity.KeySize {
			return fmt.Errorf("configstore: private key must be %d bytes, got %d", identity.KeySize, len(id.PrivateKey))
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("configstore: %s already exists; refusing to overwrite", path)
		} else if !os.IsNotExist(err) {
			return err
		}
		return atomicWrite(path, id.PrivateKey)
	})
}

// WriteRaw writes raw bytes atomically under the config lock, allowing
// overwrite. It is for non-config secret files that must move with the
// config under the same lock — today, the identity file on import, where
// the old key was already backed up and replacement is the point. Config
// files must use Write or WriteText, never WriteRaw.
func (s *Store) WriteRaw(path string, data []byte) error {
	return s.withLock(func() error {
		return atomicWrite(path, data)
	})
}

// Read loads the store's config. Under a held lock it reads directly;
// otherwise it takes the brief lock, so a read that must be consistent
// with a companion file (Step 4's bootstrap transition) cannot race a
// concurrent write.
func (s *Store) Read() (conf.Config, error) {
	var cfg conf.Config
	err := s.withLock(func() error {
		var err error
		cfg, err = conf.Load(s.path)
		return err
	})
	return cfg, err
}

// atomicWrite creates a temp file beside path with O_EXCL and a random
// suffix (so a planted symlink can never redirect the write), chmods it
// 0600, writes, fsyncs, restricts permissions, and renames into place.
// On any error the temp file is removed and the old file is untouched.
// Secrets are never logged here; the caller owns its own logging.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fileperm.Restrict(name); err != nil {
		return err
	}
	return os.Rename(name, path)
}
