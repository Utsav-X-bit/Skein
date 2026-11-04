//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

// RevokeUser removes one user's SID from the system access file and
// reloads the live daemon's grants, so the revoked caller loses access
// on its next call without a restart.
//
// Windows authorisation model differs from Unix: the access file holds
// a single owner SID (SetOwnerRoot writes one bare SID, not a role
// table), so the gate is ownership, not a role column — the invoking
// token's SID must be SYSTEM or the owner SID itself; anyone else is
// refused before anything is edited. The daemon's grants/reload
// endpoint is secret-role-gated (Step 7) and the helper-as-SYSTEM
// satisfies it over the kernel pipe. As on Unix, there is no revoke
// API endpoint, so a read/mutate API caller cannot revoke.
//
// If the daemon is not running, the pipe is absent, reload is skipped,
// and the file change takes effect at next start. A live daemon that
// refuses reload is an error, not a silent stale table. Compile-only
// on this VM: Windows revoke runtime (pipe dial, token SID, DACL
// behaviour) is on the user's Windows-machine checklist, as with all
// Windows steps.
func RevokeUser(identity string, force bool) (RevokeOutcome, error) {
	sid, err := lookupSID(identity)
	if err != nil {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: %w", err)
	}
	invoking, err := currentTokenSID()
	if err != nil {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: cannot identify invoking user: %w", err)
	}
	if invoking != "S-1-5-18" && invoking != sid {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: invoking user %s is not SYSTEM or the access-file owner (only the owner may revoke grants)", invoking)
	}
	out, err := revokeGrantFromFile(AccessPath(), sid, force)
	if err != nil {
		return RevokeOutcome{}, err
	}
	// No probe dial first: the daemon serves one HTTP request per pipe
	// instance, so a bare connect-and-close would waste one. A dial
	// failure here is treated as "daemon not running" below.
	if err := reloadGrantsViaPipe(ControlPipeName); err != nil {
		if isDaemonDown(err) {
			out.ReloadNote = "daemon not reachable; the grant change takes effect at next start"
			return out, nil
		}
		return out, fmt.Errorf("revoke-user: access file updated but grants reload failed: %w (retry POST /api/v1/system/grants/reload as a secret holder, or restart the daemon)", err)
	}
	out.Reloaded = true
	return out, nil
}

// currentTokenSID returns the SID of the process token (the user who
// approved the UAC elevation), or an error. It fails closed: an
// unidentifiable invoker never revokes.
func currentTokenSID() (string, error) {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return token.User.Sid.String(), nil
}

// reloadGrantsViaPipe POSTs the Step 7 grants/reload endpoint over the
// kernel control pipe. One HTTP request per pipe instance, matching
// the GUI client's pipe discipline.
func reloadGrantsViaPipe(pipeName string) error {
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return pipeconn.Dial(pipeName)
			},
			DisableKeepAlives: true,
		},
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://skein/api/v1/system/grants/reload", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("grants reload: daemon returned %s", resp.Status)
	}
	return nil
}

func isDaemonDown(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
