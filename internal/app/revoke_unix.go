//go:build !windows

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

// RevokeUser removes one uid's grant from the system access file and
// reloads the live daemon's grants, so the revoked caller loses access
// on its next call without a restart.
//
// Authorisation (the secret-role requirement): the helper itself runs
// elevated, but elevation alone is not the gate — the *invoking* user
// must hold the secret role. The invoking uid comes from PKEXEC_UID /
// SUDO_UID (the user who approved the elevation), falling back to the
// process uid for a direct root invocation; uid 0 is implicitly secret
// (api.roleFor: root already owns the system, denying it the API would
// be theatre). A read/mutate invoking user is refused before anything
// is edited, and the daemon's grants/reload endpoint is itself
// secret-role-gated (Step 7), with the helper-as-root satisfying it
// over the kernel socket. There is deliberately no revoke API endpoint:
// a read/mutate API caller has no endpoint to call, and cannot edit
// the root-owned access file without elevation.
//
// Order (fail-closed at each step): invoking-role check -> socket ACL
// removal (if the socket exists) -> access-file edit -> daemon reload
// (if the socket exists). If the daemon is not running, the socket is
// absent, reload is skipped, and the file change takes effect at next
// start; ReloadNote says so. If the daemon is running but reload is
// refused (non-2xx), that is an error: the file edit stands, but the
// live table is stale until reload succeeds or the daemon restarts.
func RevokeUser(identity string, force bool) (RevokeOutcome, error) {
	uid, err := strconv.Atoi(identity)
	if err != nil || uid <= 0 {
		return RevokeOutcome{}, fmt.Errorf("revoke-user: uid %q must be a positive integer", identity)
	}
	invoking := invokingUID()
	if invoking != 0 {
		roles, err := accessRoles(AccessPath)
		if err != nil {
			return RevokeOutcome{}, fmt.Errorf("revoke-user: cannot verify invoking uid %d: %w", invoking, err)
		}
		if roles[strconv.Itoa(invoking)] != "secret" {
			return RevokeOutcome{}, fmt.Errorf("revoke-user: invoking uid %d does not hold the secret role (only a secret holder may revoke grants)", invoking)
		}
	}
	socketExists := false
	if _, err := os.Stat(ControlSocket); err == nil {
		socketExists = true
	}
	if socketExists {
		if _, err := execOutput("setfacl", "-x", fmt.Sprintf("u:%d", uid), ControlSocket); err != nil {
			return RevokeOutcome{}, fmt.Errorf("revoke-user: socket ACL: %w (install the acl package if setfacl is missing)", err)
		}
	}
	out, err := revokeGrantFromFile(AccessPath, strconv.Itoa(uid), force)
	if err != nil {
		if socketExists {
			return RevokeOutcome{}, fmt.Errorf("%w (note: the socket ACL for uid %d was already removed; re-run grant-user if this revoke should not stand)", err, uid)
		}
		return RevokeOutcome{}, err
	}
	if !socketExists {
		out.ReloadNote = "daemon not running; the grant change takes effect at next start"
		return out, nil
	}
	if err := reloadGrantsViaSocket(ControlSocket); err != nil {
		if isDaemonDown(err) {
			out.ReloadNote = "daemon not reachable; the grant change takes effect at next start"
			return out, nil
		}
		return out, fmt.Errorf("revoke-user: access file updated but grants reload failed: %w (retry POST /api/v1/system/grants/reload as a secret holder, or restart the daemon)", err)
	}
	out.Reloaded = true
	return out, nil
}

// invokingUID is the user who approved this elevation: pkexec/sudo
// record it, and a direct root shell has uid 0 already.
func invokingUID() int {
	for _, key := range []string{"PKEXEC_UID", "SUDO_UID"} {
		if raw := os.Getenv(key); raw != "" {
			if uid, err := strconv.Atoi(raw); err == nil && uid > 0 {
				return uid
			}
		}
	}
	return os.Getuid()
}

// reloadGrantsViaSocket POSTs the Step 7 grants/reload endpoint over
// the kernel Unix socket. The caller is the helper-as-root, which
// roleFor maps to Secret; no bearer token is involved, and the socket
// directory is root-owned, so no separate server-identity check is
// needed here (Step 2 verification is the unprivileged clients' duty).
func reloadGrantsViaSocket(socketPath string) error {
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
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

// isDaemonDown reports whether a reload failure means "no daemon to
// talk to" (stale socket file, daemon stopped between the stat and the
// dial) rather than "the daemon refused". Dial errors are down;
// an HTTP status is a live daemon's answer and is never down (it
// surfaces as a non-2xx error before this is consulted).
func isDaemonDown(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
