//go:build windows

package services

import (
	"bytes"
	"context"
	"fmt"
)

// mintBootstrapViaHelper mints a bootstrap token through the helper's
// stdout channel only — the channel that exists today (Step 4 open
// item). The Windows GUI's usual elevated path (UAC runas + temp
// result file) cannot carry a bootstrap token: the helper refuses
// --result for this verb outright, so that path would fail, not leak.
// This implementation runs the helper with captured stdout, which
// works when the GUI process is already elevated (first-setup run
// elevated, or the installer launches it so). When it is not
// elevated, the helper's privilege check fails and the error says
// so plainly, with the elevated-CLI alternative — it never falls
// back to a result file or an unverified channel. The pipe variant
// (GUI-created named pipe, Step 4's other recommended option) is
// the follow-up that would give unelevated Windows GUI first-setup
// a UAC flow; it is recorded in the Step 8 report, not invented
// here untested.
func mintBootstrapViaHelper(ctx context.Context, verb string) (string, error) {
	// M6 (F-04): helperPath() ignores SKEIN_HELPER in release builds.
	path := helperPath()
	cmd := helperCommand(ctx, path, "bootstrap", "--verb", verb)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		// The helper writes its JSON envelope to stdout even on
		// failure (e.g. the privilege check); surface that first.
		if _, perr := parseBootstrapToken(out.Bytes(), verb); perr != nil {
			return "", fmt.Errorf("%w (first setup on Windows needs an elevated desktop app, or run `skein system init` from an elevated prompt)", perr)
		}
		if errb.Len() > 0 {
			return "", fmt.Errorf("helper bootstrap: %w: %s", err, errb.String())
		}
		return "", fmt.Errorf("helper bootstrap: %w", err)
	}
	return parseBootstrapToken(out.Bytes(), verb)
}
