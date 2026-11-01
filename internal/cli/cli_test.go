package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
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

func joinLine(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "join:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "join:"))
		}
	}
	t.Fatalf("no join line in output:\n%s", output)
	return ""
}

func nodeID(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "node id:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "node id:"))
		}
	}
	t.Fatalf("no node id in output:\n%s", output)
	return ""
}

func TestInitPrintsAJoinString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skein.toml")
	code, stdout, stderr := runCLI(t, "-c", path, "init")
	if code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	join := joinLine(t, stdout)
	if !strings.HasPrefix(join, "skein:2:") {
		t.Fatalf("join string = %q", join)
	}
	inv, err := identity.DecodeNetworkInvite(join)
	if err != nil {
		t.Fatalf("decode join: %v", err)
	}
	cfg, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inv.Secret, cfg.Node.NetworkSecret) {
		t.Fatal("join string does not carry the network secret")
	}
	if inv.ID == 0 {
		t.Fatal("join string has no identity")
	}
}

func TestJoinBootstrapsASecondNode(t *testing.T) {
	dirA := t.TempDir()
	pathA := filepath.Join(dirA, "skein.toml")
	code, outA, stderr := runCLI(t, "-c", pathA, "init")
	if code != 0 {
		t.Fatalf("init A: %s", stderr)
	}
	idA := nodeID(t, outA)
	join := joinLine(t, outA)

	dirB := t.TempDir()
	pathB := filepath.Join(dirB, "skein.toml")
	code, outB, stderr := runCLI(t, "-c", pathB, "join", join)
	if code != 0 {
		t.Fatalf("join B: %s", stderr)
	}
	if !strings.Contains(outB, "joined network") {
		t.Fatalf("join output = %q", outB)
	}

	cfgA, err := conf.Load(pathA)
	if err != nil {
		t.Fatal(err)
	}
	cfgB, err := conf.Load(pathB)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cfgA.Node.NetworkSecret, cfgB.Node.NetworkSecret) {
		t.Fatal("secrets differ after join")
	}
	if cfgA.Node.OverlayIPv4 == cfgB.Node.OverlayIPv4 {
		t.Fatalf("overlay address collision: %s", cfgB.Node.OverlayIPv4)
	}
	if cfgB.Node.OverlayIPv4.String() != "10.144.144.2" {
		t.Fatalf("joined overlay = %s, want 10.144.144.2", cfgB.Node.OverlayIPv4)
	}
	if _, err := os.Stat(filepath.Join(dirB, "identity.key")); err != nil {
		t.Fatalf("identity not created: %v", err)
	}
	found := false
	for _, p := range cfgB.Peers {
		if fmt.Sprintf("%016x", p.ID) == idA && p.Dialer {
			found = true
		}
	}
	if !found {
		t.Fatalf("inviter is not a dialer peer: %+v", cfgB.Peers)
	}

	// Joining again must keep the assigned overlay address.
	if code, _, stderr := runCLI(t, "-c", pathB, "join", join); code != 0 {
		t.Fatalf("second join: %s", stderr)
	}
	again, err := conf.Load(pathB)
	if err != nil {
		t.Fatal(err)
	}
	if again.Node.OverlayIPv4 != cfgB.Node.OverlayIPv4 {
		t.Fatalf("overlay changed on re-join: %s -> %s", cfgB.Node.OverlayIPv4, again.Node.OverlayIPv4)
	}
}

func TestJoinMovesACollidingOverlayAddress(t *testing.T) {
	dirA := t.TempDir()
	pathA := filepath.Join(dirA, "skein.toml")
	code, outA, stderr := runCLI(t, "-c", pathA, "init")
	if code != 0 {
		t.Fatalf("init A: %s", stderr)
	}
	join := joinLine(t, outA)

	// B was initialised separately and also defaults to 10.144.144.1.
	dirB := t.TempDir()
	pathB := filepath.Join(dirB, "skein.toml")
	if code, _, stderr := runCLI(t, "-c", pathB, "init"); code != 0 {
		t.Fatalf("init B: %s", stderr)
	}
	if code, _, stderr := runCLI(t, "-c", pathB, "join", join); code != 0 {
		t.Fatalf("join B: %s", stderr)
	}
	cfgB, err := conf.Load(pathB)
	if err != nil {
		t.Fatal(err)
	}
	if cfgB.Node.OverlayIPv4.String() != "10.144.144.2" {
		t.Fatalf("colliding overlay not moved: %s", cfgB.Node.OverlayIPv4)
	}
}

func TestNameCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skein.toml")
	if code, _, stderr := runCLI(t, "-c", path, "init"); code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	if code, _, stderr := runCLI(t, "-c", path, "name", "utsav-laptop"); code != 0 {
		t.Fatalf("name set: %s", stderr)
	}
	cfg, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.Name != "utsav-laptop" {
		t.Fatalf("name = %q", cfg.Node.Name)
	}
	code, stdout, _ := runCLI(t, "-c", path, "name")
	if code != 0 || !strings.Contains(stdout, "utsav-laptop") {
		t.Fatalf("name output = %q", stdout)
	}
	if code, _, _ := runCLI(t, "-c", path, "name", strings.Repeat("x", 40)); code != 1 {
		t.Fatalf("long name code = %d, want 1", code)
	}
}

func TestInviteNetworkCarriesTheSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skein.toml")
	if code, _, stderr := runCLI(t, "-c", path, "init"); code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	code, stdout, stderr := runCLI(t, "-c", path, "invite", "--network")
	if code != 0 {
		t.Fatalf("invite --network: %s", stderr)
	}
	inv, err := identity.DecodeNetworkInvite(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg, err := conf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inv.Secret, cfg.Node.NetworkSecret) {
		t.Fatal("network invite does not carry the secret")
	}
	if len(inv.UsedV4) == 0 || inv.UsedV4[0] != cfg.Node.OverlayIPv4 {
		t.Fatalf("used addresses = %v", inv.UsedV4)
	}
}
