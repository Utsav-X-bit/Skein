package daemon

import (
	"context"
	"net/http"
	"net/netip"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// PeerInvite implements api.GUI: a skein:1: invite for this node. It carries
// the identity and endpoints but no network secret — an introduction, not
// membership.
func (d *Daemon) PeerInvite(ctx context.Context) (api.Invite, error) {
	cfg := d.config()
	value, err := identity.EncodeInvite(identity.Invite{
		ID:        d.id.ID,
		PublicKey: d.id.PublicKey,
		Addrs:     allCandidates(ctx, cfg),
	})
	if err != nil {
		return api.Invite{}, &api.StatusError{
			Status:      http.StatusInternalServerError,
			Code:        api.ErrCodeInternal,
			Message:     err.Error(),
			Remediation: "check the daemon logs",
		}
	}
	return api.Invite{Kind: "peer", Value: value}, nil
}

// NetworkInvite implements api.GUI: a skein:2: join string. It carries the
// network secret plus a snapshot of the overlay addresses already in use, so
// it is regenerated after each join.
func (d *Daemon) NetworkInvite(ctx context.Context) (api.Invite, error) {
	cfg := d.config()
	var used []netip.Addr
	for _, p := range d.peers.List() {
		if p.OverlayV4.IsValid() {
			used = append(used, p.OverlayV4)
		}
	}
	value, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret:    cfg.Node.NetworkSecret,
		ID:        d.id.ID,
		PublicKey: d.id.PublicKey,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		UsedV4:    used,
		Addrs:     allCandidates(ctx, cfg),
	})
	if err != nil {
		return api.Invite{}, &api.StatusError{
			Status:      http.StatusInternalServerError,
			Code:        api.ErrCodeInternal,
			Message:     err.Error(),
			Remediation: "check the daemon logs",
		}
	}
	return api.Invite{
		Kind:     "network",
		Value:    value,
		Snapshot: true,
		Warning:  "This join string is a snapshot of the addresses in use; new devices pick unused addresses, so regenerate it after each join.",
	}, nil
}
