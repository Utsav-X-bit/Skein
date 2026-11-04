package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSystemState implements StateReader (via fakeState) and
// SystemOperator, so the Step 7 routes resolve in tests without
// a daemon. It records calls so confirm-gating tests can prove
// a refused request never reached the operator.
type fakeSystemState struct {
	*fakeState
	calls    []string
	reloaded bool
}

func (f *fakeSystemState) RotateSecret(ctx context.Context, confirm bool) (RotateResult, error) {
	f.calls = append(f.calls, "rotate")
	return RotateResult{Join: "skein:2:fake-join", RestartRequired: true}, nil
}

func (f *fakeSystemState) ResetSystem(ctx context.Context, opts ResetOptions) (ResetResult, error) {
	f.calls = append(f.calls, "reset")
	return ResetResult{Backup: "/tmp/bak", ServiceActionRequired: "stop"}, nil
}

func (f *fakeSystemState) LeaveSystem(ctx context.Context, confirm bool) (LeaveResult, error) {
	f.calls = append(f.calls, "leave")
	return LeaveResult{Departed: true, Propagated: false, PeersLeft: 1}, nil
}

func (f *fakeSystemState) SystemInit(ctx context.Context) error {
	f.calls = append(f.calls, "init")
	return &StatusError{Status: http.StatusConflict, Code: ErrCodeConflict, Message: "already configured"}
}

func (f *fakeSystemState) SystemJoin(ctx context.Context, invite string, confirm bool) (JoinResult, error) {
	f.calls = append(f.calls, "join")
	return JoinResult{PeerID: "0123456789abcdef"}, nil
}

func (f *fakeSystemState) SystemImport(ctx context.Context, source string, confirm bool) (ImportResult, error) {
	f.calls = append(f.calls, "import")
	return ImportResult{RestartRequired: true}, nil
}

func (f *fakeSystemState) UpdateCheck(ctx context.Context, manifestPath string) (UpdateCheckResult, error) {
	f.calls = append(f.calls, "check")
	return UpdateCheckResult{Version: "0.2.0", Checked: true, UpdateAvailable: true}, nil
}

func (f *fakeSystemState) UpdateApply(ctx context.Context, manifestPath, stagedBinaryPath string, confirm bool) (UpdateApplyResult, error) {
	f.calls = append(f.calls, "apply")
	return UpdateApplyResult{Version: "0.2.0", Checked: true, Detached: true}, nil
}

func (f *fakeSystemState) ReloadGrants(ctx context.Context) error {
	f.calls = append(f.calls, "reload")
	f.reloaded = true
	return nil
}

func newSystemTestServer(grants map[string]Role) (*Server, *fakeSystemState) {
	st := &fakeSystemState{fakeState: &fakeState{events: make(chan Event, 4)}}
	srv := NewServer([]byte("secret-token"), st, &fakeWriter{}, &fakeProber{})
	if grants != nil {
		srv.SetGrants(grants)
	}
	return srv, st
}

func doNoAuth(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestSystemEndpointRoleMatrix is the Step 7 extension of the
// Step 1 authorisation matrix: every new system endpoint is
// asserted for every caller class. In particular a read caller
// must never rotate/reset/leave-for-others/update, and a bearer
// caller (dev TCP) must never reach any secret endpoint —
// bearer is capped at read by roleFor, whatever token it holds.
func TestSystemEndpointRoleMatrix(t *testing.T) {
	type endpoint struct {
		method, path, body string
		role               Role
		okCode             int
	}
	endpoints := []endpoint{
		{http.MethodPost, "/api/v1/system/rotate-secret", `{"confirm":true}`, RoleSecret, http.StatusOK},
		{http.MethodPost, "/api/v1/system/reset", `{"confirm":true}`, RoleSecret, http.StatusOK},
		{http.MethodPost, "/api/v1/system/leave", `{"confirm":true}`, RoleMutate, http.StatusOK},
		{http.MethodPost, "/api/v1/system/init", `{}`, RoleSecret, http.StatusConflict},
		{http.MethodPost, "/api/v1/system/join", `{"invite":"skein:2:x"}`, RoleMutate, http.StatusOK},
		{http.MethodPost, "/api/v1/system/import", `{"source":"/tmp/x.toml","confirm":true}`, RoleSecret, http.StatusOK},
		{http.MethodGet, "/api/v1/system/update/check?manifest_path=/tmp/m.json", "", RoleSecret, http.StatusOK},
		{http.MethodPost, "/api/v1/system/update/apply", `{"manifest_path":"/tmp/m.json","confirm":true}`, RoleSecret, http.StatusOK},
		{http.MethodPost, "/api/v1/system/grants/reload", `{}`, RoleSecret, http.StatusOK},
	}
	type caller struct {
		name  string
		uid   int
		grant Role
		// bearer, when true, uses the dev-TCP token instead of a
		// kernel caller; unknown uses neither.
		bearer  bool
		unknown bool
	}
	callers := []caller{
		{"kernel-read", 1000, RoleRead, false, false},
		{"kernel-mutate", 1001, RoleMutate, false, false},
		{"kernel-secret", 1002, RoleSecret, false, false},
		{"kernel-root", 0, RoleNone, false, false},
		{"bearer", 0, RoleNone, true, false},
		{"unknown", 0, RoleNone, false, true},
	}
	for _, ep := range endpoints {
		for _, cs := range callers {
			t.Run(ep.method+" "+ep.path+" as "+cs.name, func(t *testing.T) {
				grants := map[string]Role{}
				if cs.uid != 0 && cs.grant != RoleNone {
					grants["uid:1000"] = RoleRead
					grants["uid:1001"] = RoleMutate
					grants["uid:1002"] = RoleSecret
				}
				srv, st := newSystemTestServer(grants)
				h := srv.Handler()
				var rec *httptest.ResponseRecorder
				switch {
				case cs.bearer:
					rec = do(t, h, ep.method, ep.path, ep.body, nil)
				case cs.unknown:
					rec = doNoAuth(t, h, ep.method, ep.path, ep.body)
				default:
					rec = doKernel(t, h, ep.method, ep.path, ep.body, nil, cs.uid)
				}
				held := cs.grant
				if cs.name == "kernel-root" {
					held = RoleSecret
				}
				if cs.bearer {
					held = RoleRead
				}
				want := ep.okCode
				if cs.unknown {
					want = http.StatusUnauthorized
				} else if held < ep.role {
					want = http.StatusForbidden
				}
				if rec.Code != want {
					t.Fatalf("code = %d, want %d (role %s, required %s)", rec.Code, want, held, ep.role)
				}
				if want == http.StatusForbidden && strings.Contains(rec.Body.String(), "skein:2:fake-join") {
					t.Fatal("denied response leaks the secret-bearing join string")
				}
				if want == ep.okCode && ep.path == "/api/v1/system/rotate-secret" && len(st.calls) != 1 {
					t.Fatalf("operator calls = %v, want exactly one rotate", st.calls)
				}
			})
		}
	}
}

// TestSystemConfirmRequired proves the confirm gate fires in
// the handler before the operator runs: a secret caller without
// confirm:true gets 400 and the operator is never called.
func TestSystemConfirmRequired(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/api/v1/system/rotate-secret", `{}`},
		{"/api/v1/system/reset", `{}`},
		{"/api/v1/system/leave", `{}`},
		{"/api/v1/system/import", `{"source":"/tmp/x.toml"}`},
		{"/api/v1/system/update/apply", `{"manifest_path":"/tmp/m.json"}`},
	}
	for _, c := range cases {
		srv, st := newSystemTestServer(map[string]Role{"uid:1002": RoleSecret})
		rec := doKernel(t, srv.Handler(), http.MethodPost, c.path, c.body, nil, 1002)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s without confirm: code = %d, want 400", c.path, rec.Code)
		}
		if len(st.calls) != 0 {
			t.Fatalf("%s without confirm reached the operator: %v", c.path, st.calls)
		}
	}
}
