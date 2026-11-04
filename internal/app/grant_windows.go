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
// DACLs to SYSTEM, Administrators, and the single interactive user, and
// makes that user the access file's sole secret holder. The grant is a
// role line (SID:secret), never a bare SID: a bare line parses to Read,
// so the old form demoted the new owner on the next grants reload
// (M9 §2c). The access file's DACL is set explicitly too: previously
// only the file's content was rewritten, leaving its DACL to whatever
// WriteFile preserved.
// Ownership transfer replaces the file (other grants do not survive);
// restart the daemon afterwards so the new owner can connect (the
// pipe listener's granted set is built at start).
func SetOwnerRoot(user string) error {
	sid, err := lookupSID(user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(SystemDir(), 0o750); err != nil {
		return fmt.Errorf("set-owner: %s: %w", SystemDir(), err)
	}
	if err := os.WriteFile(AccessPath(), []byte(sid+":secret\n"), 0o600); err != nil {
		return fmt.Errorf("set-owner: access file: %w", err)
	}
	if err := fileperm.GrantUser(AccessPath(), sid); err != nil {
		return fmt.Errorf("set-owner: access file DACL: %w", err)
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
