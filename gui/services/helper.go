//go:build !windows

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// runHelper invokes the fixed-verb privileged helper. The helper path is
// root-owned; the GUI never passes a free-form shell or a config path.
// It is a variable (not a func) so service-layer tests can substitute
// a fake helper without pkexec/UAC.
var runHelper = func(ctx context.Context, verb string, args ...string) error {
	// M6 (F-04): helperPath() ignores SKEIN_HELPER in release builds.
	path := helperPath()
	cmd := helperCommand(ctx, path, append([]string{verb}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return helperFailure(verb, err, out.Bytes(), errOut.Bytes())
	}
	return nil
}

// helperFailure turns a failed invocation into the error the helper reported.
// The helper writes {"ok":false,"error":...,"remediation":...} on stdout;
// showing that envelope and the surrounding exit status is not something a
// user can act on. A missing envelope (pkexec refused, helper not installed)
// keeps the process error, which is the only evidence there is.
func helperFailure(verb string, runErr error, stdout, stderr []byte) error {
	var env struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Remediation string `json:"remediation"`
	}
	if json.Unmarshal(stdout, &env) == nil && env.Error != "" {
		if env.Remediation != "" {
			return fmt.Errorf("%s: %s", env.Error, env.Remediation)
		}
		return errors.New(env.Error)
	}
	detail := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(string(stdout)),
		strings.TrimSpace(string(stderr)),
	}, " "))
	if detail != "" {
		return fmt.Errorf("helper %s: %w: %s", verb, runErr, detail)
	}
	return fmt.Errorf("helper %s: %w", verb, runErr)
}
