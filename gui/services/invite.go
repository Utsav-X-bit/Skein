package services

import (
	"context"

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

// RotateSecret is helper-only; it requires elevation and stops the service.
func (s *InviteService) RotateSecret(ctx context.Context) (api.Invite, error) {
	return api.Invite{}, runHelper(ctx, "rotate-secret")
}
