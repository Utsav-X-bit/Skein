//go:build !windows

package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// InvokingCallerID returns the kernel caller identity the elevated
// helper was invoked by, in api.Caller.ID() format ("uid:<n>"). It
// reads the same sources the grant path already uses (PKEXEC_UID,
// SUDO_UID). If neither is set, minting fails closed: a token bound
// to no caller, or to root itself, would be unbound and therefore
// unusable as proof of a human's elevation.
func InvokingCallerID() (string, error) {
	for _, key := range []string{"PKEXEC_UID", "SUDO_UID"} {
		if raw := os.Getenv(key); raw != "" {
			uid, err := strconv.Atoi(raw)
			if err != nil || uid <= 0 {
				return "", fmt.Errorf("bootstrap: %s %q is not a positive uid", key, raw)
			}
			return fmt.Sprintf("uid:%d", uid), nil
		}
	}
	return "", errors.New("bootstrap: cannot determine the invoking user (PKEXEC_UID/SUDO_UID unset); run the helper via pkexec or sudo")
}
