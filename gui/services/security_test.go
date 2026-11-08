package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/app"
)

// stubControl points the shared client at a local fake daemon and restores
// the previous client. Tests using it must not run in parallel.
func stubControl(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := defaultClient
	defaultClient = &Client{hc: srv.Client(), base: srv.URL}
	t.Cleanup(func() { defaultClient = old })
}

func TestGrantsReadsTheSecretEndpoint(t *testing.T) {
	want := api.AccessGrantsView{
		Caller: api.CallerSecurity{ID: "uid:1002", Transport: "kernel", Role: "secret"},
		Grants: []api.GrantRecord{{Identity: "uid:1002", Role: "secret"}},
	}
	var gotPath, gotMethod string
	stubControl(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewEncoder(w).Encode(want)
	})
	got, err := (&SecurityService{}).Grants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/security/grants" || gotMethod != http.MethodGet {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grants = %+v, want %+v", got, want)
	}
}

// A bearer (dev TCP) client is refused by the daemon; the wrapper must
// surface the refusal instead of an empty table that looks like "no grants".
func TestGrantsSurfacesForbidden(t *testing.T) {
	stubControl(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","message":"forbidden: secret role required"}`))
	})
	_, err := (&SecurityService{}).Grants(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want a 403 APIError", err)
	}
}

// TestGrantUserRunsHelperAndReloads: the fixed verb is the only write path,
// and the daemon reload is issued afterwards because the helper does not
// reload on grant.
func TestGrantUserRunsHelperAndReloads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("grant-user is the Linux verb")
	}
	var calls [][]string
	oldHelper := runHelper
	runHelper = func(_ context.Context, verb string, args ...string) error {
		calls = append(calls, append([]string{verb}, args...))
		return nil
	}
	t.Cleanup(func() { runHelper = oldHelper })

	reloads := 0
	stubControl(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/system/grants/reload" {
			reloads++
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	if err := (&SecurityService{}).GrantUser(context.Background(), 1001, "mutate"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"grant-user", "1001", "--role", "mutate"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("helper calls = %v, want %v", calls, want)
	}
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
}

// A helper failure is the user's answer; it must reach them unchanged and
// must not be reported as a completed grant.
func TestGrantUserSurfacesHelperError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("grant-user is the Linux verb")
	}
	oldHelper := runHelper
	runHelper = func(context.Context, string, ...string) error {
		return errors.New("grant-user: uid must be positive, got -1")
	}
	t.Cleanup(func() { runHelper = oldHelper })

	err := (&SecurityService{}).GrantUser(context.Background(), -1, "read")
	if err == nil || !strings.Contains(err.Error(), "uid must be positive") {
		t.Fatalf("err = %v", err)
	}
}

// The grant file is already written when the reload fails, so the error must
// say so rather than implying nothing changed.
func TestGrantUserReportsReloadFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("grant-user is the Linux verb")
	}
	oldHelper := runHelper
	runHelper = func(context.Context, string, ...string) error { return nil }
	t.Cleanup(func() { runHelper = oldHelper })
	stubControl(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"internal","message":"reload failed"}`))
	})

	err := (&SecurityService{}).GrantUser(context.Background(), 1001, "mutate")
	if err == nil || !strings.Contains(err.Error(), "grant is written") {
		t.Fatalf("err = %v", err)
	}
}

// Both grant verbs are bound on every platform; the one that does not apply
// fails at runtime with the platform rule instead of a missing binding.
func TestPlatformGrantVerbs(t *testing.T) {
	svc := &SecurityService{}
	if runtime.GOOS == "windows" {
		err := svc.GrantUser(context.Background(), 1001, "mutate")
		if err == nil || !strings.Contains(err.Error(), "set-owner") {
			t.Fatalf("GrantUser on Windows err = %v", err)
		}
		return
	}
	err := svc.SetOwner(context.Background(), "alice")
	if err == nil || !strings.Contains(err.Error(), "Windows verb") {
		t.Fatalf("SetOwner on Linux err = %v", err)
	}
}

// decodeRevokeOutcome is the helper-JSON boundary: success carries the
// outcome (including the note the helper emits as "note"), and a failure
// carries the helper's own words, never a key or a secret.
func TestDecodeRevokeOutcome(t *testing.T) {
	out, err := decodeRevokeOutcome([]byte(`{"ok":true,"identity":"uid:1001","role":"secret","reloaded":false,"note":"daemon not running; applies at next start"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := app.RevokeOutcome{
		Identity:   "uid:1001",
		Role:       "secret",
		Reloaded:   false,
		ReloadNote: "daemon not running; applies at next start",
	}
	if out != want {
		t.Fatalf("outcome = %+v, want %+v", out, want)
	}

	out, err = decodeRevokeOutcome([]byte(`{"ok":true,"identity":"uid:1001","role":"secret","reloaded":true,"warning":"removed the last secret grant"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Warning == "" || !out.Reloaded {
		t.Fatalf("forced outcome = %+v", out)
	}

	_, err = decodeRevokeOutcome([]byte(`{"ok":false,"error":"revoke-user: uid 1001 is the last grant","remediation":"re-run with --force"}`), errors.New("exit status 1"))
	if err == nil || !strings.Contains(err.Error(), "revoke-user: uid 1001 is the last grant") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("failure err = %v", err)
	}
	if _, err = decodeRevokeOutcome(nil, errors.New("elevate helper: cancelled")); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("transport err = %v", err)
	}
}
