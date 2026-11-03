package api

import "net/http"

// getCapabilities reports the protocol and feature flags the desktop uses to
// gate UI. Feature flags describe this release's supported set.
func (s *Server) getCapabilities(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, g.Capabilities())
}

// getIdentity returns the local node's public identity. The private key and
// network secret never leave the daemon.
func (s *Server) getIdentity(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, g.Identity())
}
