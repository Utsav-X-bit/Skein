//go:build windows

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// runHelper is a variable (not a func) so service-layer tests can
// substitute a fake helper without UAC; see helper.go.
var runHelper = func(ctx context.Context, verb string, args ...string) error {
	raw, runErr := runHelperJSON(ctx, verb, nil, args...)
	var env struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Remediation string `json:"remediation"`
	}
	_ = json.Unmarshal(raw, &env)
	if runErr != nil {
		if env.Error != "" {
			if env.Remediation != "" {
				return fmt.Errorf("%s: %s", env.Error, env.Remediation)
			}
			return errors.New(env.Error)
		}
		return fmt.Errorf("helper %s: %w", verb, runErr)
	}
	if !env.OK {
		if env.Remediation != "" {
			return fmt.Errorf("%s: %s", env.Error, env.Remediation)
		}
		return errors.New(env.Error)
	}
	return nil
}
