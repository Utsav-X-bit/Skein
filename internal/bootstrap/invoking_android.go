//go:build android

package bootstrap

import (
	"fmt"
	"os"
)

// InvokingCallerID returns the kernel caller identity on Android: the
// app itself. There is no pkexec/sudo elevation on Android — the GUI
// and the daemon are the same process under the app's own UID — so
// the invoking caller is simply the current UID, in the same
// "uid:<n>" form the Unix implementation returns from PKEXEC_UID /
// SUDO_UID. The bootstrap token is bound to this identity, and the
// in-process transport stamps the same UID as the kernel caller, so
// verification and the daemon's secret-role grant agree.
func InvokingCallerID() (string, error) {
	return fmt.Sprintf("uid:%d", os.Getuid()), nil
}
