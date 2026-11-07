//go:build windows

package services

import (
	"bytes"
	"context"
	"fmt"
)

// mintBootstrapViaHelper mints a bootstrap token through the helper.
//
// An already-elevated desktop captures the helper's stdout directly. A
// normal-token desktop cannot: UAC does not carry inherited stdio across
// the elevation boundary, and the helper refuses --result for bootstrap so
// the token never lands in a temp file. It uses the GUI-created named pipe
// instead: the desktop creates a random, user-only pipe, launches the
// helper through UAC, the helper verifies the pipe server is the desktop,
// mints the token and writes the JSON envelope back over that pipe.
func mintBootstrapViaHelper(ctx context.Context, verb string) (string, error) {
	if !currentProcessElevated() {
		return mintBootstrapViaPipe(ctx, verb)
	}
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
