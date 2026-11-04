package services

import (
	"context"
	"fmt"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// InviteService generates and rotates invites.
type InviteService struct{}

func (s *InviteService) PeerInvite(ctx context.Context) (api.Invite, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Invite{}, err
	}
	var out api.Invite
	err = c.do(ctx, "POST", "/api/v1/invites/peer", nil, &out)
	return out, err
}

func (s *InviteService) NetworkInvite(ctx context.Context) (api.Invite, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Invite{}, err
	}
	var out api.Invite
	err = c.do(ctx, "POST", "/api/v1/invites/network", nil, &out)
	return out, err
}

// RotateSecret rotates the network secret through the daemon (Step 7
// endpoint, secret role, confirm), then restarts the service via the
// minimal helper so the new secret takes effect (Step 7 sequence).
// Step 8: the helper `rotate-secret` verb is no longer called.
//
// The caller must show the "every peer must rejoin" warning BEFORE
// calling — the frontend confirm dialog does that today, and the
// warning is repeated in the returned Invite. The new join string is
// returned once, in Invite.Value, and must be shown transiently and
// never logged; the old helper path returned an empty Invite, so the
// frontend never displayed the new join at all (M7 renders it).
// The binding signature is unchanged.
func (s *InviteService) RotateSecret(ctx context.Context) (api.Invite, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Invite{}, err
	}
	var res api.RotateResult
	err = c.do(ctx, "POST", "/api/v1/system/rotate-secret", map[string]bool{"confirm": true}, &res)
	if err != nil {
		return api.Invite{}, err
	}
	if err := runHelper(ctx, "restart"); err != nil {
		// The secret is rotated, but Wails discards the return
		// value when the error is non-nil, so the join cannot
		// travel in this error — and it must never be pasted into
		// an error string (it would land in a toast/log). The
		// recovery is a second rotation, which yields a fresh join
		// and invalidates the unshown one; the error says so.
		return api.Invite{}, fmt.Errorf("secret rotated, but restarting the service failed: %w; restart it from Settings, and if the new join string was not shown, rotate again to obtain a fresh one", err)
	}
	return api.Invite{Kind: "network", Value: res.Join,
		Warning: "every peer must rejoin with this join string; it contains the new network secret"}, nil
}
