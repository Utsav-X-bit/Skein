//go:build !windows

package identity

import "os"

// restrictFile enforces owner-only permissions on Unix.
func restrictFile(path string) error {
	return os.Chmod(path, 0o600)
}
