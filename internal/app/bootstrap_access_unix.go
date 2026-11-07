//go:build !windows

package app

import (
	"fmt"
	"os/exec"
)

// bootstrapAccessPath returns the access-file path for bootstrap
// mode on Unix, where AccessPath is a constant.
func bootstrapAccessPath() string { return AccessPath }

// grantBootstrapTransportAccess admits the invoking user to the control
// socket the bootstrap daemon is about to bind. NewUnixListener derives
// the socket ACL from the access file at bind time, and a fresh install
// may not have added this user yet (no skein group, no prior grant), so
// without this the token holder cannot even connect to post it.
//
// The grant is the least role the file format can express — a bare uid,
// which ParseAccessGrants reads as read. It never writes secret or
// mutate: the single-use token remains the only bootstrap authorisation,
// and the daemon's commit upgrades the token's bound caller to secret on
// success. grantBootstrapUser preserves an existing role for the same
// uid, so a previously granted user is never downgraded.
func grantBootstrapTransportAccess() error {
	// The daemon will apply this uid as a socket ACL. Fail here, with a
	// remediation, instead of surfacing a service-start failure later
	// when the bind cannot run setfacl.
	if _, err := exec.LookPath("setfacl"); err != nil {
		return fmt.Errorf("setfacl is required to admit the invoking user to the control socket (install the acl package): %w", err)
	}
	return grantBootstrapUser()
}
