package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestListenerAuthAllowDeny covers listener-specific auth: a kernel-derived
// caller (UDS uid / pipe SID) bypasses the bearer token, an unknown caller
// (TCP) needs it.
func TestListenerAuthAllowDeny(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	h := srv.Handler()

	cases := []struct {
		name   string
		caller Caller
		header string
		want   int
	}{
		{"uds caller no token", Caller{UID: 1000}, "", http.StatusOK},
		{"uds caller wrong token", Caller{UID: 1000}, "Bearer wrong", http.StatusOK},
		{"pipe caller no token", Caller{SID: "S-1-5-21-1-2-3-1000"}, "", http.StatusOK},
		{"tcp no token", Caller{UID: -1}, "", http.StatusUnauthorized},
		{"tcp wrong token", Caller{UID: -1}, "Bearer wrong", http.StatusUnauthorized},
		{"tcp good token", Caller{UID: -1}, Bearer([]byte("secret-token")), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
			if tc.caller.Known() {
				req = req.WithContext(WithCaller(req.Context(), tc.caller))
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("code = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// TestAuditHookMutationOnly: mutations are audited with the caller identity
// and response code; reads are not.
func TestAuditHookMutationOnly(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	events := make(chan AuditEvent, 4)
	srv.Audit(func(e AuditEvent) { events <- e })
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req = req.WithContext(WithCaller(req.Context(), Caller{UID: 42}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read code = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/name", strings.NewReader(`{"name":"x"}`))
	req = req.WithContext(WithCaller(req.Context(), Caller{UID: 42}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mutation code = %d, want 204", rec.Code)
	}

	select {
	case ev := <-events:
		if ev.Method != http.MethodPost || ev.Path != "/api/v1/name" ||
			ev.Caller != "uid:42" || ev.Status != http.StatusNoContent {
			t.Fatalf("audit event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no audit event for mutation")
	}
	if len(events) != 0 {
		t.Fatalf("audit events = %d, want 0 after the mutation (reads not audited)", len(events))
	}
}

// TestPerCallerMutationLimit: one caller exhausting its bucket does not
// starve another caller.
func TestPerCallerMutationLimit(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	h := srv.Handler()
	post := func(caller Caller) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/name", strings.NewReader(`{"name":"x"}`))
		req = req.WithContext(WithCaller(req.Context(), caller))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	a, b := Caller{UID: 1}, Caller{UID: 2}
	for i := range 5 {
		if code := post(a); code != http.StatusNoContent {
			t.Fatalf("caller A burst %d: code = %d", i, code)
		}
	}
	// A used its 1 + burst 4; the next call is limited while B is fresh.
	if code := post(a); code != http.StatusTooManyRequests {
		t.Fatalf("caller A over burst: code = %d, want 429", code)
	}
	if code := post(b); code != http.StatusNoContent {
		t.Fatalf("caller B: code = %d, want 204", code)
	}
}

// capRecorder is a response recorder readable while the handler runs.
type capRecorder struct {
	mu   sync.Mutex
	code int
}

func newCapRecorder() *capRecorder { return &capRecorder{} }

func (r *capRecorder) Header() http.Header { return http.Header{} }
func (r *capRecorder) Write(p []byte) (int, error) {
	return len(p), nil
}
func (r *capRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = code
	}
}
func (r *capRecorder) Flush() {}
func (r *capRecorder) Code() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.code
}

func waitCode(t *testing.T, rec *capRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec.Code() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("code = %d, want %d", rec.Code(), want)
}

func startEventStream(t *testing.T, h http.Handler) *capRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req.Header.Set("Authorization", Bearer([]byte("secret-token")))
	rec := newCapRecorder()
	go h.ServeHTTP(rec, req)
	return rec
}

// TestSSECap: the event-stream cap rejects a second stream while ordinary
// requests still pass.
func TestSSECap(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	srv.Limits(16, 1)
	h := srv.Handler()

	one := startEventStream(t, h)
	waitCode(t, one, http.StatusOK)

	two := startEventStream(t, h)
	waitCode(t, two, http.StatusServiceUnavailable)

	rec := do(t, h, http.MethodGet, "/api/v1/status", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestConnectionCap: holding the only connection slot rejects the next
// request on any transport.
func TestConnectionCap(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	srv.Limits(1, 1)
	h := srv.Handler()

	one := startEventStream(t, h)
	waitCode(t, one, http.StatusOK)

	rec := do(t, h, http.MethodGet, "/api/v1/status", "", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("second connection = %d, want 503", rec.Code)
	}
}
