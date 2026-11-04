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
// An existing role for uid is preserved; a new uid is written bare, which
// the API reads as RoleRead (Step 1 least-privilege default for legacy
// lines). Use GrantUserRoleRoot for an explicit role.
func GrantUserRoot(uid int) error {
	return grantUserWithRole(uid, "")
}

// GrantUserRoleRoot is GrantUserRoot with an explicit role (read, mutate,
// or secret). Other lines keep their roles; the file is sorted by uid.
func GrantUserRoleRoot(uid int, role string) error {
	switch role {
	case "read", "mutate", "secret":
	default:
		return fmt.Errorf("grant-user: unknown role %q (want read, mutate, or secret)", role)
	}
	return grantUserWithRole(uid, role)
}

func grantUserWithRole(uid int, role string) error {
	if uid <= 0 {
		return fmt.Errorf("grant-user: uid must be positive, got %d", uid)
	}
	data, _ := os.ReadFile(AccessPath)
	roles := map[int]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idPart, rolePart := line, ""
		if i := strings.LastIndex(line, ":"); i >= 0 {
			idPart, rolePart = line[:i], line[i+1:]
			switch rolePart {
			case "read", "mutate", "secret":
			default:
				return fmt.Errorf("grant-user: %s has a malformed line %q", AccessPath, line)
			}
		}
		n, err := strconv.Atoi(idPart)
		if err != nil {
			return fmt.Errorf("grant-user: %s has a malformed line %q", AccessPath, line)
		}
		roles[n] = rolePart
	}
	// Explicit role always wins. GrantUserRoot (role == "") on a new uid
	// writes bare (read by API interpretation); on an existing uid it
	// preserves the stored role, so re-granting never silently downgrades
	// or upgrades anyone.
	if role != "" {
		roles[uid] = role
	} else if _, ok := roles[uid]; !ok {
		roles[uid] = ""
	}
	sorted := make([]int, 0, len(roles))
	for u := range roles {
		sorted = append(sorted, u)
	}
	sort.Ints(sorted)
	var b strings.Builder
	for _, u := range sorted {
		if roles[u] == "" {
			fmt.Fprintf(&b, "%d\n", u)
		} else {
			fmt.Fprintf(&b, "%d:%s\n", u, roles[u])
		}
	}
	if err := writeAtomicBytes(AccessPath, []byte(b.String())); err != nil {
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

// grantBootstrapUser grants the user that invoked pkexec/sudo access to the
// service control socket.
func grantBootstrapUser() error {
	for _, key := range []string{"PKEXEC_UID", "SUDO_UID"} {
		if raw := os.Getenv(key); raw != "" {
			uid, err := strconv.Atoi(raw)
			if err == nil && uid > 0 {
				return GrantUserRoot(uid)
			}
		}
	}
	return nil
}
