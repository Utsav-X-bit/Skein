//go:build windows

package services

import (
	"context"
	"fmt"
	"os"
)

// runHelperJSON runs a retained helper verb through the elevated UAC
// path and returns its JSON result.
//
// M7 (F-07): this channel must never carry a secret. The request file
// lives in %TEMP%, where Windows has no 0600 equivalent, and a
// same-user process could read or replace it while the helper runs;
// the result file is written atomically with a 0600 temp on the
// helper side but the same same-user caveat applies to the final
// path. Audit of the current tree: NO retained GUI caller passes a
// payload at all (service verbs and bootstrap both pass nil), and
// bootstrap refuses --result outright in cmd/skein-helper, so no
// secret travels %TEMP% today — the remaining exposure is the
// channel itself, carrying only non-secret verb results (service
// state). To keep it that way, a non-nil payload is refused here
// outright instead of being written to a temp file: any future
// secret-bearing helper request must use the pipe/stdout channel
// (GUI-created named pipe + a helper --pipe case for retained verbs,
// with VerifyPipeServer treatment), implemented and tested on a
// Windows machine — not smuggled in here untested. See the M7 report
// for the exact remaining exposure and fix plan.
func runHelperJSON(ctx context.Context, verb string, payload any, args ...string) ([]byte, error) {
	// M6 (F-04): helperPath() ignores SKEIN_HELPER in release builds.
	path := helperPath()
	if payload != nil {
		return nil, fmt.Errorf("helper %s: secret-bearing requests are refused over temp files (F-07); use the pipe/stdout channel", verb)
	}
	res, err := os.CreateTemp("", "skein-helper-result-*.json")
	if err != nil {
		return nil, err
	}
	resultPath := res.Name()
	res.Close()
	os.Remove(resultPath)
	defer os.Remove(resultPath)
	return runElevatedHelper(ctx, path, verb, "", resultPath, args...)
}
