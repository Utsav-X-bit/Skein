package api

import (
	"net/http"
	"strings"
)

// Step 7 system handlers. Every route is registered with its plan
// §2.3 role in Server.Handler; the gate lives in wrapRole, not
// here. Handlers only decode, enforce confirm where the plan
// requires it (rotate/reset/leave/update apply), and map daemon
// errors. Confirm is enforced twice — here (400 before any daemon
// work) and in the daemon (so a direct daemon caller cannot skip
// it either).

// system resolves the SystemOperator behind the state reader.
func (s *Server) system(w http.ResponseWriter, r *http.Request) (SystemOperator, bool) {
	op, ok := s.state.(SystemOperator)
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, ErrCodeDaemonUnavailable, "daemon does not serve the system endpoints", "restart the skein service", nil)
	}
	return op, ok
}

func (s *Server) postRotateSecret(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(r, w, &req) {
		return
	}
	if !req.Confirm {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "rotate-secret requires confirm:true", "show the \"every peer must rejoin\" warning, then retry with confirm:true", fieldError("confirm"))
		return
	}
	res, err := op.RotateSecret(r.Context(), true)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) postReset(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	var req ResetOptions
	if !decodeJSON(r, w, &req) {
		return
	}
	if !req.Confirm {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "reset requires confirm:true", "retry with confirm:true", fieldError("confirm"))
		return
	}
	res, err := op.ResetSystem(r.Context(), req)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) postLeave(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(r, w, &req) {
		return
	}
	if !req.Confirm {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "leave requires confirm:true", "retry with confirm:true", fieldError("confirm"))
		return
	}
	res, err := op.LeaveSystem(r.Context(), true)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) postSystemInit(w http.ResponseWriter, r *http.Request) {
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	// The body is ignored: there is no successful normal-mode init
	// to parameterise, and the daemon answers 409 unconditionally.
	if err := op.SystemInit(r.Context()); err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "already configured"})
}

func (s *Server) postSystemJoin(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	var req struct {
		Invite  string `json:"invite"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeJSON(r, w, &req) {
		return
	}
	if strings.TrimSpace(req.Invite) == "" {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "invite is required", "provide a skein:2: network invite in the body", fieldError("invite"))
		return
	}
	res, err := op.SystemJoin(r.Context(), strings.TrimSpace(req.Invite), req.Confirm)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) postSystemImport(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	var req struct {
		Source  string `json:"source"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeJSON(r, w, &req) {
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "source is required", "provide the config file path to import", fieldError("source"))
		return
	}
	if !req.Confirm {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "import requires confirm:true", "retry with confirm:true", fieldError("confirm"))
		return
	}
	res, err := op.SystemImport(r.Context(), strings.TrimSpace(req.Source), true)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) getUpdateCheck(w http.ResponseWriter, r *http.Request) {
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	// GET carries no body: the manifest path is a query parameter.
	// It is a local filesystem path, not a secret, and the audit
	// hook records r.URL.Path only, never the query string.
	manifest := strings.TrimSpace(r.URL.Query().Get("manifest_path"))
	if manifest == "" {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "manifest_path query parameter is required", "pass ?manifest_path=<path to the signed manifest>", fieldError("manifest_path"))
		return
	}
	res, err := op.UpdateCheck(r.Context(), manifest)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) postUpdateApply(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	var req struct {
		ManifestPath     string `json:"manifest_path"`
		StagedBinaryPath string `json:"staged_binary_path"`
		Confirm          bool   `json:"confirm"`
	}
	if !decodeJSON(r, w, &req) {
		return
	}
	if strings.TrimSpace(req.ManifestPath) == "" {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "manifest_path is required", "provide the signed manifest path", fieldError("manifest_path"))
		return
	}
	if !req.Confirm {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "update apply requires confirm:true", "retry with confirm:true", fieldError("confirm"))
		return
	}
	res, err := op.UpdateApply(r.Context(), strings.TrimSpace(req.ManifestPath), strings.TrimSpace(req.StagedBinaryPath), true)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) postGrantsReload(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	op, ok := s.system(w, r)
	if !ok {
		return
	}
	// No body: reload re-reads the access file the daemon was
	// constructed with. A missing file is not an error (the table
	// becomes unconfigured, i.e. kernel callers read-only); a
	// malformed file is an error and the old table stays in place.
	if err := op.ReloadGrants(r.Context()); err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "grants reloaded"})
}
