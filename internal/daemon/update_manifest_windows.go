//go:build windows

package daemon

import (
	"fmt"
	"io"
	"os"
)

// readManifestFile on Windows: Lstat rejects symlinks, then the
// file is opened and fstat-checked (regular file). Windows
// symlinks need privilege to create, and the staged-binary
// validation (service.ValidateDaemonPath) applies the same
// Lstat rule to the binary itself; the residual TOCTOU window
// between Lstat and open is accepted here and must be verified
// on the user's Windows machine (same caveat as Step 6's
// parent-ACL rule).
func readManifestFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("update: manifest %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("update: manifest %s is not a regular file (symlinks rejected)", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("update: manifest %s: %w", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("update: manifest %s: %w", path, err)
	}
	return raw, nil
}
