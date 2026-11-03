package api

import "net/http"

// postProbe starts an async path probe. The response reports pending
// immediately; the result arrives as a peer:probe event.
func (s *Server) postProbe(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	id, err := parsePeerID(r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, err.Error(), "", fieldError("id"))
		return
	}
	res, err := g.Probe(r.Context(), id)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
