package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestAuthorisationMatrix is the Step 1 (M5) gate: every API endpoint,
// classified read / mutate / secret, is asserted for every caller class.
// Roles: read < mutate < secret. Bootstrap is reserved for the future
// bootstrap endpoints (Step 4); no existing endpoint requires it.
// Transports: kernel = SO_PEERCRED uid / pipe SID (WithCaller), bearer =
// valid dev-TCP token, unknown = no caller and no token.
// Denial must never leak secret material: the network-invite body must not
// appear in a denied response.
func TestAuthorisationMatrix(t *testing.T) {
	// Helper to build a server with the given grants (nil = not configured).
	build := func(grants map[string]Role, gui bool) *Server {
		var srv *Server
		if gui {
			s, _, _ := newTestServerGUI(t)
			srv = s
		} else {
			s, _, _, _ := newTestServer(t)
			srv = s
		}
		if grants != nil {
			srv.SetGrants(grants)
		}
		return srv
	}

	type endpoint struct {
		method, path, body string
		role               Role
		gui                bool
		// okCode is the code a fully-authorised caller gets (200/204/400 for
		// a valid request; validation failures still prove the gate passed).
		okCode int
	}
	endpoints := []endpoint{
		{http.MethodGet, "/api/v1/status", "", RoleRead, false, http.StatusOK},
		{http.MethodGet, "/api/v1/peers", "", RoleRead, false, http.StatusOK},
		{http.MethodGet, "/api/v1/routes", "", RoleRead, false, http.StatusOK},
		{http.MethodGet, "/api/v1/activities", "", RoleRead, false, http.StatusOK},
		{http.MethodGet, "/api/v1/config", "", RoleRead, false, http.StatusOK},
		{http.MethodGet, "/api/v1/capabilities", "", RoleRead, true, http.StatusOK},
		{http.MethodGet, "/api/v1/identity", "", RoleRead, true, http.StatusOK},
		{http.MethodGet, "/api/v1/logs?since=0&limit=10", "", RoleRead, true, http.StatusOK},
		{http.MethodGet, "/api/v1/diagnostics", "", RoleRead, true, http.StatusOK},
		{http.MethodGet, "/api/v1/security", "", RoleRead, true, http.StatusOK},
		{http.MethodGet, "/api/v1/security/grants", "", RoleSecret, true, http.StatusOK},
		{http.MethodGet, "/api/v1/firewall", "", RoleRead, true, http.StatusOK},
		{http.MethodPost, "/api/v1/peers", `{"invite":"skein:1:abc"}`, RoleMutate, false, http.StatusNoContent},
		// Step 12: no DELETE peers row — the endpoint is deleted
		// (ADR-002). The matrix covers live routes only; the
		// deletion itself is asserted in TestPeerMutations.
		{http.MethodPost, "/api/v1/name", `{"name":"x"}`, RoleMutate, false, http.StatusNoContent},
		{http.MethodPatch, "/api/v1/config", `{"listen_port":1234}`, RoleMutate, true, http.StatusOK},
		{http.MethodPost, "/api/v1/connection", `{"action":"disconnect"}`, RoleMutate, true, http.StatusOK},
		{http.MethodPost, "/api/v1/peers/0123456789abcdef/probe", "", RoleMutate, true, http.StatusOK},
		{http.MethodPost, "/api/v1/invites/peer", "", RoleMutate, true, http.StatusOK},
		{http.MethodPost, "/api/v1/firewall/repair", "", RoleMutate, true, http.StatusOK},
		{http.MethodPost, "/api/v1/invites/network", "", RoleSecret, true, http.StatusOK},
	}

	callers := []struct {
		name   string
		caller *Caller
		bearer bool
		grants map[string]Role
	}{
		{"kernel-read", &Caller{UID: 1000}, false, map[string]Role{"uid:1000": RoleRead}},
		{"kernel-mutate", &Caller{UID: 1001}, false, map[string]Role{"uid:1001": RoleMutate}},
		{"kernel-secret", &Caller{UID: 1002}, false, map[string]Role{"uid:1002": RoleSecret}},
		{"kernel-root", &Caller{UID: 0}, false, map[string]Role{}},
		{"kernel-ungranted", &Caller{UID: 9999}, false, map[string]Role{"uid:1000": RoleRead}},
		{"kernel-no-grants-configured", &Caller{UID: 1000}, false, nil},
		{"pipe-secret", &Caller{SID: "S-1-5-21-1-2-3-1000"}, false, map[string]Role{"S-1-5-21-1-2-3-1000": RoleSecret}},
		{"bearer", nil, true, nil},
		{"unknown", nil, false, nil},
	}

	for _, ep := range endpoints {
		for _, cs := range callers {
			t.Run(ep.method+" "+ep.path+" as "+cs.name, func(t *testing.T) {
				srv := build(cs.grants, ep.gui)
				h := srv.Handler()
				req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
				if cs.caller != nil {
					req = req.WithContext(WithCaller(req.Context(), *cs.caller))
				}
				if cs.bearer {
					req.Header.Set("Authorization", Bearer([]byte("secret-token")))
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				// Expected outcome.
				var want int
				switch {
				case cs.name == "unknown":
					want = http.StatusUnauthorized
				case cs.caller != nil && cs.name == "kernel-ungranted":
					want = http.StatusForbidden
				case cs.bearer:
					// Bearer dev clients are read-only by design.
					if ep.role == RoleRead {
						want = ep.okCode
					} else {
						want = http.StatusForbidden
					}
				case cs.caller != nil && cs.name == "kernel-no-grants-configured":
					// No authorizer configured: kernel callers get read
					// only (least privilege that keeps status reads working).
					if ep.role == RoleRead {
						want = ep.okCode
					} else {
						want = http.StatusForbidden
					}
				case cs.caller != nil && cs.caller.UID == 0:
					// uid 0 already owns the system; it is implicitly secret.
					want = ep.okCode
				default:
					role := cs.grants[cs.caller.ID()]
					if role >= ep.role {
						want = ep.okCode
					} else {
						want = http.StatusForbidden
					}
				}
				if rec.Code != want {
					t.Fatalf("code = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
				}
				if want == http.StatusForbidden || want == http.StatusUnauthorized {
					if strings.Contains(rec.Body.String(), "skein:2:") {
						t.Fatalf("denied response leaked secret material: %s", rec.Body.String())
					}
				}
				if ep.path == "/api/v1/config" && rec.Code == http.StatusOK {
					// Config view must never carry a real secret, even for
					// authorised readers.
					if strings.Contains(rec.Body.String(), `"network_secret":""`) {
						t.Fatalf("config view leaked empty-secret marker unexpectedly: %s", rec.Body.String())
					}
				}
			})
		}
	}
}

// TestAccessGrantsParsing covers the access-file format: uid:role,
// SID:role, bare lines (legacy, least privilege = read), comments, blanks,
// and invalid roles.
func TestAccessGrantsParsing(t *testing.T) {
	grants, err := ParseAccessGrants(strings.NewReader(
		"# comment\n\n1000\n1001:mutate\n1002:secret\nS-1-5-21-1-2-3-1000:secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Role{
		"uid:1000":            RoleRead,
		"uid:1001":            RoleMutate,
		"uid:1002":            RoleSecret,
		"S-1-5-21-1-2-3-1000": RoleSecret,
	}
	if len(grants) != len(want) {
		t.Fatalf("grants = %v, want %v", grants, want)
	}
	for k, v := range want {
		if grants[k] != v {
			t.Fatalf("grants[%q] = %v, want %v", k, grants[k], v)
		}
	}
	if _, err := ParseAccessGrants(strings.NewReader("1000:boss\n")); err == nil {
		t.Fatal("invalid role accepted")
	}
	if _, err := ParseAccessGrants(strings.NewReader("1000:bootstrap\n")); err == nil {
		t.Fatal("bootstrap role must never come from the access file")
	}
	if _, err := ParseAccessGrants(strings.NewReader("notanid:read\n")); err == nil {
		t.Fatal("invalid identity accepted")
	}
}

// TestAuditHookRedactsSecretEndpoints: a secret mutation produces exactly
// one audit record, flagged secret, and the record carries no secret value.
func TestAuditHookRedactsSecretEndpoints(t *testing.T) {
	srv, _, _ := newTestServerGUI(t)
	srv.SetGrants(map[string]Role{"uid:1002": RoleSecret})
	events := make(chan AuditEvent, 4)
	srv.Audit(func(e AuditEvent) { events <- e })
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites/network", nil)
	req = req.WithContext(WithCaller(req.Context(), Caller{UID: 1002}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("network invite code = %d", rec.Code)
	}
	select {
	case ev := <-events:
		if !ev.Secret || ev.Method != http.MethodPost || ev.Path != "/api/v1/invites/network" {
			t.Fatalf("audit event = %+v", ev)
		}
	default:
		t.Fatal("no audit event for secret mutation")
	}
	if len(events) != 0 {
		t.Fatalf("extra audit events: %d", len(events))
	}
}

// TestAccessGrantsView covers the secret-only grants read: the whole
// identity→role table plus the caller's own security facts. A bearer token
// is valid but capped at Read, so it never reads the table; the response
// carries identities and roles only — no token, no key, no secret.
func TestAccessGrantsView(t *testing.T) {
	const path = "/api/v1/security/grants"
	srv, _, _ := newTestServerGUI(t)
	srv.SetGrants(map[string]Role{
		"uid:1002":            RoleSecret,
		"uid:1000":            RoleRead,
		"S-1-5-21-1-2-3-1000": RoleMutate,
	})
	h := srv.Handler()

	rec := doKernel(t, h, http.MethodGet, path, "", nil, 1002)
	if rec.Code != http.StatusOK {
		t.Fatalf("secret caller code = %d body=%s", rec.Code, rec.Body)
	}
	var view AccessGrantsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	wantCaller := CallerSecurity{ID: "uid:1002", Transport: "kernel", Role: "secret"}
	if view.Caller != wantCaller {
		t.Fatalf("caller = %+v, want %+v", view.Caller, wantCaller)
	}
	wantGrants := []GrantRecord{
		{Identity: "S-1-5-21-1-2-3-1000", Role: "mutate"},
		{Identity: "uid:1000", Role: "read"},
		{Identity: "uid:1002", Role: "secret"},
	}
	if !reflect.DeepEqual(view.Grants, wantGrants) {
		t.Fatalf("grants = %+v, want %+v", view.Grants, wantGrants)
	}

	// No secret material and no extra fields: the record shape is the
	// contract, so a future field cannot smuggle a key or a token out.
	var raw struct {
		Caller map[string]json.RawMessage   `json:"caller"`
		Grants []map[string]json.RawMessage `json:"grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Caller) != 4 || raw.Caller["is_root"] == nil {
		t.Fatalf("caller fields = %v", raw.Caller)
	}
	for _, g := range raw.Grants {
		if len(g) != 2 || g["identity"] == nil || g["role"] == nil {
			t.Fatalf("grant record carries unexpected fields: %v", g)
		}
	}
	body := rec.Body.String()
	for _, leak := range []string{"c2VjcmV0LXRva2Vu", "private", "PrivateKey", "network_secret"} {
		if strings.Contains(body, leak) {
			t.Fatalf("grants view leaked %q: %s", leak, body)
		}
	}

	// uid 0 owns the system and is implicitly secret; so are the Windows
	// SYSTEM / Administrators SIDs. Both must be flagged is_root.
	rec = doKernel(t, h, http.MethodGet, path, "", nil, 0)
	if rec.Code != http.StatusOK {
		t.Fatalf("root code = %d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Caller.IsRoot || view.Caller.Role != "secret" || view.Caller.Transport != "kernel" {
		t.Fatalf("root caller = %+v", view.Caller)
	}

	for _, sid := range []string{"S-1-5-18", "S-1-5-32-544"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(WithCaller(req.Context(), Caller{SID: sid}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d body=%s", sid, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if !view.Caller.IsRoot || view.Caller.ID != sid || view.Caller.Role != "secret" {
			t.Fatalf("%s caller = %+v", sid, view.Caller)
		}
	}

	// A valid bearer token is still read-only, and read/mutate kernel
	// callers hold too little role for the table.
	rec = do(t, h, http.MethodGet, path, "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bearer code = %d, want 403", rec.Code)
	}
	for _, uid := range []int{1000, 9999} {
		rec = doKernel(t, h, http.MethodGet, path, "", nil, uid)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("uid %d code = %d, want 403", uid, rec.Code)
		}
	}

	// An empty table is an empty list, never null: the screen renders it.
	srv.SetGrants(map[string]Role{})
	rec = doKernel(t, h, http.MethodGet, path, "", nil, 0)
	if !strings.Contains(rec.Body.String(), `"grants":[]`) {
		t.Fatalf("empty table body = %s", rec.Body)
	}
}
