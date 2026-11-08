package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/ctlerr"
)

// A plain-text 403 from the bootstrap endpoint (the real daemon writes
// http.Error, not a JSON envelope) must not tell the user to ask for
// the secret role: bootstrap is not role-gated, and the token is bound
// to the uid that approved elevation.
func TestBootstrapDeniedExplainsTokenBinding(t *testing.T) {
	err := bootstrapDenied(ctlerr.New(http.StatusForbidden, "bootstrap: token verification failed"))
	if ctlerr.KindOf(err) != ctlerr.Denied {
		t.Fatalf("kind = %v, want Denied", ctlerr.KindOf(err))
	}
	msg := err.Error()
	if !strings.Contains(msg, "bound to the user who approved") {
		t.Fatalf("403 guidance does not explain the binding: %q", msg)
	}
	if strings.Contains(msg, "grant-user") {
		t.Fatalf("403 guidance still points at a role grant: %q", msg)
	}
	if !strings.Contains(msg, "token verification failed") {
		t.Fatalf("daemon message was lost: %q", msg)
	}
}

// A daemon-supplied remediation is authoritative and must survive.
func TestBootstrapDeniedPreservesDaemonRemediation(t *testing.T) {
	orig := &ctlerr.Error{
		Kind:        ctlerr.Denied,
		Code:        http.StatusForbidden,
		Message:     "forbidden",
		Remediation: "ask an administrator to grant it",
	}
	got := bootstrapDenied(orig)
	if got != orig {
		t.Fatalf("daemon remediation was replaced: %v", got)
	}
}

// Non-403 failures pass through unchanged.
func TestBootstrapDeniedLeavesOtherErrorsAlone(t *testing.T) {
	down := ctlerr.NewDown(errors.New("connection refused"))
	if got := bootstrapDenied(down); got != down {
		t.Fatalf("transport error was rewritten: %v", got)
	}
}
