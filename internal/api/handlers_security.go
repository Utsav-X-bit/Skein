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

// getAccessGrants reports the live authorisation table and the caller's own
// security facts. It is RoleSecret: the table names every identity that may
// drive the daemon, and a bearer caller is capped at Read by roleFor.
//
// The caller's role is resolved before the snapshot, so the snapshot's lock
// never covers roleFor (which takes it again).
func (s *Server) getAccessGrants(w http.ResponseWriter, r *http.Request) {
	c := CallerFrom(r.Context())
	view := AccessGrantsView{
		Caller: CallerSecurity{
			ID:        c.ID(),
			Transport: transportName(c.Transport),
			Role:      s.roleFor(c).String(),
			IsRoot:    isRootCaller(c),
		},
		Grants: s.snapshotGrants(),
	}
	writeJSON(w, http.StatusOK, view)
}

// transportName is the transport as the security screen shows it.
func transportName(t Transport) string {
	if t == TransportKernel {
		return "kernel"
	}
	return "bearer"
}
