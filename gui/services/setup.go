package services

import (
	"context"
	"errors"
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
	res, err := bootstrapCall(ctx, "init", token, fields)
	if err != nil {
		// A failed attempt must not leave the bootstrap server
		// running with its token spent: on Android the next mint
		// would be refused ("a bootstrap is already in progress")
		// and the user would be stuck until the token expires.
		// Tear it down so a retry starts clean. Best-effort: on
		// desktop there is no in-process server to cancel.
		_ = cancelBootstrapImpl(ctx)
	}
	return res, err
}

// Join configures this node from a network join string, via bootstrap.
func (s *SetupService) Join(ctx context.Context, invite string) (app.BootstrapResult, error) {
	token, err := mintBootstrapToken(ctx, "join")
	if err != nil {
		return app.BootstrapResult{}, err
	}
	res, err := bootstrapCall(ctx, "join", token, map[string]any{"invite": invite})
	if err != nil {
		// See Init: a failed join (e.g. a truncated invite) must
		// release the bootstrap server so the user can retry
		// immediately with a corrected invite.
		_ = cancelBootstrapImpl(ctx)
	}
	return res, err
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

// ensureBootstrapImpl starts the bootstrap server for verb. It is
// overridden on Android (setup_android.go), where the server runs
// in-process; elsewhere onboarding uses the desktop helper flow and
// this reports that the in-process server is unavailable. The method
// itself lives here, untagged, so the bindings generator sees it on
// every platform.
var ensureBootstrapImpl = func(ctx context.Context, verb string) error {
	return errors.New("the in-process bootstrap server is only available on Android")
}

var cancelBootstrapImpl = func(ctx context.Context) error {
	return errors.New("the in-process bootstrap server is only available on Android")
}

// EnsureBootstrap prepares the bootstrap server for verb ("init",
// "join" or "import"). The mobile Onboarding calls it after the user
// picks create/join and before submitting the form, so the bootstrap
// endpoint is up when SetupService.Init/Join posts. On Android it
// mints the single-use token and serves the daemon's bootstrap-only
// API over the in-process transport — the Android equivalent of the
// desktop helper minting a token and starting the service in
// bootstrap mode.
func (s *SetupService) EnsureBootstrap(ctx context.Context, verb string) error {
	return ensureBootstrapImpl(ctx, verb)
}

// CancelBootstrap stops an abandoned onboarding bootstrap server
// and releases the config-store lock it holds, so a later Connect
// can start the normal daemon. A successful bootstrap stops itself.
func (s *SetupService) CancelBootstrap(ctx context.Context) error {
	return cancelBootstrapImpl(ctx)
}

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
	res, err := bootstrapCall(ctx, "import", token, map[string]any{"source": path})
	if err != nil {
		// See Init: a failed import must release the bootstrap
		// server so the user can retry immediately.
		_ = cancelBootstrapImpl(ctx)
	}
	return res, err
}
