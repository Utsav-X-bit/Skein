package services

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/ctlerr"
)

// Step 8 service-layer tests. The gui/services package needs the
// Wails/GTK toolchain for a plain Linux build; with -tags purego it
// builds and runs on the audit VM too (verified). Nothing here
// invents a JS harness: these are Go tests for the Go services layer.

func TestParseBootstrapToken(t *testing.T) {
	raw := []byte(`{"ok":true,"token":"abc123","verb":"init","expires_in_seconds":600}`)
	tok, err := parseBootstrapToken(raw, "init")
	if err != nil || tok != "abc123" {
		t.Fatalf("valid envelope: tok=%q err=%v", tok, err)
	}
	// A failed envelope must never leak the token into the error.
	raw = []byte(`{"ok":false,"error":"config exists","remediation":"use reset"}`)
	if _, err := parseBootstrapToken(raw, "init"); err == nil || strings.Contains(err.Error(), "abc123") {
		t.Fatalf("failed envelope err = %v", err)
	}
	raw = []byte(`{"ok":true,"token":"abc123","verb":"join"}`)
	if _, err := parseBootstrapToken(raw, "init"); err == nil {
		t.Fatal("join token accepted for init")
	}
}

func TestAPIErrorKindClassification(t *testing.T) {
	cases := map[int]ctlerr.Kind{
		401: ctlerr.Unauthenticated,
		403: ctlerr.Denied,
		423: ctlerr.Locked,
		400: ctlerr.Other,
		500: ctlerr.Other,
	}
	for code, want := range cases {
		e := &APIError{Status: code, Message: "x"}
		if got := e.Kind(); got != want {
			t.Errorf("APIError{Status:%d}.Kind() = %v, want %v", code, got, want)
		}
	}
	if ClassifyError(ErrDaemonNotConnected) != ctlerr.DaemonDown {
		t.Fatal("ErrDaemonNotConnected not classified DaemonDown")
	}
	if ClassifyError(errors.New("plain")) != ctlerr.Other {
		t.Fatal("plain error classified as non-Other")
	}
	// A daemon remediation wins over the generic Kind text.
	e := &APIError{Status: 403, Message: "forbidden", Remediation: "ask an administrator to grant it"}
	if got := ClassifiedRemediation(e); got != "ask an administrator to grant it" {
		t.Fatalf("ClassifiedRemediation = %q", got)
	}
}

// TestBootstrapCallPostsTokenInBody runs a fake bootstrap daemon over
// a Unix socket in this same process. Server-identity verification
// passes because the peer exe is this test binary and the test sets
// daemonImageOverride to it (the expected-image prefix rule;
// release builds ignore SKEIN_DAEMON_IMAGE, see env.go).
func TestBootstrapCallPostsTokenInBody(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/control.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var gotToken, gotPath string
	var gotBody map[string]any
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		gotToken, _ = gotBody["token"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"node_id": "0123456789abcdef", "public_key": "k", "name": "n",
			"overlay_ipv4": "10.144.144.1", "invite": "i", "join": "j",
			"config_path": "/etc/skein/skein.toml", "service": "skein",
		})
	})}
	go srv.Serve(ln)
	defer srv.Close()

	controlSocketOverride = sock
	t.Cleanup(func() { controlSocketOverride = "" })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	daemonImageOverride = exe
	t.Cleanup(func() { daemonImageOverride = "" })
	defaultClient = nil
	defer func() { defaultClient = nil }()

	res, err := bootstrapCall(context.Background(), "init", "test-token-123", map[string]any{"name": "node1"})
	if err != nil {
		t.Fatalf("bootstrapCall: %v", err)
	}
	if gotPath != "/api/v1/bootstrap/init" || gotToken != "test-token-123" {
		t.Fatalf("daemon saw path=%q token=%q", gotPath, gotToken)
	}
	if res.NodeID != "0123456789abcdef" || res.Join != "j" {
		t.Fatalf("result = %+v", res)
	}
}
