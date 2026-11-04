package api

import "net/http"

// connectionRequest is the body of POST /connection.
type connectionRequest struct {
	Action string `json:"action"`
}

// postConnection pauses or resumes the dataplane. The service keeps running;
// only the TUN/link/routes are affected.
func (s *Server) postConnection(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
		writeError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "too many changes at once", "wait a moment and retry", nil)
		return
	}
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	var req connectionRequest
	if !decodeJSON(r, w, &req) {
		return
	}
	var (
		conn Connection
		err  error
	)
	switch req.Action {
	case "connect":
		conn, err = g.Connect(r.Context())
	case "disconnect":
		conn, err = g.Disconnect(r.Context())
	default:
		writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, `action must be "connect" or "disconnect"`, "", fieldError("action"))
		return
	}
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conn)
}
