package api

import "net/http"

// getDiagnostics returns the redacted support bundle. It never includes the
// raw config file; the config view has secrets replaced.
func (s *Server) getDiagnostics(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, g.Diagnostics())
}
