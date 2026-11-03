package api

import "net/http"

// getFirewall reports the manager, its state, the rules Skein manages, and
// the last check. Status is best-effort: a missing manager reports state
// "missing" rather than an error.
func (s *Server) getFirewall(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, g.Firewall())
}

// repairFirewall re-applies the managed rules transactionally (add before
// remove, unique rule names, rollback on failure).
func (s *Server) repairFirewall(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	// Failure is reported in the status card (last_error), so a failed
	// repair still returns the status, not an envelope.
	st, _ := g.RepairFirewall(r.Context())
	writeJSON(w, http.StatusOK, st)
}
