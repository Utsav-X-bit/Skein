//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// loadImportSource loads a user-mode config + sibling identity.key for
// bootstrap import, closing the symlink/TOCTOU gap in the legacy
// helper import path (which Lstat-validates and then re-opens by path): here
// each file is opened once with O_NOFOLLOW and read via the resulting
// fd after an fstat check (regular file, owner-only mode). The parent
// chain is also checked for world-writable directories, as in
// validateSourceFile. A symlink anywhere in the two paths is refused.
func loadImportSource(source string) (conf.Config, identity.Identity, error) {
	cfgText, err := readImportFile(source)
	if err != nil {
		return conf.Config{}, identity.Identity{}, err
	}
	cfg, err := conf.Parse(string(cfgText))
	if err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: %w", err)
	}
	keyPath := filepath.Join(filepath.Dir(source), "identity.key")
	keyRaw, err := readImportFile(keyPath)
	if err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: identity: %w", err)
	}
	id, err := identity.FromPrivateKey(keyRaw)
	if err != nil {
		return conf.Config{}, identity.Identity{}, fmt.Errorf("bootstrap import: identity: %w", err)
	}
	return cfg, id, nil
}

func readImportFile(path string) ([]byte, error) {
	if err := checkImportParents(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("bootstrap import: open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("bootstrap import: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("bootstrap import: %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("bootstrap import: %s is accessible by group or others (mode %o); chmod 600 first", path, info.Mode().Perm())
	}
	data := make([]byte, info.Size())
	if _, err := readFullImport(f, data); err != nil {
		return nil, fmt.Errorf("bootstrap import: read %s: %w", path, err)
	}
	return data, nil
}

func readFullImport(f *os.File, data []byte) (int, error) {
	n := 0
	for n < len(data) {
		m, err := f.Read(data[n:])
		n += m
		if err != nil {
			return n, err
		}
		if m == 0 {
			break
		}
	}
	return n, nil
}

// checkImportParents rejects a parent chain in which someone other
// than the owner could replace the source: group/other WRITE
// permission. This deliberately differs from the helper's
// validateSourceFile, which rejects any group/other access (0o077,
// including read/execute) and therefore refuses sources under any
// 0755 directory — and from a naive write check, which would refuse
// /tmp itself. A sticky directory (mode 1777, e.g. /tmp) is exempt:
// in a sticky directory only the file's owner can replace it, so
// replacement by another user is not possible. The source files
// themselves keep the strict owner-only check in readImportFile:
// a readable secret is a leak even when it cannot be replaced.
func checkImportParents(path string) error {
	dir := filepath.Dir(path)
	for {
		di, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("bootstrap import: parent %s: %w", dir, err)
		}
		if di.IsDir() && di.Mode().Perm()&0o022 != 0 && di.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("bootstrap import: parent directory %s is writable by group or others", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
