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

// LeaveNetwork leaves this node's own network (ADR-002, Step 7
// endpoint). It is the only peer-departure action: Step 12 deleted
// the GUI peer-removal method with the DELETE endpoint — no peer
// removes another peer. Propagated is the source of truth — until M4 lands
// leave-announcement gossip it is false, and the caller must say
// so rather than claim a mesh-wide leave.
// (The frontend wires it when bindings regenerate.)
func (s *PeerService) LeaveNetwork(ctx context.Context) (api.LeaveResult, error) {
	c, err := ControlClient()
	if err != nil {
		return api.LeaveResult{}, err
	}
	var out api.LeaveResult
	err = c.do(ctx, "POST", "/api/v1/system/leave", map[string]bool{"confirm": true}, &out)
	return out, err
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
