//go:build unix

package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// checkParentWritable (Unix): the daemon binary's parent directory
// must not be group- or world-writable, or anyone in that group
// could replace the service binary between validation and start.
func checkParentWritable(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("service: daemon parent %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("service: daemon parent %q is not a directory", dir)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("service: daemon parent %q is writable by group/other (mode %o); a planted binary could replace the service binary", dir, info.Mode().Perm())
	}
	return nil
}
