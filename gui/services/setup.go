package services

import (
	"context"
	"fmt"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/app"
)

// SetupService runs first-setup and reset. Step 8: Init/Join/Import no
// longer call the helper's init/join/import verbs — those verbs are
// deprecated in Step 10 and deleted in Step 12. Instead each call
// mints a single-use bootstrap token through the minimal helper (the
// only elevation in the flow) and posts it to the daemon's bootstrap
// endpoint over the verified control transport (bootstrap.go). The
// signatures and the BootstrapResult shape are unchanged, so the
// frontend bindings keep working. Reset goes through the daemon's
// system/reset endpoint and then stops the service via the helper
// (Step 7 sequence); it never calls helper `reset`.
type SetupService struct{}

// InitRequest is the onboarding create-network form.
type InitRequest struct {
	Name       string `json:"name"`
	PrefixV4   string `json:"prefix_v4,omitempty"`
	PrefixV6   string `json:"prefix_v6,omitempty"`
	Port       uint16 `json:"port,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
	MDNS       *bool  `json:"mdns,omitempty"`
	STUN       *bool  `json:"stun,omitempty"`
	STUNServer string `json:"stun_server,omitempty"`
}

// Init creates a network via the bootstrap flow. The result carries
// public metadata and a secret-bearing join string; the UI treats it
// as transient (shown once, never logged).
func (s *SetupService) Init(ctx context.Context, req InitRequest) (app.BootstrapResult, error) {
	token, err := mintBootstrapToken(ctx, "init")
	if err != nil {
		return app.BootstrapResult{}, err
	}
	fields := map[string]any{"name": req.Name}
	if req.PrefixV4 != "" {
		fields["prefix_v4"] = req.PrefixV4
	}
	if req.PrefixV6 != "" {
		fields["prefix_v6"] = req.PrefixV6
	}
	if req.Port != 0 {
		fields["port"] = req.Port
	}
	if req.MTU != 0 {
		fields["mtu"] = req.MTU
	}
	if req.MDNS != nil {
		fields["mdns"] = *req.MDNS
	}
	if req.STUN != nil {
		fields["stun"] = *req.STUN
	}
	if req.STUNServer != "" {
		fields["stun_server"] = req.STUNServer
	}
	return bootstrapCall(ctx, "init", token, fields)
}

// Join configures this node from a network join string, via bootstrap.
func (s *SetupService) Join(ctx context.Context, invite string) (app.BootstrapResult, error) {
	token, err := mintBootstrapToken(ctx, "join")
	if err != nil {
		return app.BootstrapResult{}, err
	}
	return bootstrapCall(ctx, "join", token, map[string]any{"invite": invite})
}

// Rejoin restores membership in the current network. It never bootstraps or
// switches credentials; a different-network invite is refused by the daemon.
func (s *SetupService) Rejoin(ctx context.Context, invite string) (api.JoinResult, error) {
	c, err := ControlClient()
	if err != nil {
		return api.JoinResult{}, err
	}
	var res api.JoinResult
	err = c.do(ctx, "POST", "/api/v1/system/join", map[string]any{"invite": invite, "confirm": true}, &res)
	return res, err
}

// NetworkResetResult preserves completed destructive work across a later
// helper failure. Wails discards a return value when an error is also returned,
// so a failed stop must be data: retrying reset would lose the original backup.
type NetworkResetResult struct {
	Backup        string `json:"backup"`
	FirewallError string `json:"firewall_error,omitempty"`
	StopError     string `json:"stop_error,omitempty"`
}

// ResetWithPurge resets through the daemon, then verifies the service stopped.
// Purge is irreversible; otherwise Backup names the daemon-created backup.
func (s *SetupService) ResetWithPurge(ctx context.Context, purge, keepIdentity bool) (NetworkResetResult, error) {
	c, err := ControlClient()
	if err != nil {
		return NetworkResetResult{}, err
	}
	var res api.ResetResult
	if err := c.do(ctx, "POST", "/api/v1/system/reset", api.ResetOptions{Purge: purge, KeepIdentity: keepIdentity, Confirm: true}, &res); err != nil {
		return NetworkResetResult{}, err
	}
	out := NetworkResetResult{Backup: res.Backup, FirewallError: res.FirewallError}
	if err := (&ServiceService{}).Stop(ctx); err != nil {
		out.StopError = fmt.Sprintf("The network was reset, but the service did not stop: %v", err)
	}
	return out, nil
}

// Import migrates a user-mode config into the system path, via
// bootstrap. The source path is a local path the user typed, not a
// secret; it travels in the bootstrap request body.
func (s *SetupService) Import(ctx context.Context, path string) (app.BootstrapResult, error) {
	token, err := mintBootstrapToken(ctx, "import")
	if err != nil {
		return app.BootstrapResult{}, err
	}
	return bootstrapCall(ctx, "import", token, map[string]any{"source": path})
}
