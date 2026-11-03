package api

import "net/http"

// RegisterRoutes adds the desktop contract endpoints. The server core keeps
// its original registrations and calls this once during Handler setup; the
// two overlapping original routes (probe, events) are superseded here.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	if _, ok := s.state.(GUI); !ok {
		// Tests and legacy in-process handlers do not implement the desktop
		// contract; keep the original probe/events behavior.
		mux.HandleFunc("POST /api/v1/peers/{id}/probe", s.wrap(s.probePeer))
		mux.HandleFunc("GET /api/v1/events", s.wrap(s.getEvents))
		return
	}
	mux.HandleFunc("GET /api/v1/capabilities", s.wrap(s.getCapabilities))
	mux.HandleFunc("GET /api/v1/identity", s.wrap(s.getIdentity))
	mux.HandleFunc("PATCH /api/v1/config", s.wrap(s.patchConfig))
	mux.HandleFunc("POST /api/v1/connection", s.wrap(s.postConnection))
	mux.HandleFunc("POST /api/v1/peers/{id}/probe", s.wrap(s.postProbe))
	mux.HandleFunc("POST /api/v1/invites/peer", s.wrap(s.postPeerInvite))
	mux.HandleFunc("POST /api/v1/invites/network", s.wrap(s.postNetworkInvite))
	mux.HandleFunc("GET /api/v1/logs", s.wrap(s.getLogs))
	mux.HandleFunc("GET /api/v1/events", s.wrap(s.streamEvents))
	mux.HandleFunc("GET /api/v1/diagnostics", s.wrap(s.getDiagnostics))
	mux.HandleFunc("GET /api/v1/security", s.wrap(s.getSecurity))
	mux.HandleFunc("GET /api/v1/firewall", s.wrap(s.getFirewall))
	mux.HandleFunc("POST /api/v1/firewall/repair", s.wrap(s.repairFirewall))
}
