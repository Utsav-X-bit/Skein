//go:build !windows

package services

import (
	"errors"
	"strings"
	"testing"
)

// The helper's failure is prose inside a JSON envelope. The envelope and the
// exit status must not reach the user.
func TestHelperFailureDecodesEnvelope(t *testing.T) {
	raw := []byte(`{"ok":false,"error":"service: systemctl start: exit status 5: Failed to start skein.service: Unit skein.service not found.","remediation":""}`)
	err := helperFailure("start", errors.New("exit status 1"), raw, nil)
	if got, want := err.Error(), "service: systemctl start: exit status 5: Failed to start skein.service: Unit skein.service not found."; got != want {
		t.Fatalf("error = %q, want the envelope's error field", got)
	}
	if strings.Contains(err.Error(), `"ok"`) {
		t.Fatalf("error = %q, want no JSON envelope", err)
	}
}

// Without an envelope the process error and its output are the only evidence:
// pkexec's exit status is what lets the desktop recognise a dismissed prompt.
func TestHelperFailureKeepsTheProcessErrorWithoutAnEnvelope(t *testing.T) {
	err := helperFailure("start", errors.New("exit status 126"), nil, []byte("Error executing command as another user: Request dismissed"))
	if !strings.Contains(err.Error(), "exit status 126") || !strings.Contains(err.Error(), "Request dismissed") {
		t.Fatalf("error = %q, want the exit status and stderr", err)
	}
}
