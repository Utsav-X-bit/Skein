package api

import "net/http"

// RegisterRoutes adds the desktop contract endpoints. The server core keeps
// its original registrations and calls this once during Handler setup; the
// two overlapping original routes (probe, events) are superseded here.
// RegisterRoutes adds the desktop contract endpoints. Role classification
// matches Handler(): reads are RoleRead, state changes are RoleMutate,
// and postNetworkInvite is RoleSecret (kernel transport only, because it
// returns the live network secret).
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	if _, ok := s.state.(GUI); !ok {
		// Tests and legacy in-process handlers do not implement the desktop
		// contract; keep the original probe/events behavior.
		mux.HandleFunc("POST /api/v1/peers/{id}/probe", s.wrapRole(RoleMutate, s.probePeer))
		mux.HandleFunc("GET /api/v1/events", s.wrapRole(RoleRead, s.getEvents))
		return
	}
	mux.HandleFunc("GET /api/v1/capabilities", s.wrapRole(RoleRead, s.getCapabilities))
	mux.HandleFunc("GET /api/v1/identity", s.wrapRole(RoleRead, s.getIdentity))
	mux.HandleFunc("PATCH /api/v1/config", s.wrapRole(RoleMutate, s.patchConfig))
	mux.HandleFunc("POST /api/v1/connection", s.wrapRole(RoleMutate, s.postConnection))
	mux.HandleFunc("POST /api/v1/peers/{id}/probe", s.wrapRole(RoleMutate, s.postProbe))
	mux.HandleFunc("POST /api/v1/invites/peer", s.wrapRole(RoleMutate, s.postPeerInvite))
	mux.HandleFunc("POST /api/v1/invites/network", s.wrapRole(RoleSecret, s.postNetworkInvite))
	mux.HandleFunc("GET /api/v1/logs", s.wrapRole(RoleRead, s.getLogs))
	mux.HandleFunc("GET /api/v1/events", s.wrapRole(RoleRead, s.streamEvents))
	mux.HandleFunc("GET /api/v1/diagnostics", s.wrapRole(RoleRead, s.getDiagnostics))
	mux.HandleFunc("GET /api/v1/security", s.wrapRole(RoleRead, s.getSecurity))
	mux.HandleFunc("GET /api/v1/firewall", s.wrapRole(RoleRead, s.getFirewall))
	mux.HandleFunc("POST /api/v1/firewall/repair", s.wrapRole(RoleMutate, s.repairFirewall))
}
