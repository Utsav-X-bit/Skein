//go:build !windows

package fileperm

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

func restrict(path string) error {
	return os.Chmod(path, 0o600)
}

// restrictDACL is a no-op on Unix: Restrict's DACL has no Unix counterpart,
// and the caller's create mode is the access control.
func restrictDACL(string) error { return nil }

func chownToSudoUser(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	name := os.Getenv("SUDO_USER")
	if name == "" || name == "root" {
		return nil
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil
	}
	return os.Chown(path, uid, gid)
}

func preserveOwner(path, ref string) error {
	if info, err := os.Stat(ref); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			return os.Chown(path, int(st.Uid), int(st.Gid))
		}
		return nil
	}
	// The daemon runs as root without SUDO_USER. When there is no file to
	// copy ownership from, the directory's owner is the account that owns
	// the configuration, so keep it there instead of leaving a root file in
	// a user's home.
	if info, err := os.Stat(filepath.Dir(path)); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if int(st.Uid) != os.Geteuid() || int(st.Gid) != os.Getegid() {
				return os.Chown(path, int(st.Uid), int(st.Gid))
			}
			return nil
		}
	}
	return chownToSudoUser(path)
}

// root gives path to root and closes it to everyone else. Directories stay
// executable by their owner only: 0750 root:root, files 0600.
// On Android the app runs as an unprivileged UID with no root to chown
// to (chown to uid 0 fails EPERM); the files already live in the app's
// private dir owned by its own UID, so skip the chown there and only
// tighten the mode. The same skip applies to any non-root caller: only
// a root process can give a file to root, and the GUI never calls Root.
func root(path string) error {
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 0, 0); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		mode = 0o750
	}
	return os.Chmod(path, mode)
}

func grantUser(_, _ string) error {
	return errors.New("fileperm: on Unix, socket access is granted with a setfacl ACL, not file ownership")
}

func dirAdminOnly(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	return info.Mode().Perm()&0o022 == 0, nil
}

func noWorldAccess(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.Mode().Perm()&0o077 == 0, nil
}
