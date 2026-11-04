//go:build windows

package bootstrap

import (
	"golang.org/x/sys/windows"
)

// InvokingCallerID returns the SID of the user whose elevated token
// the helper runs under — the same source the Windows grant path
// uses. Under UAC runas, the elevated token's user is the invoking
// user, so the token is bound to that user, not to SYSTEM.
func InvokingCallerID() (string, error) {
	tok, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return tok.User.Sid.String(), nil
}
