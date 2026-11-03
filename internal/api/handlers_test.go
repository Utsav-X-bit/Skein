package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGUI implements GUI on top of fakeState.
type fakeGUI struct {
	*fakeState
	hub      *Hub
	patched  []ConfigPatch
	probedID uint64
}

func (f *fakeGUI) Capabilities() Capabilities {
	return Capabilities{AppVersion: "test", Protocol: 1, Platform: "linux", Features: map[string]bool{"connection_pause": true}}
}

func (f *fakeGUI) Identity() Identity {
	return Identity{ID: "0123456789abcdef", PublicKey: "pub-key"}
}

func (f *fakeGUI) Connection() Connection { return Connection{State: "connected", SinceMS: 1} }

func (f *fakeGUI) Connect(ctx context.Context) (Connection, error) {
	return Connection{State: "connecting"}, nil
}

func (f *fakeGUI) Disconnect(ctx context.Context) (Connection, error) {
	return Connection{State: "paused", PausedReason: "user"}, nil
}

func (f *fakeGUI) PatchConfig(ctx context.Context, p ConfigPatch) (ConfigPatchResult, error) {
	f.patched = append(f.patched, p)
	applied := []string{}
	restart := false
	if p.Name != nil {
		applied = append(applied, "name")
	}
	if p.ListenPort != nil {
		applied = append(applied, "listen_port")
		restart = true
	}
	return ConfigPatchResult{RestartRequired: restart, Applied: applied}, nil
}

func (f *fakeGUI) PeerInvite(ctx context.Context) (Invite, error) {
	return Invite{Kind: "peer", Value: "skein:1:peer"}, nil
}

func (f *fakeGUI) NetworkInvite(ctx context.Context) (Invite, error) {
	return Invite{Kind: "network", Value: "skein:2:net", Snapshot: true, Warning: "w"}, nil
}

func (f *fakeGUI) Logs(since uint64, limit int) ([]LogEntry, uint64, error) {
	return []LogEntry{{TimeMS: 1, Level: "info", Message: "hello"}}, 1, nil
}

func (f *fakeGUI) Diagnostics() DiagnosticBundle {
	return DiagnosticBundle{Logs: []LogEntry{}, Peers: []Peer{}, Routes: []Route{}, Redactions: []string{"join_strings"}}
}

func (f *fakeGUI) Security() SecurityStatus {
	return SecurityStatus{ConfigPath: "/etc/skein/skein.toml"}
}

func (f *fakeGUI) Firewall() FirewallStatus {
	return FirewallStatus{Manager: "ufw", State: "active", Rules: []FirewallRule{}}
}

func (f *fakeGUI) RepairFirewall(ctx context.Context) (FirewallStatus, error) {
	return FirewallStatus{Manager: "ufw", State: "active", Rules: []FirewallRule{}}, nil
}

func (f *fakeGUI) Probe(ctx context.Context, id uint64) (ProbeResult, error) {
	f.probedID = id
	return ProbeResult{PeerID: "0123456789abcdef", State: "pending"}, nil
}

func (f *fakeGUI) Hub() *Hub { return f.hub }

func newTestServerGUI(t *testing.T) (*Server, *fakeGUI, *Hub) {
	t.Helper()
	hub := NewHub()
	gui := &fakeGUI{fakeState: &fakeState{events: make(chan Event, 4)}, hub: hub}
	return NewServer([]byte("secret-token"), gui, &fakeWriter{}, &fakeProber{}), gui, hub
}

func TestCapabilitiesAndIdentity(t *testing.T) {
	srv, _, _ := newTestServerGUI(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/capabilities", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities code = %d", rec.Code)
	}
	var caps Capabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Protocol != 1 || caps.Features["connection_pause"] != true {
		t.Fatalf("caps = %+v", caps)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/identity", "", nil)
	var id Identity
	if err := json.Unmarshal(rec.Body.Bytes(), &id); err != nil {
		t.Fatal(err)
	}
	if id.ID != "0123456789abcdef" || id.PublicKey != "pub-key" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestConfigPatchAllowlist(t *testing.T) {
	// Unknown fields (api.*, network_secret, peers, ...) are rejected by the
	// strict decoder, so a patch can only touch the allowlist.
	srv, gui, _ := newTestServerGUI(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodPatch, "/api/v1/config", `{"name":"box","network_secret":"abc"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field code = %d body=%s", rec.Code, rec.Body)
	}
	var env APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Code != ErrCodeValidationFailed || env.RequestID == "" {
		t.Fatalf("envelope = %+v", env)
	}
	if len(gui.patched) != 0 {
		t.Fatalf("rejected patch was applied: %+v", gui.patched)
	}

	rec = do(t, h, http.MethodPatch, "/api/v1/config", `{"listen_port":1234}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch code = %d body=%s", rec.Code, rec.Body)
	}
	var result ConfigPatchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.RestartRequired || len(result.Applied) != 1 || result.Applied[0] != "listen_port" {
		t.Fatalf("result = %+v", result)
	}
}

func TestConnectionAndInvites(t *testing.T) {
	srv, _, _ := newTestServerGUI(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/connection", `{"action":"disconnect"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("connection code = %d", rec.Code)
	}
	var conn Connection
	if err := json.Unmarshal(rec.Body.Bytes(), &conn); err != nil {
		t.Fatal(err)
	}
	if conn.State != "paused" {
		t.Fatalf("connection = %+v", conn)
	}

	if rec := do(t, h, http.MethodPost, "/api/v1/connection", `{"action":"warp"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action code = %d", rec.Code)
	}

	for path, kind := range map[string]string{"/api/v1/invites/peer": "peer", "/api/v1/invites/network": "network"} {
		rec := do(t, h, http.MethodPost, path, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d", path, rec.Code)
		}
		var inv Invite
		if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
			t.Fatal(err)
		}
		if inv.Kind != kind || inv.Value == "" {
			t.Fatalf("%s invite = %+v", path, inv)
		}
	}
}

func TestLogsPage(t *testing.T) {
	srv, _, _ := newTestServerGUI(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodGet, "/api/v1/logs?since=0&limit=10", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs code = %d", rec.Code)
	}
	var page logPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Next != 1 {
		t.Fatalf("page = %+v", page)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/logs?limit=0", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit code = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/logs?since=x", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad since code = %d", rec.Code)
	}
}

func TestProbeReportsPending(t *testing.T) {
	srv, gui, _ := newTestServerGUI(t)
	h := srv.Handler()

	rec := do(t, h, http.MethodPost, "/api/v1/peers/0123456789abcdef/probe", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("probe code = %d body=%s", rec.Code, rec.Body)
	}
	var res ProbeResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.State != "pending" || gui.probedID != 0x0123456789abcdef {
		t.Fatalf("probe = %+v id=%d", res, gui.probedID)
	}
}

// TestSSETwoClientsAndReplay verifies the contract: two subscribers both
// receive every event, and a late subscriber with Last-Event-ID gets the
// replayed tail from the ring.
func TestSSETwoClientsAndReplay(t *testing.T) {
	srv, _, hub := newTestServerGUI(t)
	h := srv.Handler()
	for i := 1; i <= 3; i++ {
		hub.Publish("peers:changed", map[string]int{"n": i})
	}

	open := func(t *testing.T, lastID string) (*syncRecorder, context.CancelFunc) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
		req.Header.Set("Authorization", Bearer([]byte("secret-token")))
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		rec := newSyncRecorder()
		done := make(chan struct{})
		go func() { h.ServeHTTP(rec, req); close(done) }()
		return rec, cancel
	}
	waitFor := func(t *testing.T, rec *syncRecorder, want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !strings.Contains(rec.String(), want) {
			time.Sleep(5 * time.Millisecond)
		}
		if !strings.Contains(rec.String(), want) {
			t.Fatalf("stream missing %q: %q", want, rec.String())
		}
	}

	a, cancelA := open(t, "")
	defer cancelA()
	waitFor(t, a, `"n":3`)

	b, cancelB := open(t, "2")
	defer cancelB()
	// B subscribed after event 3 but asked to resume after 2: replay delivers 3.
	waitFor(t, b, `"n":3`)
	if strings.Contains(b.String(), `"n":1`) {
		t.Fatalf("replay leaked pre-cursor events: %q", b.String())
	}

	hub.Publish("peers:changed", map[string]int{"n": 4})
	waitFor(t, a, `"n":4`)
	waitFor(t, b, `"n":4`)
	if !strings.Contains(a.String(), "id: 4") {
		t.Fatalf("event ID missing: %q", a.String())
	}
}
