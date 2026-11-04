//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

// RevokeUser removes one user's SID from the system access file and
// reloads the live daemon's grants, so the revoked caller loses access
// on its next call without a restart.
//
// The gate is role-based, matching Unix: SYSTEM, any secret-role
// holder, or the grant holder themselves may revoke; anyone else is
// refused before anything is edited. (M9 §2c: the old gate compared
// the invoker to the TARGET sid, so a secret-holding owner could
// only revoke themselves or as SYSTEM, while its message claimed
// "only the owner may revoke grants".) The daemon's grants/reload
// endpoint is secret-role-gated (Step 7); a secret holder's reload
// succeeds, while a self-revoker with a lesser role gets the file
// change with a reload failure (it takes effect at next start). As
// on Unix, there is no revoke API endpoint, so a read/mutate API
// caller cannot revoke.
//
// If the daemon is not running, the pipe is absent, reload is skipped,
// and the file change takes effect at next start. A live daemon that
// refuses reload is an error, not a silent stale table.
func RevokeUser(identity string, force bool) (RevokeOutcome, error) {
	sid, err := lookupSID(identity)
	if err != nil {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: %w", err)
	}
	invoking, err := currentTokenSID()
	if err != nil {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: cannot identify invoking user: %w", err)
	}
	if invoking != "S-1-5-18" && invoking != sid && !invokerHasSecretRole(invoking) {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: invoking user %s is not SYSTEM, a secret holder, or the grant holder (only SYSTEM, secret holders, or the grant holder may revoke grants)", invoking)
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

// invokerHasSecretRole reports whether sid holds the secret role in
// the system access file. An unreadable or malformed file fails
// closed (false): no edit happens on a file the helper cannot read.
func invokerHasSecretRole(sid string) bool {
	f, err := os.Open(AccessPath())
	if err != nil {
		return false
	}
	defer f.Close()
	grants, err := api.ParseAccessGrants(f)
	if err != nil {
		return false
	}
	return grants[sid] == api.RoleSecret
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
