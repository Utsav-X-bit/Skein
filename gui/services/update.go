package services

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// UpdateService checks for and applies daemon updates (Step 7
// endpoints). The old Check was a stub that always returned
// ErrDaemonNotConnected; it is kept with its binding signature, but
// now says why it cannot answer: a daemon update check verifies a
// signed manifest, and the GUI has no manifest path to check. The
// real service-layer methods are CheckManifest and Apply — the M7
// About/Updates screen wires them when bindings regenerate, passing
// the staged manifest path the installer/release flow provides.
// Apply is asynchronous by necessity (Step 7): it returns
// {detached:true} and the caller must poll CheckManifest for
// LastUpdate (applied vs rolled back), never assume success from
// the apply response.
type UpdateService struct{}

// Check reports an available update, if any.
func (s *UpdateService) Check(ctx context.Context) (UpdateInfo, error) {
	return UpdateInfo{}, fmt.Errorf("update check needs a staged signed manifest; use CheckManifest with its path")
}

// CheckManifest verifies a staged manifest against the daemon and
// reports the newest verified version plus the last apply outcome.
func (s *UpdateService) CheckManifest(ctx context.Context, manifestPath string) (api.UpdateCheckResult, error) {
	c, err := ControlClient()
	if err != nil {
		return api.UpdateCheckResult{}, err
	}
	var out api.UpdateCheckResult
	err = c.do(ctx, "GET", "/api/v1/system/update/check?manifest_path="+url.QueryEscape(manifestPath), nil, &out)
	return out, err
}

// Apply launches the daemon's detached apply for a verified manifest.
// It never claims success: poll CheckManifest's LastUpdate.
func (s *UpdateService) Apply(ctx context.Context, manifestPath, stagedBinaryPath string) (api.UpdateApplyResult, error) {
	c, err := ControlClient()
	if err != nil {
		return api.UpdateApplyResult{}, err
	}
	var out api.UpdateApplyResult
	body := map[string]any{"manifest_path": manifestPath, "confirm": true}
	if stagedBinaryPath != "" {
		body["staged_binary_path"] = stagedBinaryPath
	}
	err = c.do(ctx, "POST", "/api/v1/system/update/apply", body, &out)
	return out, err
}

// ReloadGrants reloads the daemon's grant file after a grant change
// (Step 7 endpoint, secret role). The GUI calls it after any
// grant-user/revoke-user change so the running daemon's roles match
// the file without a restart.
func (s *UpdateService) ReloadGrants(ctx context.Context) error {
	c, err := ControlClient()
	if err != nil {
		return err
	}
	return c.do(ctx, "POST", "/api/v1/system/grants/reload", nil, nil)
}
