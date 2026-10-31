package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	code, _, stderr := runCLI(t, "frobnicate")
	if code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := runCLI(t); code != 2 {
		t.Fatalf("no-argument code = %d, want 2", code)
	}
	if code, _, _ := runCLI(t, "peers", "add"); code != 1 {
		t.Fatalf("missing-argument code = %d, want 1", code)
	}
}

func TestInitCreatesOwnerOnlyFilesAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	code, stdout, stderr := runCLI(t, "-c", path, "init")
	if code != 0 {
		t.Fatalf("init failed: %s", stderr)
	}
	if !strings.Contains(stdout, "invite:") || !strings.Contains(stdout, "node id:") {
		t.Fatalf("init output = %q", stdout)
	}
	for _, file := range []string{path, filepath.Join(dir, "identity.key")} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s mode = %o, want 600", file, perm)
		}
	}
	if code, _, _ := runCLI(t, "-c", path, "init"); code != 1 {
		t.Fatalf("second init code = %d, want 1", code)
	}
}

func TestGenkeyAndInviteWorkOffline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	if code, _, stderr := runCLI(t, "-c", path, "init"); code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	code, stdout, stderr := runCLI(t, "-c", path, "genkey")
	if code != 0 || !strings.Contains(stdout, "public key:") {
		t.Fatalf("genkey code=%d out=%q err=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "-c", path, "invite")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(stdout), "skein:1:") {
		t.Fatalf("invite code=%d out=%q err=%q", code, stdout, stderr)
	}
}

func writeConfig(t *testing.T, apiListen, token string) string {
	t.Helper()
	psk := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	text := `[node]
overlay_ipv4 = "10.144.144.1"
network_secret = "` + psk + `"

[api]
listen = "` + apiListen + `"
token = "` + token + `"

[discovery]
mdns = true
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOnlineCommandsUseTheAPI(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/v1/status":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"0123456789abcdef","overlay_ipv4":"10.144.144.1","peers":2,"uptime_seconds":5,"version":"test","listen_port":11010}`))
		case "/api/v1/peers":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":"0123456789abcdef","state":"up","path":"v4","rtt_ms":3.5,"direct":true}]`))
		case "/api/v1/routes":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"destination":"0123456789abcdef","next_hop":"0123456789abcdef","cost_ms":3.5,"hops":1}]`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, u.Host, token)

	code, stdout, stderr := runCLI(t, "-c", path, "status")
	if code != 0 {
		t.Fatalf("status: %s", stderr)
	}
	if !strings.Contains(stdout, "0123456789abcdef") || !strings.Contains(stdout, "peers:    2") {
		t.Fatalf("status output = %q", stdout)
	}
	if gotAuth != "Bearer "+token {
		t.Fatalf("auth header = %q", gotAuth)
	}

	code, stdout, _ = runCLI(t, "-c", path, "peers", "list")
	if code != 0 || !strings.Contains(stdout, "STATE") {
		t.Fatalf("peers output = %q", stdout)
	}
	code, stdout, _ = runCLI(t, "-c", path, "routes", "--json")
	if code != 0 || !strings.Contains(stdout, "destination") {
		t.Fatalf("routes output = %q", stdout)
	}
	if code, _, _ := runCLI(t, "-c", path, "probe", "0123456789abcdef"); code != 0 {
		t.Fatalf("probe code = %d", code)
	}
	if code, _, _ := runCLI(t, "-c", path, "probe", "not-an-id"); code != 1 {
		t.Fatalf("bad probe id code = %d, want 1", code)
	}
}

func TestAPIErrorsAreReported(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	path := writeConfig(t, u.Host, token)

	code, _, stderr := runCLI(t, "-c", path, "status")
	if code != 1 || !strings.Contains(stderr, "401") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
