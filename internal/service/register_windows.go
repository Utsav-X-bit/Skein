//go:build windows

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// checkParentWritable (Windows): the parent must be admin-only.
// %ProgramFiles% (and %ProgramFiles(x86)%) are accepted directly —
// they are admin-only by default. Any other parent goes through the
// real DACL check (fileperm.DirAdminOnly): a directory whose DACL
// grants write-like access only to SYSTEM, Administrators, and
// TrustedInstaller is accepted, so packaged installs and admin-only
// staging directories work while user-profile and helper-directory
// parents are still rejected.
func checkParentWritable(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("service: daemon parent %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("service: daemon parent %q is not a directory", dir)
	}
	// Note: the file itself is already Lstat-checked for
	// symlinks in ValidateDaemonPath; the parent gate below is the
	// Windows half of that check.
	for _, pf := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if pf == "" {
			continue
		}
		pf = filepath.Clean(pf)
		if strings.EqualFold(dir, pf) || strings.HasPrefix(strings.ToLower(dir), strings.ToLower(pf)+`\`) {
			return nil
		}
	}
	ok, err := fileperm.DirAdminOnly(dir)
	if err != nil {
		return fmt.Errorf("service: daemon parent %q: ACL check failed: %w", dir, err)
	}
	if ok {
		return nil
	}
	return fmt.Errorf("service: daemon parent %q is not admin-only: non-administrators can write to it", dir)
}
