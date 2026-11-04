//go:build !windows

package services

import "context"

// mintBootstrapViaHelper mints a bootstrap token through the helper's
// stdout channel: pkexec captures the helper's stdout, so the token
// travels from the elevated helper to the GUI without touching disk.
// The result-file channel is never used for bootstrap (the helper
// refuses --result for this verb by design).
func mintBootstrapViaHelper(ctx context.Context, verb string) (string, error) {
	raw, runErr := runHelperJSON(ctx, "bootstrap", nil, "--verb", verb)
	if runErr != nil {
		return "", runErr
	}
	return parseBootstrapToken(raw, verb)
}
