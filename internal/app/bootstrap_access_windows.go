//go:build windows

package app

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// bootstrapAccessPath returns the access-file path for bootstrap
// mode on Windows, where AccessPath is a function of ProgramData.
func bootstrapAccessPath() string { return AccessPath() }

// grantBootstrapTransportAccess admits the invoking user to the control
// pipe the bootstrap daemon is about to bind. NewPipeListener derives the
// pipe DACL from the access file at bind time, and a fresh install has
// not written this user yet (the installer registers the service only
// through the bootstrap flow), so without this the unelevated GUI cannot
// connect to post its token.
//
// The elevated helper runs as the same user account (UAC elevation does
// not change the user SID), so its token user is the interactive user.
// The grant is the least role — read — and every existing grant and role
// is preserved: this never grants secret or mutate. The single-use token
// remains the only bootstrap authorisation, and the daemon's commit
// upgrades the token's bound caller to secret on success.
func grantBootstrapTransportAccess() error {
	tok, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("bootstrap: invoking user: %w", err)
	}
	sid := tok.User.Sid.String()
	grants := map[string]api.Role{}
	if raw, err := os.ReadFile(AccessPath()); err == nil {
		parsed, err := api.ParseAccessGrants(strings.NewReader(string(raw)))
		if err != nil {
			return fmt.Errorf("bootstrap: access file %s: %w", AccessPath(), err)
		}
		grants = parsed
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: access file %s: %w", AccessPath(), err)
	}
	if _, ok := grants[sid]; !ok {
		grants[sid] = api.RoleRead
	}
	keys := make([]string, 0, len(grants))
	for k := range grants {
		// Windows grants are SIDs; a uid line here is a malformed file
		// (the pipe layer skips it) and would be rewritten into an
		// unparseable "uid:<n>:<role>" line. Fail closed.
		if strings.HasPrefix(k, "uid:") {
			return fmt.Errorf("bootstrap: access file %s contains a uid grant, which Windows cannot use", AccessPath())
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s:%s\n", k, grants[k].String())
	}
	return writeAccessFileAtomic(AccessPath(), []byte(b.String()))
}
