package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/app"
)

// SecurityService reads the authorisation table and runs the privileged
// grant verbs. The GUI itself is unprivileged: every write goes through the
// fixed-verb helper, never through a shell or a config path chosen here.
type SecurityService struct{}

// Grants returns the live authorisation table and the caller's own security
// facts. The daemon requires the secret role over the kernel transport, so a
// bearer (dev TCP) client is refused rather than shown a table it cannot use.
func (s *SecurityService) Grants(ctx context.Context) (api.AccessGrantsView, error) {
	c, err := ControlClient()
	if err != nil {
		return api.AccessGrantsView{}, err
	}
	var out api.AccessGrantsView
	err = c.do(ctx, "GET", "/api/v1/security/grants", nil, &out)
	return out, err
}

// GrantUser grants uid an explicit role. It is the Linux verb; Windows
// transfers ownership with SetOwner instead. The helper writes the access
// file and the socket ACL but does not reload the daemon, so the live table
// is refreshed here; a reload failure is reported with the grant already
// persisted (the change still applies at the next daemon start).
func (s *SecurityService) GrantUser(ctx context.Context, uid int, role string) error {
	if runtime.GOOS == "windows" {
		return errors.New("grant-user is a Linux verb; use set-owner on Windows")
	}
	if err := runHelper(ctx, "grant-user", strconv.Itoa(uid), "--role", role); err != nil {
		return err
	}
	if err := (&UpdateService{}).ReloadGrants(ctx); err != nil {
		return fmt.Errorf("grant-user: the grant is written but the running daemon did not reload it: %w", err)
	}
	return nil
}

// SetOwner transfers ownership of the system config and control pipe to
// user. It is the Windows verb; Linux grants socket access per user with
// GrantUser. The new owner's grant and pipe access are built when the daemon
// starts, so the caller must restart the service afterwards.
func (s *SecurityService) SetOwner(ctx context.Context, user string) error {
	if runtime.GOOS != "windows" {
		return errors.New("set-owner is a Windows verb; use grant-user on Linux")
	}
	return runHelper(ctx, "set-owner", user)
}

// RevokeUser removes identity's grant through the helper, which also reloads
// the live daemon, so no separate reload is needed. force is passed only
// after the UI has shown the helper's refusal naming the consequence (last
// grant, or last secret grant).
func (s *SecurityService) RevokeUser(ctx context.Context, identity string, force bool) (app.RevokeOutcome, error) {
	args := []string{identity}
	if force {
		args = append(args, "--force")
	}
	raw, runErr := runHelperJSON(ctx, "revoke-user", nil, args...)
	return decodeRevokeOutcome(raw, runErr)
}

// helperEnvelope is the fixed helper result shape. Both channels (stdout on
// Unix, the result file on Windows) emit it; Windows reports a failed verb
// inside the envelope rather than as a process error.
type helperEnvelope struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	Remediation string `json:"remediation"`
	Identity    string `json:"identity"`
	Role        string `json:"role"`
	Warning     string `json:"warning"`
	Reloaded    bool   `json:"reloaded"`
	Note        string `json:"note"`
}

// decodeRevokeOutcome turns the helper envelope into a public outcome. The
// helper's own error text is surfaced as-is; only a transport failure with no
// envelope is wrapped.
func decodeRevokeOutcome(raw []byte, runErr error) (app.RevokeOutcome, error) {
	var env helperEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	if runErr != nil || !env.OK {
		switch {
		case env.Error != "" && env.Remediation != "":
			return app.RevokeOutcome{}, fmt.Errorf("%s (%s)", env.Error, env.Remediation)
		case env.Error != "":
			return app.RevokeOutcome{}, errors.New(env.Error)
		case runErr != nil:
			return app.RevokeOutcome{}, fmt.Errorf("helper revoke-user: %w", runErr)
		default:
			return app.RevokeOutcome{}, errors.New("helper revoke-user: empty result")
		}
	}
	return app.RevokeOutcome{
		Identity:   env.Identity,
		Role:       env.Role,
		Warning:    env.Warning,
		Reloaded:   env.Reloaded,
		ReloadNote: env.Note,
	}, nil
}
