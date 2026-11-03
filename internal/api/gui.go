package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

// GUI is the daemon-side contract for the desktop endpoints. It extends
// StateReader with mutations and derived views; the daemon passes itself as
// the StateReader and handlers find it through a type assertion, so the
// server core needs no extra fields.
type GUI interface {
	Capabilities() Capabilities
	Identity() Identity
	Connection() Connection
	Connect(ctx context.Context) (Connection, error)
	Disconnect(ctx context.Context) (Connection, error)
	PatchConfig(ctx context.Context, p ConfigPatch) (ConfigPatchResult, error)
	PeerInvite(ctx context.Context) (Invite, error)
	NetworkInvite(ctx context.Context) (Invite, error)
	Logs(since uint64, limit int) (entries []LogEntry, next uint64, err error)
	Diagnostics() DiagnosticBundle
	Security() SecurityStatus
	Firewall() FirewallStatus
	RepairFirewall(ctx context.Context) (FirewallStatus, error)
	Probe(ctx context.Context, id uint64) (ProbeResult, error)
	Hub() *Hub
}

// gui resolves the GUI implementation behind the state reader.
func (s *Server) gui(w http.ResponseWriter, r *http.Request) (GUI, bool) {
	g, ok := s.state.(GUI)
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, ErrCodeDaemonUnavailable, "daemon does not serve the GUI endpoints", "restart the skein service", nil)
	}
	return g, ok
}

// Error codes from the desktop API contract.
const (
	ErrCodeDaemonUnavailable = "daemon_unavailable"
	ErrCodePermissionDenied  = "permission_denied"
	ErrCodeNotFound          = "not_found"
	ErrCodeConflict          = "conflict"
	ErrCodeValidationFailed  = "validation_failed"
	ErrCodeRestartRequired   = "restart_required"
	ErrCodeUnsupported       = "unsupported"
	ErrCodeRateLimited       = "rate_limited"
	ErrCodeInternal          = "internal"
)

// writeError renders the {code,message,remediation,details,request_id}
// envelope.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message, remediation string, details map[string]string) {
	id := make([]byte, 4)
	_, _ = rand.Read(id)
	writeJSON(w, status, APIError{
		Code:        code,
		Message:     message,
		Remediation: remediation,
		Details:     details,
		RequestID:   hex.EncodeToString(id),
	})
}

// fieldError builds the details map for a single failed field.
func fieldError(field string) map[string]string { return map[string]string{"field": field} }

// decodeJSON strictly decodes a JSON body of at most 16 KiB, rejecting
// unknown fields so a patch cannot smuggle in api.*/secret/peer fields.
func decodeJSON(r *http.Request, w http.ResponseWriter, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "invalid JSON body", err.Error(), nil)
		return false
	}
	return true
}

// StatusError lets a daemon-side error choose its own envelope and status.
type StatusError struct {
	Status      int
	Code        string
	Message     string
	Remediation string
	Details     map[string]string
}

func (e *StatusError) Error() string { return e.Message }

// writeDaemonError maps a GUI implementation error to the response.
func writeDaemonError(w http.ResponseWriter, r *http.Request, err error) {
	var se *StatusError
	if errors.As(err, &se) {
		writeError(w, r, se.Status, se.Code, se.Message, se.Remediation, se.Details)
		return
	}
	writeError(w, r, http.StatusBadGateway, ErrCodeInternal, err.Error(), "check the daemon logs", nil)
}
