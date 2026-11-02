package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StateReader supplies read-only state to the API.
type StateReader interface {
	Status() Status
	Peers() []Peer
	Routes() []Route
	Activities() []Activity
	Config() ConfigView
	Events() <-chan Event
}

// PeerWriter applies peer mutations.
type PeerWriter interface {
	AddPeer(invite string) error
	RemovePeer(id uint64) error
	SetName(name string) error
}

// Prober forces a path probe.
type Prober interface {
	ProbePeer(ctx context.Context, id uint64) error
}

// Server is the authenticated loopback API.
type Server struct {
	token  []byte
	state  StateReader
	writer PeerWriter
	prober Prober

	mu       sync.Mutex
	lastCall time.Time
	burst    int
}

// Bearer renders the Authorization value for a raw API token. Tokens travel
// as base64url text so the header stays printable.
func Bearer(token []byte) string {
	return "Bearer " + base64.RawURLEncoding.EncodeToString(token)
}

// NewServer creates the API handler set.
func NewServer(token []byte, state StateReader, writer PeerWriter, prober Prober) *Server {
	return &Server{token: append([]byte(nil), token...), state: state, writer: writer, prober: prober}
}

// Handler returns the authenticated HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.wrap(s.getStatus))
	mux.HandleFunc("GET /api/v1/peers", s.wrap(s.getPeers))
	mux.HandleFunc("POST /api/v1/peers", s.wrap(s.postPeer))
	mux.HandleFunc("DELETE /api/v1/peers/{id}", s.wrap(s.deletePeer))
	mux.HandleFunc("POST /api/v1/peers/{id}/probe", s.wrap(s.probePeer))
	mux.HandleFunc("GET /api/v1/routes", s.wrap(s.getRoutes))
	mux.HandleFunc("GET /api/v1/activities", s.wrap(s.getActivities))
	mux.HandleFunc("GET /api/v1/config", s.wrap(s.getConfig))
	mux.HandleFunc("GET /api/v1/events", s.wrap(s.getEvents))
	mux.HandleFunc("POST /api/v1/name", s.wrap(s.postName))
	return mux
}

type handlerFunc func(http.ResponseWriter, *http.Request)

// wrap enforces bearer authentication, rejects browser origins, and sends no
// CORS headers.
func (s *Server) wrap(next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not allowed", http.StatusForbidden)
			return
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) || len(s.token) == 0 {
		return false
	}
	got := strings.TrimPrefix(header, prefix)
	want := base64.RawURLEncoding.EncodeToString(s.token)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.state.Status())
}

func (s *Server) getPeers(w http.ResponseWriter, r *http.Request) {
	peers := s.state.Peers()
	if peers == nil {
		peers = []Peer{}
	}
	writeJSON(w, http.StatusOK, peers)
}

func (s *Server) postPeer(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req AddPeerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Invite == "" {
		http.Error(w, "invite is required", http.StatusBadRequest)
		return
	}
	if err := s.writer.AddPeer(req.Invite); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deletePeer(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	id, err := parsePeerID(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.writer.RemovePeer(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) probePeer(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	id, err := parsePeerID(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.prober.ProbePeer(ctx, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postName(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation() {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req SetNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := s.writer.SetName(req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getRoutes(w http.ResponseWriter, r *http.Request) {
	routes := s.state.Routes()
	if routes == nil {
		routes = []Route{}
	}
	writeJSON(w, http.StatusOK, routes)
}

func (s *Server) getActivities(w http.ResponseWriter, r *http.Request) {
	activities := s.state.Activities()
	if activities == nil {
		activities = []Activity{}
	}
	writeJSON(w, http.StatusOK, activities)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.state.Config())
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events := s.state.Events()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			body, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// allowMutation applies a one-per-second token bucket to state-changing calls.
func (s *Server) allowMutation() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.lastCall.IsZero() || now.Sub(s.lastCall) >= time.Second {
		s.lastCall = now
		s.burst = 0
		return true
	}
	s.burst++
	return s.burst <= 4
}

func parsePeerID(s string) (uint64, error) {
	id, err := strconv.ParseUint(s, 16, 64)
	if err != nil || id == 0 {
		return 0, errors.New("peer id must be 16 hexadecimal digits")
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
