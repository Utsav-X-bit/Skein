package api

import "net/http"

// patchConfig applies an allowlisted partial update. Strict decoding rejects
// everything outside the allowlist (api.*, network secret, peers, prefixes).
func (s *Server) patchConfig(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	var p ConfigPatch
	if !decodeJSON(r, w, &p) {
		return
	}
	result, err := g.PatchConfig(r.Context(), p)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
