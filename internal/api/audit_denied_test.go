package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeniedAttemptAudit is the Step 5 (M5) test for the Step 1 open
// item: a denied authorisation attempt must produce an audit record
// (caller, method, path, required vs held role, 403) through the same
// audit hook as mutations — and the record must never carry a token,
// secret, or request body (AuditEvent has no such field, and this test
// proves the formatted record contains none of the planted values).
func TestDeniedAttemptAudit(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	grantTestUser(srv, 1000, RoleRead)
	var events []AuditEvent
	srv.Audit(func(ev AuditEvent) { events = append(events, ev) })
	h := srv.Handler()

	// Mutate endpoint as a read-only kernel caller: 403 + one denial.
	rec := doKernel(t, h, http.MethodPost, "/api/v1/peers", `{"invite":"skein:2:PLANTED-SECRET"}`, nil, 1000)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /peers as read = %d, want 403", rec.Code)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1 denial", len(events))
	}
	ev := events[0]
	if !ev.Denied || ev.Caller != "uid:1000" || ev.Method != http.MethodPost ||
		ev.Path != "/api/v1/peers" || ev.Status != http.StatusForbidden ||
		ev.Required != RoleMutate || ev.Held != RoleRead {
		t.Fatalf("denial event = %+v, want denied uid:1000 POST /api/v1/peers 403 required=mutate held=read", ev)
	}
	if got := fmt.Sprintf("%+v", ev); strings.Contains(got, "PLANTED-SECRET") || strings.Contains(got, "secret-token") || strings.Contains(got, "skein:2:") {
		t.Fatalf("denial record leaks secret material: %+v", ev)
	}
	if strings.Contains(rec.Body.String(), "skein:2:") {
		t.Fatalf("denied response leaks secret material: %q", rec.Body.String())
	}

	// Secret endpoint as the same read caller: 403 + denial flagged
	// secret, still with no secret material in the record.
	srvGUI, _, _ := newTestServerGUI(t)
	grantTestUser(srvGUI, 1000, RoleRead)
	events = nil
	srvGUI.Audit(func(ev AuditEvent) { events = append(events, ev) })
	rec = doKernel(t, srvGUI.Handler(), http.MethodPost, "/api/v1/invites/network", "", nil, 1000)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /invites/network as read = %d, want 403", rec.Code)
	}
	if len(events) != 1 || !events[0].Denied || !events[0].Secret ||
		events[0].Required != RoleSecret || events[0].Held != RoleRead {
		t.Fatalf("secret denial events = %+v, want one denied secret required=secret held=read", events)
	}
	if strings.Contains(rec.Body.String(), "skein:2:") {
		t.Fatalf("secret denied response leaks invite: %q", rec.Body.String())
	}
}

// TestUnauthenticatedNoAudit proves the documented Step 5 boundary:
// a 401 (no authenticated caller to attribute) produces no audit
// record, so an unauthenticated client cannot fill the audit log.
func TestUnauthenticatedNoAudit(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	var events []AuditEvent
	srv.Audit(func(ev AuditEvent) { events = append(events, ev) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/peers", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST = %d, want 401", rec.Code)
	}
	if len(events) != 0 {
		t.Fatalf("audit events = %+v, want none for a 401", events)
	}
}
