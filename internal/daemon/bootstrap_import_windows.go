//go:build windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// loadImportSource is the Windows bootstrap-import loader. Windows has
// no O_NOFOLLOW equivalent in Go's os package, so it Lstat-rejects
// reparse points (symlinks) and then reads by path — the same residual
// TOCTOU window as the legacy helper import path, documented here rather
// than hidden. A real Windows run (M3 Tier 5) must confirm whether
// the platform offers a stronger open-by-handle check before this is
// called production-ready on Windows.
func loadImportSource(source string) (conf.Config, identity.Identity, error) {
	cfgText, err := readImportFileWindows(source)
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	cfg, err := conf.Parse(string(cfgText))
	if err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: %w", err)
	}
	keyPath := filepath.Join(filepath.Dir(source), "identity.key")
	keyRaw, err := readImportFileWindows(keyPath)
	if err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: identity: %w", err)
	}
	id, err := identity.FromPrivateKey(keyRaw)
	if err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: identity: %w", err)
	}
	return cfg, id, nil
}

func readImportFileWindows(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("bootstrap import: %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("bootstrap import: %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bootstrap import: read %s: %w", path, err)
	}
	return data, nil
}
