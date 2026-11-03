package api

import "net/http"

// postPeerInvite generates a skein:1: peer invite. It carries no network
// secret: it is an introduction, not membership.
func (s *Server) postPeerInvite(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	inv, err := g.PeerInvite(r.Context())
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// postNetworkInvite generates a skein:2: join string. It carries the network
// secret and a snapshot of the overlay addresses in use.
func (s *Server) postNetworkInvite(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	inv, err := g.NetworkInvite(r.Context())
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}
