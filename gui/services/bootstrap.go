package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Utsav-X-bit/skein/internal/app"
)

// Step 8 bootstrap flow (plan §8.1, Step 4 token mechanics). The GUI
// stays unprivileged: it asks the minimal helper to mint a single-use
// token (the one elevation in this flow), then calls the daemon's
// bootstrap endpoint over the verified control transport. The token
// lives only in memory for that one call: it is never logged, never
// written to disk in plaintext, and never placed in argv or a URL.
// (Go strings are immutable, so the token string itself lingers until
// GC — the same residual Step 4 records for every secret in this
// codebase. What Step 8 guarantees is no copy on disk, in a log, or
// in a second request.)
//
// mintBootstrapToken is a variable so tests on a machine with the GUI
// toolchain can substitute a fake minter without a helper binary.
var mintBootstrapToken = func(ctx context.Context, verb string) (string, error) {
	return mintBootstrapViaHelper(ctx, verb)
}

// bootstrapEnvelope is the helper's stdout JSON for the bootstrap verb.
type bootstrapEnvelope struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	Remediation string `json:"remediation"`
	Token       string `json:"token"`
	Verb        string `json:"verb"`
}

// parseBootstrapToken validates a helper bootstrap envelope without
// logging any part of it. The token itself is never included in an
// error string.
func parseBootstrapToken(raw []byte, verb string) (string, error) {
	var env bootstrapEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("helper bootstrap: invalid JSON: %w", err)
	}
	if !env.OK {
		if env.Remediation != "" {
			return "", fmt.Errorf("helper bootstrap: %s: %s", env.Error, env.Remediation)
		}
		return "", errors.New("helper bootstrap: " + env.Error)
	}
	if env.Token == "" {
		return "", errors.New("helper bootstrap: no token in response")
	}
	if env.Verb != "" && env.Verb != verb {
		return "", fmt.Errorf("helper bootstrap: token minted for %q, want %q", env.Verb, verb)
	}
	return env.Token, nil
}

// bootstrapCall posts one bootstrap request and decodes the daemon's
// BootstrapResult. fields are the verb's payload (init options, invite,
// or source path); the token is added to the JSON body only.
func bootstrapCall(ctx context.Context, verb, token string, fields map[string]any) (app.BootstrapResult, error) {
	var out app.BootstrapResult
	c, err := ControlClient()
	if err != nil {
		return out, err
	}
	body := map[string]any{"token": token}
	for k, v := range fields {
		body[k] = v
	}
	// The body map (and the token string it references) goes out of
	// scope here; nothing retains it after this call.
	err = c.do(ctx, "POST", "/api/v1/bootstrap/"+verb, body, &out)
	return out, err
}
