package api

import "net/http"

// getSecurity reports ownership and permission facts the GUI shows as
// notices: config/identity owner and mode, socket ACL uids, update signature
// state, and the firewall manager. No secrets.
func (s *Server) getSecurity(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, g.Security())
}
