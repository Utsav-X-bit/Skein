//go:build !windows

package daemon

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// readManifestFile opens the update manifest once with
// O_NOFOLLOW and reads it via the fd after an fstat check
// (regular file), closing the symlink/TOCTOU gap in the old
// helper (which Stat-ed the binary path, then re-opened it).
// A symlink manifest is refused (F-05), not followed.
func readManifestFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("update: manifest %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("update: manifest %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("update: manifest %s is not a regular file", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("update: manifest %s: %w", path, err)
	}
	return raw, nil
}
