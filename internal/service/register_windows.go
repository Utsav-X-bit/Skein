//go:build windows

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkParentWritable (Windows): fail-closed conservative rule.
// A real parent-ACL check (admin-only) still needs verification on
// a Windows VM (plan §6.1) — until then, only %ProgramFiles% (and
// %ProgramFiles(x86)%), which are admin-only by default, are
// accepted as daemon parents. An explicit path elsewhere is
// rejected rather than trusted on an unchecked ACL heuristic.
// This covers the package search (%ProgramFiles%\Skein\skeind.exe)
// and rejects user-profile / helper-directory parents outright.
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
	// symlinks in ValidateDaemonPath; the ProgramFiles rule below
	// is the parent gate on Windows (a parent outside the package
	// directory cannot yield a service binary at all).
	for _, pf := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if pf == "" {
			continue
		}
		pf = filepath.Clean(pf)
		if strings.EqualFold(dir, pf) || strings.HasPrefix(strings.ToLower(dir), strings.ToLower(pf)+`\`) {
			return nil
		}
	}
	return fmt.Errorf("service: daemon parent %q is outside %%ProgramFiles%%; Windows parent-ACL verification is still pending on a real Windows VM, so non-package parents are rejected", dir)
}
