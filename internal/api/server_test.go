package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncRecorder is a ResponseRecorder that is safe to read while the handler runs.
type syncRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
	code   int
}

func newSyncRecorder() *syncRecorder { return &syncRecorder{header: make(http.Header)} }

func (r *syncRecorder) Header() http.Header { return r.header }
func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}
func (r *syncRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.code = code
}
func (r *syncRecorder) Flush() {}
func (r *syncRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

type fakeState struct {
	events chan Event
}

func (f *fakeState) Status() Status {
	return Status{ID: "0123456789abcdef", OverlayIPv4: "10.144.144.1", Version: "test", ListenPort: 11010, PeerCount: 1}
}
func (f *fakeState) Peers() []Peer {
	return []Peer{{ID: "0123456789abcdef", State: "up", Path: "v4", RTTMs: 12.4, Direct: true}}
}
func (f *fakeState) Routes() []Route {
	return []Route{{Destination: "0123456789abcdef", NextHop: "0123456789abcdef", CostMs: 12.4, Hops: 1}}
}
func (f *fakeState) Activities() []Activity { return nil }
func (f *fakeState) Config() ConfigView {
	return ConfigView{OverlayIPv4: "10.144.144.1", NetworkSecret: "redacted", APIToken: "redacted"}
}

func TestActivitiesReturnsAnArray(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodGet, "/api/v1/activities", "", nil)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("activities = %s", rec.Body.String())
	}
}
func (f *fakeState) Events() <-chan Event { return f.events }

type fakeWriter struct {
	added   []string
	removed []uint64
	names   []string
}

func (f *fakeWriter) SetName(name string) error {
	f.names = append(f.names, name)
	return nil
}

func (f *fakeWriter) AddPeer(invite string) error {
	f.added = append(f.added, invite)
	return nil
}
func (f *fakeWriter) RemovePeer(id uint64) error {
	f.removed = append(f.removed, id)
	return nil
}

type fakeProber struct{ probed []uint64 }

func (f *fakeProber) ProbePeer(ctx context.Context, id uint64) error {
	f.probed = append(f.probed, id)
	return nil
}

func newTestServer(t *testing.T) (*Server, *fakeState, *fakeWriter, *fakeProber) {
	t.Helper()
	state := &fakeState{events: make(chan Event, 4)}
	writer := &fakeWriter{}
	prober := &fakeProber{}
	return NewServer([]byte("secret-token"), state, writer, prober), state, writer, prober
}

func do(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", Bearer([]byte("secret-token")))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequiresBearerToken(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token code = %d, want 401", rec.Code)
	}
}

func TestRejectsBrowserOrigin(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodGet, "/api/v1/status", "", map[string]string{"Origin": "https://example.com"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header was sent")
	}
}

func TestReadEndpoints(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	h := srv.Handler()
	for _, path := range []string{"/api/v1/status", "/api/v1/peers", "/api/v1/routes", "/api/v1/activities", "/api/v1/config"} {
		rec := do(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("%s content type = %q", path, ct)
		}
	}
}

func TestConfigRedactsSecrets(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := do(t, srv.Handler(), http.MethodGet, "/api/v1/config", "", nil)
	var view ConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.NetworkSecret != "redacted" || view.APIToken != "redacted" {
		t.Fatalf("secrets leaked: %+v", view)
	}
}

func TestPeerMutations(t *testing.T) {
	srv, _, writer, prober := newTestServer(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/peers", `{"invite":"skein:1:abc"}`, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("post code = %d body=%s", rec.Code, rec.Body)
	}
	if len(writer.added) != 1 || writer.added[0] != "skein:1:abc" {
		t.Fatalf("added = %v", writer.added)
	}

	rec = do(t, h, http.MethodDelete, "/api/v1/peers/0123456789abcdef", "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete code = %d", rec.Code)
	}
	if len(writer.removed) != 1 || writer.removed[0] != 0x0123456789abcdef {
		t.Fatalf("removed = %v", writer.removed)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/peers/0123456789abcdef/probe", "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("probe code = %d", rec.Code)
	}
	if len(prober.probed) != 1 {
		t.Fatalf("probed = %v", prober.probed)
	}
}

func TestRejectsBadPeerRequests(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	h := srv.Handler()
	if rec := do(t, h, http.MethodPost, "/api/v1/peers", `{}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty invite code = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/peers", `not json`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json code = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/peers/zzzz", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id code = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/peers", strings.Repeat("x", 20<<10), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body code = %d", rec.Code)
	}
}

func TestSSEStreamsEvents(t *testing.T) {
	srv, state, _, _ := newTestServer(t)
	srv.lastCall = time.Time{}
	h := srv.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", Bearer([]byte("secret-token")))
	rec := newSyncRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()
	state.events <- Event{Type: "peer_up", PeerID: "0123456789abcdef"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(rec.String(), "peer_up") {
		time.Sleep(5 * time.Millisecond)
	}
	body := rec.String()
	if !strings.Contains(body, "event: peer_up") || !strings.Contains(body, "0123456789abcdef") {
		t.Fatalf("stream = %q", body)
	}
}

func TestSetNameEndpoint(t *testing.T) {
	srv, _, writer, _ := newTestServer(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/name", `{"name":"asus-utsav"}`, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body)
	}
	if len(writer.names) != 1 || writer.names[0] != "asus-utsav" {
		t.Fatalf("names = %v", writer.names)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/name", `{"name":""}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name code = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/name", `not json`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json code = %d", rec.Code)
	}
}
