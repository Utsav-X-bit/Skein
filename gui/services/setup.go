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

// Reset backs up the existing config/identity (or keeps identity) so
// a new network can be created. It is destructive: the caller (the
// frontend confirm dialog) must have confirmed before calling. The
// daemon performs the reset; this method then stops the service via
// the minimal helper and reports the backup path. If stopping fails,
// the backup path is still returned in the error so it cannot be
// silently lost.
func (s *SetupService) Reset(ctx context.Context, keepIdentity bool) (app.BackupResult, error) {
	return s.resetWithPurge(ctx, false, keepIdentity)
}

// ResetWithPurge is Reset with the purge option the uninstall/reset
// dialog needs (M7 frontend wires it when bindings regenerate; Reset
// keeps the existing binding signature). With purge, config/identity
// are removed instead of backed up.
func (s *SetupService) ResetWithPurge(ctx context.Context, purge, keepIdentity bool) (app.BackupResult, error) {
	return s.resetWithPurge(ctx, purge, keepIdentity)
}

func (s *SetupService) resetWithPurge(ctx context.Context, purge, keepIdentity bool) (app.BackupResult, error) {
	c, err := ControlClient()
	if err != nil {
		return app.BackupResult{}, err
	}
	var res api.ResetResult
	err = c.do(ctx, "POST", "/api/v1/system/reset", api.ResetOptions{Purge: purge, KeepIdentity: keepIdentity, Confirm: true}, &res)
	if err != nil {
		return app.BackupResult{}, err
	}
	msg := res.Message
	if res.FirewallError != "" {
		msg += " (firewall cleanup failed: " + res.FirewallError + "; rules may remain)"
	}
	if err := runHelper(ctx, "stop"); err != nil {
		return app.BackupResult{Backup: res.Backup, Message: msg},
			fmt.Errorf("reset complete, backup at %s, but stopping the service failed: %w; stop it manually", res.Backup, err)
	}
	return app.BackupResult{Backup: res.Backup, Message: msg}, nil
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
