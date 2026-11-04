package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// callerKey stores the kernel-derived caller identity in the request context.
type callerKey struct{}

// Transport names how a caller authenticated. Kernel means the identity
// came from the kernel (SO_PEERCRED on the Unix socket, the client token
// on the Windows pipe); Bearer means a dev-TCP client presented the raw
// API token. The distinction is the authorisation gate: a bearer caller
// never exceeds Read, however valid its token is (see roleFor).
type Transport int

const (
	// TransportKernel is the zero value, so callers built without an
	// explicit transport (tests, older code) are kernel callers.
	TransportKernel Transport = iota
	TransportBearer
)

// Caller identifies the process that opened a control connection. The
// identity comes from the kernel (SO_PEERCRED on the Unix socket, the
// impersonated client token on the Windows pipe), never from anything the
// client sends.
type Caller struct {
	// UID is the peer's real uid on Unix; -1 when unknown.
	UID int
	// SID is the client's user SID on Windows; "" when unknown.
	SID string
	// Transport is how the caller authenticated.
	Transport Transport
}

// Known reports whether the transport derived a caller identity.
func (c Caller) Known() bool { return c.UID >= 0 || c.SID != "" }

// ID is a stable, log-safe label for audit and rate limiting. It never
// carries a secret.
func (c Caller) ID() string {
	switch {
	case c.SID != "":
		return c.SID
	case c.UID >= 0:
		return fmt.Sprintf("uid:%d", c.UID)
	case c.Transport == TransportBearer:
		return "bearer"
	default:
		return "unknown"
	}
}

// WithCaller returns ctx carrying the given caller identity.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller stored in ctx, if any.
func CallerFrom(ctx context.Context) Caller {
	c, ok := ctx.Value(callerKey{}).(Caller)
	if !ok {
		return Caller{UID: -1}
	}
	return c
}

// AuditEvent records one mutating request, or (Step 5) one denied
// authorisation attempt. Caller is a kernel identity (uid or SID),
// not a secret.
type AuditEvent struct {
	Time   time.Time
	Caller string
	Method string
	Path   string
	Status int
	// Secret marks a secret-endpoint mutation (e.g. network-invite
	// generation). The record never carries the secret itself; Secret
	// only says a secret was disclosed, so the log can be audited
	// without leaking it.
	Secret bool
	// Denied marks an authorisation denial (Step 5): the caller was
	// authenticated but held too low a role for the route.
	// Required is the role the route demanded, Held is the role the
	// caller held. Neither field ever carries a token, secret, or
	// request body.
	Denied   bool
	Required Role
	Held     Role
}

// authorized reports whether r may proceed. A request carrying a kernel
// caller (Unix socket or Windows pipe) is authenticated by its connection
// and bypasses the bearer token; any other request (loopback TCP) must
// present the token.
func (s *Server) authorized(r *http.Request) bool {
	if c := CallerFrom(r.Context()); c.Known() {
		return true
	}
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || len(s.token) == 0 {
		return false
	}
	got := strings.TrimPrefix(header, prefix)
	want := base64.RawURLEncoding.EncodeToString(s.token)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// CallerContext attaches kernel-derived caller identity to a connection's
// context. It is used as http.Server.ConnContext for UDS/pipe listeners.
func CallerContext(ctx context.Context, c net.Conn) context.Context {
	if cc, ok := c.(interface{ ConnContext() context.Context }); ok {
		return cc.ConnContext()
	}
	return ctx
}
