//go:build windows

package app

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// GrantUserRoot is the Linux uid+ACL verb.
func GrantUserRoot(int) error {
	return fmt.Errorf("grant-user is a Linux verb; on Windows use set-owner")
}

// SetOwnerRoot rewrites the config directory, config files, and control pipe
// DACLs to SYSTEM, Administrators, and the single interactive user.
func SetOwnerRoot(user string) error {
	sid, err := lookupSID(user)
	if err != nil {
		return err
	}
	if err := fileperm.GrantUser(SystemDir(), sid); err != nil {
		return fmt.Errorf("set-owner: %s: %w", SystemDir(), err)
	}
	for _, p := range []string{SystemConfigPath(), SystemIdentityPath()} {
		if err := fileperm.GrantUser(p, sid); err != nil {
			return fmt.Errorf("set-owner: %s: %w", p, err)
		}
	}
	if err := fileperm.GrantUser(ControlPipe, sid); err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("set-owner: control pipe: %w", err)
	}
	return nil
}

// lookupSID accepts a SID as-is or resolves a user name to its SID.
func lookupSID(user string) (string, error) {
	if _, err := windows.StringToSid(user); err == nil {
		return user, nil
	}
	sid, _, _, err := windows.LookupSID("", user)
	if err != nil {
		return "", fmt.Errorf("set-owner: resolve user %q: %w", user, err)
	}
	return sid.String(), nil
}
