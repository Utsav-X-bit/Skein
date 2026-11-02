package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/version"
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
	if !cfgA.Node.OverlayIPv4Prefix.Contains(cfgB.Node.OverlayIPv4) {
		t.Fatalf("joined overlay %s is outside the prefix", cfgB.Node.OverlayIPv4)
	}
	if cfgB.Node.OverlayIPv4 == netip.MustParseAddr("10.144.144.0") || cfgB.Node.OverlayIPv4 == netip.MustParseAddr("10.144.144.255") {
		t.Fatalf("joined overlay %s is not a host address", cfgB.Node.OverlayIPv4)
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
	if cfgB.Node.OverlayIPv4 == netip.MustParseAddr("10.144.144.1") {
		t.Fatalf("colliding overlay not moved: %s", cfgB.Node.OverlayIPv4)
	}
	if !cfgB.Node.OverlayIPv4Prefix.Contains(cfgB.Node.OverlayIPv4) {
		t.Fatalf("moved overlay %s is outside the prefix", cfgB.Node.OverlayIPv4)
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

func TestInviteNetworkIncludesPublicAddr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skein.toml")
	if code, _, stderr := runCLI(t, "-c", path, "init"); code != 0 {
		t.Fatalf("init: %s", stderr)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text = bytes.Replace(text, []byte("[node]\n"), []byte("[node]\npublic_addr = \"203.0.113.7:11010\"\n"), 1)
	if err := os.WriteFile(path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "-c", path, "invite", "--network")
	if code != 0 {
		t.Fatalf("invite --network: %s", stderr)
	}
	inv, err := identity.DecodeNetworkInvite(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatal(err)
	}
	want := netip.MustParseAddrPort("203.0.113.7:11010")
	for _, addr := range inv.Addrs {
		if addr == want {
			return
		}
	}
	t.Fatalf("public address missing from invite: %v", inv.Addrs)
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

// TestLoadConfigExplainsOwnership guards the hint shown when the root daemon
// or an elevated init left the config unreadable to the normal user.
func TestLoadConfigExplainsOwnership(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "skein.toml")
	if err := os.WriteFile(path, []byte("[node]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "fix ownership") {
		t.Fatalf("loadConfig = %v, want an ownership hint", err)
	}
}

func TestVersionFlagsWorkWithoutAConfig(t *testing.T) {
	for _, arg := range []string{"version", "-v", "--version"} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), []string{"-c", "/nonexistent/skein.toml", arg}, &out, &errOut)
		if code != 0 {
			t.Fatalf("%s exit = %d, stderr = %q", arg, code, errOut.String())
		}
		want := "skein " + version.Info()
		if !strings.Contains(out.String(), want) {
			t.Fatalf("%s output = %q, want %q", arg, out.String(), want)
		}
	}
}

type setupRunner struct{ calls []string }

func (f *setupRunner) run(name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if line == "ufw status" {
		return "Status: active", nil
	}
	return "", nil
}

func TestSetupConfiguresTheFirewall(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))
	path := writeConfig(t, "127.0.0.1:1", token)
	f := &setupRunner{}
	old := firewall.SetRunner(f.run)
	t.Cleanup(func() { firewall.SetRunner(old) })

	code, out, errOut := runCLI(t, "-c", path, "setup")
	if code != 0 {
		t.Fatalf("setup exit = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "allowed UDP 11010") || !strings.Contains(out, "skein0") || !strings.Contains(out, "ufw") {
		t.Fatalf("setup output = %q", out)
	}
	calls := strings.Join(f.calls, "\n")
	for _, want := range []string{"ufw allow 11010/udp", "ufw allow in on skein0", "ufw allow out on skein0"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("missing %q in:\n%s", want, calls)
		}
	}

	f.calls = nil
	code, out, errOut = runCLI(t, "-c", path, "setup", "--remove")
	if code != 0 {
		t.Fatalf("setup --remove exit = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "removed") {
		t.Fatalf("remove output = %q", out)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "ufw delete allow in on skein0") {
		t.Fatalf("remove calls = %v", f.calls)
	}
}

func TestSetupReportsAMissingInterfaceAsPartialSuccess(t *testing.T) {
	cfg := conf.Default()
	cfg.Node.Interface = "Skein"
	var out bytes.Buffer
	if err := reportSetupError(firewall.ErrInterfaceMissing, cfg, &out); err != nil {
		t.Fatalf("missing interface should be a partial success: %v", err)
	}
	if !strings.Contains(out.String(), "allowed UDP 11010") || !strings.Contains(out.String(), "not present/up yet") {
		t.Fatalf("output = %q", out.String())
	}
	out.Reset()
	if err := reportSetupError(errors.New("access denied"), cfg, &out); err == nil {
		t.Fatal("a real failure should be returned")
	}
}

func TestLoadConfigMissingExplainsBothPaths(t *testing.T) {
	_, err := loadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err == nil {
		t.Fatal("missing config was accepted")
	}
	for _, want := range []string{"skein init", "-c", "ExecStart"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}

func stubDaemon(t *testing.T, running bool) {
	t.Helper()
	old := daemonActive
	daemonActive = func() bool { return running }
	t.Cleanup(func() { daemonActive = old })
}

func resetFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "skein.toml")
	identity := filepath.Join(dir, "identity.key")
	if err := os.WriteFile(config, []byte("[node]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return config, identity
}

func TestResetBacksUpConfigAndIdentity(t *testing.T) {
	stubDaemon(t, false)
	config, identity := resetFixture(t)
	var out bytes.Buffer
	if err := cmdReset(config, false, false, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("config still exists")
	}
	if _, err := os.Stat(identity); !os.IsNotExist(err) {
		t.Fatal("identity still exists")
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(config), "reset-*", "skein.toml"))
	if len(backups) != 1 {
		t.Fatalf("config backup = %v", backups)
	}
	if !strings.Contains(out.String(), "skein init") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestResetPurgeRemovesEverything(t *testing.T) {
	stubDaemon(t, false)
	config, identity := resetFixture(t)
	var out bytes.Buffer
	if err := cmdReset(config, true, false, false, &out); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{config, identity} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", path)
		}
	}
	if backups, _ := filepath.Glob(filepath.Join(filepath.Dir(config), "reset-*")); len(backups) != 0 {
		t.Fatalf("purge left backups: %v", backups)
	}
}

func TestResetCanKeepIdentity(t *testing.T) {
	stubDaemon(t, false)
	config, identity := resetFixture(t)
	var out bytes.Buffer
	if err := cmdReset(config, true, true, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("config still exists")
	}
	if _, err := os.Stat(identity); err != nil {
		t.Fatalf("identity was removed: %v", err)
	}
}

func TestResetRefusesARunningDaemon(t *testing.T) {
	stubDaemon(t, true)
	config, _ := resetFixture(t)
	var out bytes.Buffer
	if err := cmdReset(config, true, false, false, &out); err == nil {
		t.Fatal("reset ran while the daemon was active")
	}
	if err := cmdReset(config, true, false, true, &out); err != nil {
		t.Fatalf("--force did not proceed: %v", err)
	}
}

func TestResetWithNothingToRemove(t *testing.T) {
	stubDaemon(t, false)
	var out bytes.Buffer
	if err := cmdReset(filepath.Join(t.TempDir(), "skein.toml"), false, false, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to remove") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDaemonContactErrorSuggestsStartingIt(t *testing.T) {
	err := daemonContactError(errors.New("dial tcp 127.0.0.1:11011: connect: connection refused"))
	for _, want := range []string{"not running", "systemctl", "dashboard"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	other := daemonContactError(errors.New("i/o timeout"))
	if strings.Contains(other.Error(), "not running") {
		t.Fatal("a timeout was reported as a stopped daemon")
	}
}
