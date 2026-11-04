package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// PeerWriter applies peer mutations. Step 12 (ADR-002): there is no
// peer-removal method — no peer removes another peer. The only
// departure path is a peer leaving itself via POST /api/v1/system/leave.
type PeerWriter interface {
	AddPeer(invite string) error
	SetName(name string) error
}

// Prober forces a path probe.
type Prober interface {
	ProbePeer(ctx context.Context, id uint64) error
}

// DefaultMaxConns bounds concurrent requests across all control transports
// (REST and SSE combined); DefaultMaxSSE bounds simultaneous event streams.
const (
	DefaultMaxConns = 64
	DefaultMaxSSE   = 8
)

// Server is the authenticated control API.
type Server struct {
	token  []byte
	state  StateReader
	writer PeerWriter
	prober Prober

	// audit is invoked after each mutating request; nil disables auditing.
	audit func(AuditEvent)

	// grants is the authorisation table (caller ID -> role). Nil means
	// "not configured": kernel callers get Read only (see roleFor).
	// Guarded by mu.
	mu     sync.RWMutex
	grants map[string]Role

	// Limits. maxConns counts in-flight requests across every transport;
	// maxSSE counts open event streams. Set before serving starts.
	maxConns int
	maxSSE   int
	inflight int64
	sseCount int64

	lastCall time.Time

	callerMu sync.Mutex
	callers  map[string]*callerLimiter
}

// Bearer renders the Authorization value for a raw API token. Tokens travel
// as base64url text so the header stays printable.
func Bearer(token []byte) string {
	return "Bearer " + base64.RawURLEncoding.EncodeToString(token)
}

// NewServer creates the API handler set.
func NewServer(token []byte, state StateReader, writer PeerWriter, prober Prober) *Server {
	return &Server{
		token:    append([]byte(nil), token...),
		state:    state,
		writer:   writer,
		prober:   prober,
		maxConns: DefaultMaxConns,
		maxSSE:   DefaultMaxSSE,
		callers:  make(map[string]*callerLimiter),
	}
}

// Audit installs the hook invoked after each mutating request (POST, PUT,
// PATCH, DELETE) with the caller identity, path, and response code,
// and (Step 5) after each denied authorisation attempt.
func (s *Server) Audit(fn func(AuditEvent)) { s.audit = fn }

// Limits caps concurrent requests (conns) and simultaneous event streams
// (sse). Non-positive values keep the defaults. Call before serving starts.
func (s *Server) Limits(conns, sse int) {
	if conns > 0 {
		s.maxConns = conns
	}
	if sse > 0 {
		s.maxSSE = sse
	}
}

// ConnectionCap is the in-flight request cap, for transports that need to
// match it (the Windows pipe's connection bound).
func (s *Server) ConnectionCap() int { return s.maxConns }

// reserveSSE claims one event-stream slot and returns its release function;
// ok is false when the cap is reached.
func (s *Server) reserveSSE() (release func(), ok bool) {
	if atomic.AddInt64(&s.sseCount, 1) > int64(s.maxSSE) {
		atomic.AddInt64(&s.sseCount, -1)
		return nil, false
	}
	return func() { atomic.AddInt64(&s.sseCount, -1) }, true
}

// Handler returns the authenticated HTTP handler. The role table is the
// Step 1 authorisation matrix: every route is registered with the
// minimum role it requires, and the gate lives in wrapRole, not in the
// handlers. GUI routes are classified the same way in RegisterRoutes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.wrapRole(RoleRead, s.getStatus))
	mux.HandleFunc("GET /api/v1/peers", s.wrapRole(RoleRead, s.getPeers))
	mux.HandleFunc("POST /api/v1/peers", s.wrapRole(RoleMutate, s.postPeer))
	// Step 12: no peer-removal route here — removed, not deprecated.
	// A peer leaves itself via POST /api/v1/system/leave (ADR-002).
	mux.HandleFunc("GET /api/v1/routes", s.wrapRole(RoleRead, s.getRoutes))
	mux.HandleFunc("GET /api/v1/activities", s.wrapRole(RoleRead, s.getActivities))
	mux.HandleFunc("GET /api/v1/config", s.wrapRole(RoleRead, s.getConfig))
	mux.HandleFunc("POST /api/v1/name", s.wrapRole(RoleMutate, s.postName))
	// Step 7 system endpoints (plan §2.3). Roles are the gate:
	// rotate/reset/update/grants-reload disclose or destroy
	// membership material or change who holds secret access, so
	// they are secret; leave/join are membership mutations, so
	// mutate; import replaces config+identity, so secret; init
	// is secret-gated even though it always answers 409 on a
	// configured daemon, so an unprivileged caller learns nothing
	// about the node's state beyond the 403 itself.
	mux.HandleFunc("POST /api/v1/system/rotate-secret", s.wrapRole(RoleSecret, s.postRotateSecret))
	mux.HandleFunc("POST /api/v1/system/reset", s.wrapRole(RoleSecret, s.postReset))
	mux.HandleFunc("POST /api/v1/system/leave", s.wrapRole(RoleMutate, s.postLeave))
	mux.HandleFunc("POST /api/v1/system/init", s.wrapRole(RoleSecret, s.postSystemInit))
	mux.HandleFunc("POST /api/v1/system/join", s.wrapRole(RoleMutate, s.postSystemJoin))
	mux.HandleFunc("POST /api/v1/system/import", s.wrapRole(RoleSecret, s.postSystemImport))
	mux.HandleFunc("GET /api/v1/system/update/check", s.wrapRole(RoleSecret, s.getUpdateCheck))
	mux.HandleFunc("POST /api/v1/system/update/apply", s.wrapRole(RoleSecret, s.postUpdateApply))
	mux.HandleFunc("POST /api/v1/system/grants/reload", s.wrapRole(RoleSecret, s.postGrantsReload))
	s.RegisterRoutes(mux)
	return mux
}

type handlerFunc func(http.ResponseWriter, *http.Request)

// wrapRole is the single transport-layer authorisation gate. It
// authenticates (kernel caller or bearer token), rejects browser
// origins, sends no CORS headers, caps concurrent requests, then checks
// the caller's role against the route's required role before the handler
// runs, so a denied caller never reaches handler code or secret material.
// A bearer caller is injected into the request context as TransportBearer,
// so handlers and the audit hook see "bearer" instead of the old shared
// "unknown" bucket. Step 5: a denied attempt IS audit-logged (caller,
// method, path, required vs held role, 403) through the same hook —
// never a token, secret, or request body — so a probing low-role
// caller is visible in the daemon log. Unauthenticated requests
// (401) are not audit-logged: there is no authenticated caller to
// attribute, and logging every unauthenticated probe would let an
// unauthenticated client fill the log.
func (s *Server) wrapRole(required Role, next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not allowed", http.StatusForbidden)
			return
		}
		n := atomic.AddInt64(&s.inflight, 1)
		if n > int64(s.maxConns) {
			atomic.AddInt64(&s.inflight, -1)
			http.Error(w, "too many connections", http.StatusServiceUnavailable)
			return
		}
		defer atomic.AddInt64(&s.inflight, -1)

		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		caller := CallerFrom(r.Context())
		if !caller.Known() {
			// Authenticated, so this must be a valid bearer token.
			caller = Caller{UID: -1, Transport: TransportBearer}
		}
		r = r.WithContext(WithCaller(r.Context(), caller))
		if role := s.roleFor(caller); role < required {
			// Step 5 (Step 1 open item): denied-attempt audit.
			// The record carries identities and roles only —
			// no token, no secret, no request body (the body
			// has not even been read at this point).
			if s.audit != nil {
				s.audit(AuditEvent{
					Time:     time.Now(),
					Caller:   caller.ID(),
					Method:   r.Method,
					Path:     r.URL.Path,
					Status:   http.StatusForbidden,
					Secret:   required == RoleSecret,
					Denied:   true,
					Required: required,
					Held:     role,
				})
			}
			http.Error(w, "forbidden: "+required.String()+" role required; ask an administrator to grant it", http.StatusForbidden)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		next(rec, r)

		if s.audit != nil && isMutation(r.Method) {
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			s.audit(AuditEvent{
				Time:   time.Now(),
				Caller: caller.ID(),
				Method: r.Method,
				Path:   r.URL.Path,
				Status: status,
				Secret: required == RoleSecret,
			})
		}
	}
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// statusRecorder captures the response code for the audit hook.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
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

func (s *Server) probePeer(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
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
	if !s.allowMutation(CallerFrom(r.Context()).ID()) {
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
	release, ok := s.reserveSSE()
	if !ok {
		http.Error(w, "too many event streams", http.StatusServiceUnavailable)
		return
	}
	defer release()
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

// callerLimiter is one caller's mutation bucket.
type callerLimiter struct {
	last  time.Time
	burst int
	idle  time.Time
}

// allowMutation applies a per-caller one-per-second token bucket with a
// small burst, so a single dashboard action (name + peer) is not rejected
// while still bounding abuse. Callers are kernel identities (uid or SID)
// or "bearer" for token clients. An unknown or empty caller is denied
// outright (strictest bucket): wrapRole injects the effective caller, so
// an unknown caller reaching this point is unauthenticated, and the old
// shared "unknown" bucket that let any unauthenticated client share a
// rate limit is gone.
func (s *Server) allowMutation(callers ...string) bool {
	if len(callers) == 0 || callers[0] == "" || callers[0] == "unknown" {
		return false
	}
	caller := callers[0]
	s.callerMu.Lock()
	defer s.callerMu.Unlock()
	now := time.Now()
	if len(s.callers) > 256 {
		for k, st := range s.callers {
			if now.Sub(st.idle) > 10*time.Minute {
				delete(s.callers, k)
			}
		}
	}
	st, ok := s.callers[caller]
	if !ok {
		st = &callerLimiter{idle: now}
		s.callers[caller] = st
	}
	if st.last.IsZero() || now.Sub(st.last) >= time.Second {
		st.last = now
		st.burst = 0
		st.idle = now
		return true
	}
	st.burst++
	st.idle = now
	return st.burst <= 4
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
