//go:build windows

package fileperm

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// restrict replaces the file's DACL with full control for the current user,
// SYSTEM, and Administrators, and protects it from inheritance. SYSTEM is
// required because the Windows service runs as LocalSystem and must read its
// configuration and identity key.
func restrict(path string) error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read token user: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("build SYSTEM SID: %w", err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("build Administrators SID: %w", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		allow(user.User.Sid),
		allow(system),
		allow(admins),
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}

func chownToSudoUser(string) error { return nil }

func preserveOwner(string, string) error { return nil }

// root replaces the DACL with full control for SYSTEM and Administrators
// only (no interactive user). The DACL is the service boundary; setting the
// owner to SYSTEM needs SeRestorePrivilege and fails for some elevated
// administrators, so the owner is left unchanged.
func root(path string) error {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("build SYSTEM SID: %w", err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("build Administrators SID: %w", err)
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		allow(system),
		allow(admins),
	}, nil)
	if err != nil {
		return fmt.Errorf("build DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}

// grantUser rewrites the DACL to SYSTEM, Administrators, and user, so the
// single interactive user keeps socket/config access after set-owner.
func grantUser(path, user string) error {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("build SYSTEM SID: %w", err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("build Administrators SID: %w", err)
	}
	sid, err := windows.StringToSid(user)
	if err != nil {
		return fmt.Errorf("user %q is not a SID: %w", user, err)
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		allow(system),
		allow(admins),
		allow(sid),
	}, nil)
	if err != nil {
		return fmt.Errorf("build DACL: %w", err)
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}

func allow(sid *windows.SID) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
