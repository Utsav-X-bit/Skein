//go:build !windows

package daemon

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// fileOwner resolves the config/identity owner to a username.
func fileOwner(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	uid := strconv.FormatUint(uint64(st.Uid), 10)
	if u, err := user.LookupId(uid); err == nil {
		return u.Username
	}
	return uid
}
