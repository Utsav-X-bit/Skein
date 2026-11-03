package services

import (
	"context"
	"fmt"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// PeerService manages the peer table.
type PeerService struct{}

func (s *PeerService) List(ctx context.Context) ([]api.Peer, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out []api.Peer
	err = c.do(ctx, "GET", "/api/v1/peers", nil, &out)
	return out, err
}

func (s *PeerService) Add(ctx context.Context, req api.AddPeerRequest) error {
	c, err := ControlClient()
	if err != nil {
		return err
	}
	return c.do(ctx, "POST", "/api/v1/peers", req, nil)
}

func (s *PeerService) Remove(ctx context.Context, peerID string) error {
	c, err := ControlClient()
	if err != nil {
		return err
	}
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/v1/peers/%s", peerID), nil, nil)
}

func (s *PeerService) Probe(ctx context.Context, peerID string) (api.ProbeResult, error) {
	c, err := ControlClient()
	if err != nil {
		return api.ProbeResult{}, err
	}
	var out api.ProbeResult
	err = c.do(ctx, "POST", fmt.Sprintf("/api/v1/peers/%s/probe", peerID), nil, &out)
	return out, err
}
