package api

import (
	"fmt"
	"net/http"
	"strconv"
)

// streamEvents is the SSE endpoint. It streams hub events with monotonic IDs
// and replays from Last-Event-ID when the ring still holds them. Slow
// consumers are disconnected by the hub; W1's server core enforces the
// per-listener stream cap via reserveSSE.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	release, ok := s.reserveSSE()
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, ErrCodeUnsupported, "too many open event streams", "close other event streams and retry", nil)
		return
	}
	defer release()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, ErrCodeUnsupported, "streaming unsupported", "", nil)
		return
	}
	var lastID uint64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "Last-Event-ID must be an integer", "", nil)
			return
		}
		lastID = id
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, cancel := g.Hub().Subscribe(lastID)
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return // dropped as a slow consumer
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
