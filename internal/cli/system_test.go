package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/ctlerr"
)

// fakeSystemDaemon serves the Step 7 system + Step 4 bootstrap endpoints
// over plain TCP (the CLI test transport; kernel-transport verification
// is covered by the api package's own tests). It records bootstrap
// tokens and helper verbs so tests can prove the flow, not just the
// exit code.
type fakeSystemDaemon struct {
	t            *testing.T
	tokens       []string
	verbs        []string
	status       int
	rotateJoin   string
	resetBackup  string
	leavePropped bool
}

func (f *fakeSystemDaemon) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := io.ReadAll(r.Body)
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/v1/bootstrap/"):
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if tok, _ := req["token"].(string); tok != "" {
			f.tokens = append(f.tokens, tok)
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			fmt.Fprintf(w, `{"code":"permission_denied","message":"forbidden: secret role required","remediation":"ask an administrator to grant it"}`)
			return
		}
		verb := strings.TrimPrefix(r.URL.Path, "/api/v1/bootstrap/")
		_ = json.NewEncoder(w).Encode(app.BootstrapResult{
			NodeID: "0123456789abcdef", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			Name: "testnode", Overlay: "10.144.144.1", Invite: "skein:1:test",
			Join: "skein:2:test", ConfigPath: "/etc/skein/skein.toml", Service: "skein",
		})
		_ = verb
	case r.URL.Path == "/api/v1/system/reset":
		_ = json.NewEncoder(w).Encode(map[string]any{"backup": f.resetBackup, "message": "reset done", "service_action_required": "stop"})
	case r.URL.Path == "/api/v1/system/leave":
		_ = json.NewEncoder(w).Encode(map[string]any{"departed": true, "propagated": f.leavePropped, "peers_left": 2, "routes_withdrawn": 3})
	case r.URL.Path == "/api/v1/system/rotate-secret":
		_ = json.NewEncoder(w).Encode(map[string]any{"join": f.rotateJoin, "backup": "/etc/skein/backups/r1", "restart_required": true})
	case r.URL.Path == "/api/v1/status":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "0123456789abcdef", "overlay_ipv4": "10.144.144.1", "peers": 0, "uptime_seconds": 1, "version": "test", "listen_port": 11010})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func stubSystem(t *testing.T, f *fakeSystemDaemon) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, u.Host, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	cfg, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	oldClient := newSystemClient
	newSystemClient = func() *client { return clientFor(cfg, 10*time.Second) }
	t.Cleanup(func() { newSystemClient = oldClient })
	oldMint := mintSystemToken
	mintSystemToken = func(verb string) (string, error) { return "test-bootstrap-token", nil }
	t.Cleanup(func() { mintSystemToken = oldMint })
	oldHelper := runSystemHelper
	runSystemHelper = func(verb string, args ...string) ([]byte, error) {
		f.verbs = append(f.verbs, verb)
		return []byte(`{}`), nil
	}
	t.Cleanup(func() { runSystemHelper = oldHelper })
}

func TestSystemBootstrapInitJoinImport(t *testing.T) {
	for _, verb := range []string{"init", "join", "import"} {
		f := &fakeSystemDaemon{t: t}
		stubSystem(t, f)
		var out bytes.Buffer
		var err error
		switch verb {
		case "init":
			err = cmdSystemInit(context.Background(), []string{"--name", "node1"}, &out)
		case "join":
			err = cmdSystemJoin(context.Background(), "skein:2:invite", &out)
		case "import":
			err = cmdSystemImport(context.Background(), "/tmp/user.toml", &out)
		}
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if len(f.tokens) != 1 || f.tokens[0] != "test-bootstrap-token" {
			t.Fatalf("%s: tokens = %v, want exactly the minted token in the body", verb, f.tokens)
		}
		if !strings.Contains(out.String(), "0123456789abcdef") || !strings.Contains(out.String(), "skein:2:test") {
			t.Fatalf("%s output = %q", verb, out.String())
		}
		if !strings.Contains(out.String(), "contains the network secret") {
			t.Fatalf("%s output lacks the secret warning: %q", verb, out.String())
		}
	}
}

func TestSystemResetStopsServiceAndLeaveWarnsNotPropagated(t *testing.T) {
	f := &fakeSystemDaemon{t: t, resetBackup: "/etc/skein/backups/reset1"}
	stubSystem(t, f)
	var out bytes.Buffer
	if err := cmdSystemReset(context.Background(), false, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/etc/skein/backups/reset1") || !strings.Contains(out.String(), "service stopped") {
		t.Fatalf("reset output = %q", out.String())
	}
	if len(f.verbs) != 1 || f.verbs[0] != "stop" {
		t.Fatalf("reset helper verbs = %v, want [stop]", f.verbs)
	}

	out.Reset()
	if err := cmdSystemLeave(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "peers left 2") || !strings.Contains(out.String(), "not yet been announced mesh-wide") {
		t.Fatalf("leave output = %q (propagated:false must be stated, not hidden)", out.String())
	}
}

func TestSystemRotatePrintsJoinBeforeRestart(t *testing.T) {
	f := &fakeSystemDaemon{t: t, rotateJoin: "skein:2:rotated"}
	stubSystem(t, f)
	var out bytes.Buffer
	if err := cmdSystemRotate(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "skein:2:rotated") || !strings.Contains(s, "every peer must rejoin") {
		t.Fatalf("rotate output = %q", s)
	}
	if strings.Index(s, "skein:2:rotated") > strings.Index(s, "service restarted") {
		t.Fatalf("join printed after restart notice: %q", s)
	}
	if len(f.verbs) != 1 || f.verbs[0] != "restart" {
		t.Fatalf("rotate helper verbs = %v, want [restart]", f.verbs)
	}
}

func TestSystemErrorsAreClassified(t *testing.T) {
	// 403 from the daemon must surface as Denied, not a generic failure.
	f := &fakeSystemDaemon{t: t, status: http.StatusForbidden}
	stubSystem(t, f)
	var out bytes.Buffer
	err := cmdSystemInit(context.Background(), nil, &out)
	if err == nil || ctlerr.KindOf(err) != ctlerr.Denied {
		t.Fatalf("403 init: err = %v, kind = %v, want Denied", err, ctlerr.KindOf(err))
	}
	// The daemon's own remediation wins over the generic Kind text.
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "grant it") {
		t.Fatalf("403 init error = %q", err.Error())
	}

	// A dead control plane must surface as DaemonDown.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	u, _ := url.Parse(srv.URL)
	srv.Close()
	path := writeConfig(t, u.Host, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	cfg, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	oldClient := newSystemClient
	newSystemClient = func() *client { return clientFor(cfg, 10*time.Second) }
	defer func() { newSystemClient = oldClient }()
	oldMint := mintSystemToken
	mintSystemToken = func(verb string) (string, error) { return "tok", nil }
	defer func() { mintSystemToken = oldMint }()
	err = cmdSystemInit(context.Background(), nil, &out)
	if err == nil || ctlerr.KindOf(err) != ctlerr.DaemonDown {
		t.Fatalf("down init: err = %v, kind = %v, want DaemonDown", err, ctlerr.KindOf(err))
	}
}

func TestDashboardUnreachablePrintsRemediationAndSpawnsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	u, _ := url.Parse(srv.URL)
	srv.Close()
	path := writeConfig(t, u.Host, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	code, _, stderr := runCLI(t, "-c", path, "dashboard")
	if code != 1 {
		t.Fatalf("dashboard code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "skein system start") {
		t.Fatalf("dashboard stderr = %q, want the service-start remediation", stderr)
	}
	// Step 8: no child daemon, hence no child log file beside the config.
	if _, err := os.Stat(path + ".log"); !os.IsNotExist(err) {
		t.Fatalf("dashboard created a child log file %s.log", path)
	}
}

func TestDevInitUsesKernelTransportNotLegacyToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dev transport assertion is socket-shaped on Unix; Windows pipe shape is covered by the cross-build")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	code, _, stderr := runCLI(t, "-c", path, "init")
	if code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	cfg, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Listen != "" || len(cfg.API.Token) != 0 {
		t.Fatalf("dev init still writes legacy listen/token: listen=%q token_len=%d", cfg.API.Listen, len(cfg.API.Token))
	}
	if cfg.API.Socket == "" {
		t.Fatalf("dev init has no control socket: %+v", cfg.API)
	}
}

func TestSystemPathCommandsRedirectToSystem(t *testing.T) {
	sys := app.SystemConfigPath()
	code, _, stderr := runCLI(t, "-c", sys, "reset")
	if code != 1 || !strings.Contains(stderr, "skein system reset") {
		t.Fatalf("system reset: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runCLI(t, "-c", sys, "setup")
	if code != 1 || !strings.Contains(stderr, "managed by the daemon") {
		t.Fatalf("system setup: code=%d stderr=%q", code, stderr)
	}
	// The dev-path reset tests still exercise cmdReset directly; this
	// only proves the system path no longer reaches it.
	var _ = errors.New
}
