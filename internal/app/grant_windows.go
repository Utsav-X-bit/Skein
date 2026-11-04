//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// GrantUserRoot is the Linux uid+ACL verb.
func GrantUserRoot(int) error {
	return fmt.Errorf("grant-user is a Linux verb; on Windows use set-owner")
}

// GrantUserRoleRoot is the Linux uid:role grant verb; same Windows stub.
func GrantUserRoleRoot(int, string) error {
	return fmt.Errorf("grant-user is a Linux verb; on Windows use set-owner")
}

// SetOwnerRoot rewrites the config directory, config files, and control pipe
// DACLs to SYSTEM, Administrators, and the single interactive user.
func SetOwnerRoot(user string) error {
	sid, err := lookupSID(user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(SystemDir(), 0o750); err != nil {
		return fmt.Errorf("set-owner: %s: %w", SystemDir(), err)
	}
	if err := os.WriteFile(AccessPath(), []byte(sid+"\n"), 0o600); err != nil {
		return fmt.Errorf("set-owner: access file: %w", err)
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

// grantBootstrapUser grants the interactive user that launched the elevated
// helper access to the service control pipe.
func grantBootstrapUser() error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	return SetOwnerRoot(user.User.Sid.String())
}
