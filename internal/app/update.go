package app

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Utsav-X-bit/skein/internal/service"
)

// updateReleaseKey is the ed25519 signing key for daemon update manifests
// (seed: sha512("skein daemon update release key v1"), 64 bytes, held by the
// release pipeline).
var updateReleaseKey = mustDecodeKey("VcDhzP6paP1T24kvn5cxUp/Q3gCSNAOx+aD3cKJ+e2k=")

func mustDecodeKey(b64 string) ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("app: bad update release key")
	}
	return ed25519.PublicKey(key)
}

// UpdateManifest describes one staged daemon binary. SHA256 is lowercase hex;
// Signature is base64 ed25519 over "version\nbinary\nsha256".
type UpdateManifest struct {
	Version   string `json:"version"`
	Binary    string `json:"binary"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

// SignedPayload is the exact byte string the signature covers.
func (m UpdateManifest) SignedPayload() string {
	return m.Version + "\n" + m.Binary + "\n" + m.SHA256
}

// UpdateResult reports what update-daemon did.
type UpdateResult struct {
	Version    string `json:"version"`
	Checked    bool   `json:"checked"`
	Updated    bool   `json:"updated"`
	RolledBack bool   `json:"rolled_back"`
}

// UpdateRoot verifies the manifest's digest and signature, then — only with
// consent — stops the service, swaps the binary (keeping a .old backup),
// starts it, health-checks, and rolls back on failure.
func UpdateRoot(manifestPath string, consent bool) (UpdateResult, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return UpdateResult{}, err
	}
	var m UpdateManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return UpdateResult{}, fmt.Errorf("update: manifest: %w", err)
	}
	if m.Version == "" || m.Binary == "" || m.SHA256 == "" || m.Signature == "" {
		return UpdateResult{}, errors.New("update: manifest needs version, binary, sha256, and signature")
	}
	if info, err := os.Stat(m.Binary); err != nil {
		return UpdateResult{}, err
	} else if !info.Mode().IsRegular() {
		return UpdateResult{}, fmt.Errorf("update: %s is not a regular file", m.Binary)
	}
	f, err := os.Open(m.Binary)
	if err != nil {
		return UpdateResult{}, err
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		f.Close()
		return UpdateResult{}, err
	}
	f.Close()
	if hex.EncodeToString(sum.Sum(nil)) != strings.ToLower(m.SHA256) {
		return UpdateResult{}, fmt.Errorf("update: binary digest is %s, manifest says %s", hex.EncodeToString(sum.Sum(nil)), m.SHA256)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("update: signature: %w", err)
	}
	if !ed25519.Verify(updateReleaseKey, []byte(m.SignedPayload()), sig) {
		return UpdateResult{}, errors.New("update: signature does not match the release key")
	}
	if !consent {
		return UpdateResult{Version: m.Version, Checked: true}, nil
	}

	exe, _, installed, err := service.Installed()
	if err != nil {
		return UpdateResult{}, err
	}
	if !installed {
		return UpdateResult{}, errors.New("update: service is not installed")
	}
	old := exe + ".old"
	if err := copyFile(exe, old); err != nil {
		return UpdateResult{}, fmt.Errorf("update: back up %s: %w", exe, err)
	}
	if err := service.Stop(); err != nil {
		log.Printf("skein-helper: update: stop: %v", err)
	}
	rollback := func() error {
		if err := service.Stop(); err != nil {
			log.Printf("skein-helper: update: rollback stop: %v", err)
		}
		if err := copyFile(old, exe); err != nil {
			return fmt.Errorf("update: rollback: %w", err)
		}
		_ = os.Remove(old)
		if err := service.Start(); err != nil {
			return fmt.Errorf("update: rolled back but service failed to start: %w", err)
		}
		return nil
	}
	if err := copyFile(m.Binary, exe); err != nil {
		rollback()
		return UpdateResult{Version: m.Version, Checked: true, RolledBack: true},
			fmt.Errorf("update: swap: %w", err)
	}
	if err := service.Start(); err != nil {
		rollback()
		return UpdateResult{Version: m.Version, Checked: true, RolledBack: true},
			fmt.Errorf("update: start: %w", err)
	}
	if err := serviceRunning(15 * time.Second); err != nil {
		rollback()
		return UpdateResult{Version: m.Version, Checked: true, RolledBack: true},
			fmt.Errorf("update: health check: %w (rolled back)", err)
	}
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("skein-helper: update: remove backup: %v", err)
	}
	return UpdateResult{Version: m.Version, Checked: true, Updated: true}, nil
}

// serviceRunning polls the unprivileged service state until it is running
// or the deadline passes.
func serviceRunning(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := service.ReadStatus()
		if err != nil {
			return err
		}
		if st.State == service.StateRunning {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service still %s after %s", st.State, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// copyFile copies src to dst, executable on Unix.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".swap-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}
