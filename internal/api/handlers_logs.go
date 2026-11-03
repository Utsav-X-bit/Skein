package api

import (
	"net/http"
	"strconv"
)

// logPage is the GET /logs response.
type logPage struct {
	Entries []LogEntry `json:"entries"`
	Next    uint64     `json:"next"`
}

const (
	defaultLogLimit = 100
	maxLogLimit     = 1000
)

// getLogs serves the bounded redacted log tail. since is the sequence cursor
// from a previous response; only entries after it are returned.
func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	g, ok := s.gui(w, r)
	if !ok {
		return
	}
	var since uint64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "since must be an integer", "", fieldError("since"))
			return
		}
		since = n
	}
	limit := defaultLogLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLogLimit {
			writeError(w, r, http.StatusBadRequest, ErrCodeValidationFailed, "limit must be 1-1000", "", fieldError("limit"))
			return
		}
		limit = n
	}
	entries, next, err := g.Logs(since, limit)
	if err != nil {
		writeDaemonError(w, r, err)
		return
	}
	if entries == nil {
		entries = []LogEntry{}
	}
	writeJSON(w, http.StatusOK, logPage{Entries: entries, Next: next})
}
