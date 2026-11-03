package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Utsav-X-bit/skein/internal/app"
)

// SetupService runs the fixed-verb privileged bootstrap (init/join/import).
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

// Init creates the root-owned config and service. The result carries public
// metadata and a secret-bearing join string; the UI treats it as transient.
func (s *SetupService) Init(ctx context.Context, req InitRequest) (app.BootstrapResult, error) {
	return helperJSON[app.BootstrapResult](ctx, "init", req)
}

// Join configures this node from a network join string.
func (s *SetupService) Join(ctx context.Context, invite string) (app.BootstrapResult, error) {
	return helperJSON[app.BootstrapResult](ctx, "join", map[string]string{"invite": invite})
}

// Reset backs up the existing root config/identity (or keeps identity) so a
// new network can be created. It is destructive and requires confirmation.
func (s *SetupService) Reset(ctx context.Context, keepIdentity bool) (app.BackupResult, error) {
	args := []string{}
	if keepIdentity {
		args = append(args, "--keep-identity")
	}
	return helperJSON[app.BackupResult](ctx, "reset", nil, args...)
}

// Import migrates a user-mode config into the root-owned system path.
func (s *SetupService) Import(ctx context.Context, path string) (app.BootstrapResult, error) {
	return helperJSON[app.BootstrapResult](ctx, "import", nil, "--source", path)
}

func helperJSON[T any](ctx context.Context, verb string, payload any, args ...string) (T, error) {
	var zero T
	raw, runErr := runHelperJSON(ctx, verb, payload, args...)
	var env struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Remediation string `json:"remediation"`
	}
	_ = json.Unmarshal(raw, &env)
	if runErr != nil {
		if env.Error != "" {
			if env.Remediation != "" {
				return zero, fmt.Errorf("%s: %s", env.Error, env.Remediation)
			}
			return zero, errors.New(env.Error)
		}
		return zero, fmt.Errorf("helper %s: %w", verb, runErr)
	}
	if !env.OK {
		if env.Remediation != "" {
			return zero, fmt.Errorf("%s: %s", env.Error, env.Remediation)
		}
		return zero, errors.New(env.Error)
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, err
	}
	return result, nil
}
