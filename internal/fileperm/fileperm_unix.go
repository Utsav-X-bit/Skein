//go:build !windows

package fileperm

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func restrict(path string) error {
	return os.Chmod(path, 0o600)
}

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
	return chownToSudoUser(path)
}
