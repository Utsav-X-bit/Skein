//go:build !windows

package app

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// GrantUserRoot persists uid in the access file and applies the socket ACL
// now; the daemon re-applies it from the access file at listener creation.
func GrantUserRoot(uid int) error {
	if uid <= 0 {
		return fmt.Errorf("grant-user: uid must be positive, got %d", uid)
	}
	data, _ := os.ReadFile(AccessPath)
	uids := map[int]struct{}{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return fmt.Errorf("grant-user: %s has a malformed line %q", AccessPath, line)
		}
		uids[n] = struct{}{}
	}
	uids[uid] = struct{}{}
	sorted := make([]int, 0, len(uids))
	for u := range uids {
		sorted = append(sorted, u)
	}
	sort.Ints(sorted)
	var b strings.Builder
	for _, u := range sorted {
		fmt.Fprintf(&b, "%d\n", u)
	}
	if err := writeRootBytes(AccessPath, []byte(b.String())); err != nil {
		return err
	}
	if _, err := os.Stat(ControlSocket); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, err := execOutput("setfacl", "-m", fmt.Sprintf("u:%d:rw", uid), ControlSocket); err != nil {
		return fmt.Errorf("grant-user: socket ACL: %w (install the acl package if setfacl is missing)", err)
	}
	return nil
}

// SetOwnerRoot is the Windows DACL verb; on Unix socket access is a per-user
// ACL, applied by GrantUserRoot.
func SetOwnerRoot(string) error {
	return fmt.Errorf("set-owner is a Windows verb; on Unix use grant-user")
}
