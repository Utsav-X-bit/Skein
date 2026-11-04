// Package ctlerr classifies control-plane failures the same way for every
// caller (GUI services, CLI) — M5 Step 8, plan §8.1/§5. Three states, each
// with its own remediation; a generic "failed" is never enough:
//
//   - Unauthenticated (401): the daemon did not recognise the caller at
//     all (no grant, unknown caller). Remediation: get granted.
//   - Denied (403): the caller is recognised but the role is too low, or
//     the transport may not carry this endpoint. Remediation: get a
//     higher role from an administrator; do not retry unchanged.
//   - DaemonDown: no control transport answered at all (socket/pipe
//     missing, connection refused, verification failed at dial).
//     Remediation: start the service.
//   - Locked (423): the config is held by another daemon process.
//     Remediation: stop the other daemon, then retry.
//   - Other: everything else (400 validation, 409 conflict, 500, ...);
//     the daemon's own message + remediation are authoritative there.
package ctlerr

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind is one of the control-plane failure classes.
type Kind int

const (
	Other Kind = iota
	Unauthenticated
	Denied
	DaemonDown
	Locked
)

// String is the short label UIs and CLIs print before the message.
func (k Kind) String() string {
	switch k {
	case Unauthenticated:
		return "unauthenticated"
	case Denied:
		return "access denied"
	case DaemonDown:
		return "daemon offline"
	case Locked:
		return "config locked"
	default:
		return "daemon error"
	}
}

// ClassifyStatus maps an HTTP status to its Kind. Transport failures are
// not statuses; callers map those to DaemonDown themselves.
func ClassifyStatus(code int) Kind {
	switch code {
	case http.StatusUnauthorized:
		return Unauthenticated
	case http.StatusForbidden:
		return Denied
	case http.StatusLocked:
		return Locked
	default:
		return Other
	}
}

// Remediation is the distinct next step for a Kind. It never names a
// secret and never suggests retrying a denied call unchanged.
func Remediation(k Kind) string {
	switch k {
	case Unauthenticated:
		return "the daemon did not recognise this user; ask an administrator to grant access (skein-helper grant-user <uid>) and retry"
	case Denied:
		return "this action needs a higher role than this user holds; ask an administrator to grant it (skein-helper grant-user <uid> --role secret) and retry — retrying unchanged will fail again"
	case DaemonDown:
		return "the daemon is not answering; start the service (skein-helper start, systemctl start skein, or Start-Service skein) and retry"
	case Locked:
		return "another daemon holds this config; stop it first, then retry"
	default:
		return ""
	}
}

// Error is a classified control-plane error. Message is the daemon's own
// message (or the transport error); Remediation is filled from Kind when
// the caller does not supply a better one. Code is the HTTP status when
// known (0 for transport errors); it is kept in the text so a caller
// can still match on "401"/"403" as before.
type Error struct {
	Kind        Kind
	Code        int
	Message     string
	Remediation string
}

func (e *Error) Error() string {
	rem := e.Remediation
	if rem == "" {
		rem = Remediation(e.Kind)
	}
	head := e.Kind.String()
	if e.Code != 0 {
		head = fmt.Sprintf("%s (%d)", head, e.Code)
	}
	if rem != "" {
		return fmt.Sprintf("%s: %s: %s", head, e.Message, rem)
	}
	return fmt.Sprintf("%s: %s", head, e.Message)
}

// New builds a classified error from an HTTP status and message.
func New(code int, message string) *Error {
	return &Error{Kind: ClassifyStatus(code), Code: code, Message: message}
}

// NewDown builds a DaemonDown error from a transport failure.
func NewDown(err error) *Error {
	return &Error{Kind: DaemonDown, Message: err.Error()}
}

// KindOf unwraps an error to its Kind, or Other when it is not a
// classified control error.
func KindOf(err error) Kind {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return Other
}
