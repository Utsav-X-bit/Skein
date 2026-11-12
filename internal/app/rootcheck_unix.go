//go:build !windows && !android

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkSystemOwnership rejects a system config not owned by root: the
// daemon runs as root, so a user-planted file in the system directory
// would otherwise be trusted. User-mode configs (dev CLI) are exempt.
func checkSystemOwnership(configPath string) error {
	if filepath.Dir(configPath) != systemDir || os.Geteuid() != 0 {
		return nil
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if st.Uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not root; the daemon runs as root", configPath, st.Uid)
	}
	return nil
}
