package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// bootstrapFixture is one unconfigured node directory with a minted
// token. Tests run as root, so fileperm.Root on written files works;
// SKEIN_NO_STUN keeps the result's candidate probe offline and fast.
type bootstrapFixture struct {
	dir     string
	cfgPath string
	keyPath string
	accPath string
	bs      *Bootstrap
	token   string // base64url plaintext token, for request bodies
	raw     []byte
}

func newBootstrapFixture(t *testing.T, verb string) *bootstrapFixture {
	t.Helper()
	t.Setenv("SKEIN_NO_STUN", "1")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	accPath := filepath.Join(dir, "access")
	statePath := filepath.Join(dir, "bootstrap.json")
	raw, err := bootstrap.Mint(statePath, "uid:1000", verb)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := NewBootstrap(BootstrapOptions{
		ConfigPath: cfgPath, IdentityPath: keyPath,
		StatePath: statePath, AccessPath: accPath,
		Socket: filepath.Join(dir, "control.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bs.Close() })
	return &bootstrapFixture{
		dir: dir, cfgPath: cfgPath, keyPath: keyPath, accPath: accPath,
		bs: bs, token: base64.RawURLEncoding.EncodeToString(raw), raw: raw,
	}
}

func (f *bootstrapFixture) call(t *testing.T, method, path string, caller api.Caller, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(api.WithCaller(req.Context(), caller))
	rec := httptest.NewRecorder()
	f.bs.Handler().ServeHTTP(rec, req)
	return rec
}

func kernelCaller() api.Caller { return api.Caller{UID: 1000, Transport: api.TransportKernel} }

func TestBootstrapStatusAndRefusedEndpoints(t *testing.T) {
	f := newBootstrapFixture(t, bootstrap.VerbInit)
	if rec := f.call(t, "GET", "/api/v1/status", kernelCaller(), ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	} else if !strings.Contains(rec.Body.String(), `"state":"bootstrap"`) {
		t.Fatalf("status body = %s", rec.Body.String())
	}
	if rec := f.call(t, "GET", "/api/v1/peers", kernelCaller(), ""); rec.Code != http.StatusGone {
		t.Fatalf("peers in bootstrap mode = %d, want 410", rec.Code)
	}
	if rec := f.call(t, "POST", "/api/v1/peers", kernelCaller(), `{}`); rec.Code != http.StatusGone {
		t.Fatalf("peer mutation in bootstrap mode = %d, want 410", rec.Code)
	}
}

func TestBootstrapInitHappyPath(t *testing.T) {
	f := newBootstrapFixture(t, bootstrap.VerbInit)
	rec := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(),
		`{"token":`+jsonStr(f.token)+`,"name":"bootnode","stun":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("init = %d: %s", rec.Code, rec.Body.String())
	}
	var res BootstrapResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Name != "bootnode" || !strings.HasPrefix(res.Join, "skein:2:") || !strings.HasPrefix(res.Invite, "skein:1:") {
		t.Fatalf("unexpected result: %+v", res)
	}
	cfg, err := conf.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.Name != "bootnode" || len(cfg.Node.NetworkSecret) != 32 {
		t.Fatalf("written config: name=%q secret len=%d", cfg.Node.Name, len(cfg.Node.NetworkSecret))
	}
	if cfg.API.Listen != "" || len(cfg.API.Token) != 0 {
		t.Fatal("bootstrap config must not enable the dev-TCP listener")
	}
	key, err := os.ReadFile(f.keyPath)
	if err != nil || len(key) != 32 {
		t.Fatalf("identity: %v len=%d", err, len(key))
	}
	if _, err := os.Stat(filepath.Join(f.dir, "bootstrap.json")); !os.IsNotExist(err) {
		t.Fatal("state file must be deleted after success")
	}
	acc, _ := os.ReadFile(f.accPath)
	if string(acc) != "1000:secret\n" {
		t.Fatalf("access file = %q, want bootstrap caller granted secret", acc)
	}
	// Second use after success: the token is dead.
	rec2 := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(),
		`{"token":`+jsonStr(f.token)+`}`)
	if rec2.Code != http.StatusGone {
		t.Fatalf("second use = %d, want 410", rec2.Code)
	}
	// The outcome is available for the in-process transition.
	out, ok := f.bs.Outcome()
	if !ok || out.CallerID != "uid:1000" || out.Config.Node.Name != "bootnode" {
		t.Fatalf("outcome ok=%v caller=%q", ok, out.CallerID)
	}
	clear(f.raw)
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestBootstrapRejectionMatrix(t *testing.T) {
	t.Run("wrong token", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		wrong := make([]byte, len(f.raw))
		copy(wrong, f.raw)
		wrong[0] ^= 0xff
		body := `{"token":` + jsonStr(base64.RawURLEncoding.EncodeToString(wrong)) + `}`
		if rec := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(), body); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong token = %d, want 403", rec.Code)
		}
		if _, err := os.Stat(filepath.Join(f.dir, "bootstrap.json")); err != nil {
			t.Fatal("state must survive a non-burning failure")
		}
	})
	t.Run("wrong uid", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		other := api.Caller{UID: 1001, Transport: api.TransportKernel}
		body := `{"token":` + jsonStr(f.token) + `}`
		if rec := f.call(t, "POST", "/api/v1/bootstrap/init", other, body); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong uid = %d, want 403", rec.Code)
		}
	})
	t.Run("bearer transport", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		bearer := api.Caller{UID: -1, Transport: api.TransportBearer}
		body := `{"token":` + jsonStr(f.token) + `}`
		if rec := f.call(t, "POST", "/api/v1/bootstrap/init", bearer, body); rec.Code != http.StatusForbidden {
			t.Fatalf("bearer = %d, want 403", rec.Code)
		}
	})
	t.Run("unknown caller", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		body := `{"token":` + jsonStr(f.token) + `}`
		if rec := f.call(t, "POST", "/api/v1/bootstrap/init", api.Caller{UID: -1}, body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("unknown = %d, want 401", rec.Code)
		}
	})
	t.Run("wrong verb", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		body := `{"token":` + jsonStr(f.token) + `,"invite":"skein:2:x"}`
		if rec := f.call(t, "POST", "/api/v1/bootstrap/join", kernelCaller(), body); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong verb = %d, want 403", rec.Code)
		}
	})
	t.Run("fifth failure burns", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		wrong := make([]byte, len(f.raw))
		copy(wrong, f.raw)
		wrong[0] ^= 0xff
		body := `{"token":` + jsonStr(base64.RawURLEncoding.EncodeToString(wrong)) + `}`
		for i := 0; i < bootstrap.MaxAttempts; i++ {
			rec := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(), body)
			if i < bootstrap.MaxAttempts-1 && rec.Code != http.StatusForbidden {
				t.Fatalf("attempt %d = %d, want 403", i+1, rec.Code)
			}
			if i == bootstrap.MaxAttempts-1 && rec.Code != http.StatusGone {
				t.Fatalf("5th attempt = %d, want 410 (burned)", rec.Code)
			}
		}
		// Even the correct token is dead now.
		rec := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(), `{"token":`+jsonStr(f.token)+`}`)
		if rec.Code != http.StatusGone {
			t.Fatalf("after burn = %d, want 410", rec.Code)
		}
	})
	t.Run("expired state", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "bootstrap.json")
		raw := make([]byte, bootstrap.TokenSize)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		st := bootstrap.State{
			TokenHash: hex.EncodeToString(sum[:]), Caller: "uid:1000",
			Verb: bootstrap.VerbInit, ExpiresAt: time.Now().Add(-time.Minute),
		}
		data, _ := json.Marshal(st)
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewBootstrap(BootstrapOptions{
			ConfigPath:   filepath.Join(dir, "skein.toml"),
			IdentityPath: filepath.Join(dir, "identity.key"),
			StatePath:    statePath,
		}); err == nil {
			t.Fatal("expired state must refuse bootstrap construction")
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatal("expired state file must be deleted at startup")
		}
	})
	t.Run("already configured", func(t *testing.T) {
		f := newBootstrapFixture(t, bootstrap.VerbInit)
		// Write a config behind bootstrap's back: construction of a
		// second Bootstrap for the same path must refuse.
		if err := os.WriteFile(f.cfgPath, []byte("# placeholder\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewBootstrap(BootstrapOptions{
			ConfigPath: f.cfgPath, IdentityPath: f.keyPath,
			StatePath: filepath.Join(f.dir, "bootstrap.json"),
		}); err == nil {
			t.Fatal("bootstrap with an existing config must refuse")
		}
	})
}

func TestBootstrapJoinHappyPath(t *testing.T) {
	inviter, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	invite, err := identity.EncodeNetworkInvite(identity.NetworkInvite{
		Secret: secret, ID: inviter.ID, PublicKey: inviter.PublicKey,
		PrefixV4: mustPrefix(t, "10.144.144.0/24"),
		PrefixV6: mustPrefix(t, "fd00:6d65:7368::/64"),
	})
	if err != nil {
		t.Fatal(err)
	}
	f := newBootstrapFixture(t, bootstrap.VerbJoin)
	body := `{"token":` + jsonStr(f.token) + `,"invite":` + jsonStr(invite) + `}`
	rec := f.call(t, "POST", "/api/v1/bootstrap/join", kernelCaller(), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body.String())
	}
	cfg, err := conf.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Peers) != 1 || cfg.Peers[0].ID != inviter.ID || !cfg.Peers[0].Dialer {
		t.Fatalf("joined config peers: %+v", cfg.Peers)
	}
	if !cfg.Node.OverlayIPv4Prefix.Contains(cfg.Node.OverlayIPv4) {
		t.Fatalf("overlay %s not in invite prefix", cfg.Node.OverlayIPv4)
	}
}

func TestBootstrapImportHappyPathAndSymlinkRefused(t *testing.T) {
	// Source: a valid user config + identity.key, both owner-only.
	srcDir := t.TempDir()
	srcID, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	srcCfg := conf.Default()
	srcCfg.Node.Name = "imported"
	srcCfg.Node.OverlayIPv4 = mustAddr(t, "10.144.144.9")
	// A user-mode source config must itself render: the dev-TCP
	// listen in Default() requires a token, so use a socket
	// transport for the source (bootstrap overwrites it anyway).
	srcCfg.API.Listen = ""
	srcCfg.API.Socket = "/run/skein/control.sock"
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	srcCfg.Node.NetworkSecret = secret
	text, err := conf.Render(srcCfg)
	if err != nil {
		t.Fatal(err)
	}
	// This shell's umask (007) makes Go test temp dirs group-writable
	// (0770), which the import parent check correctly refuses. Make
	// the source tree owner-only up to (not including) sticky /tmp.
	for dir := srcDir; dir != "/tmp" && dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	srcPath := filepath.Join(srcDir, "skein.toml")
	if err := os.WriteFile(srcPath, text, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "identity.key"), srcID.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}

	f := newBootstrapFixture(t, bootstrap.VerbImport)
	body := `{"token":` + jsonStr(f.token) + `,"source":` + jsonStr(srcPath) + `}`
	rec := f.call(t, "POST", "/api/v1/bootstrap/import", kernelCaller(), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d: %s", rec.Code, rec.Body.String())
	}
	cfg, err := conf.Load(f.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Node.Name != "imported" || cfg.Node.OverlayIPv4.String() != "10.144.144.9" {
		t.Fatalf("imported config: %+v", cfg.Node)
	}
	key, _ := os.ReadFile(f.keyPath)
	if !bytes.Equal(key, srcID.PrivateKey) {
		t.Fatal("imported identity does not match the source key")
	}

	// Symlink source must be refused (O_NOFOLLOW on Unix).
	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "skein.toml")
	if err := os.Symlink(srcPath, linkPath); err != nil {
		t.Fatal(err)
	}
	f2 := newBootstrapFixture(t, bootstrap.VerbImport)
	body2 := `{"token":` + jsonStr(f2.token) + `,"source":` + jsonStr(linkPath) + `}`
	rec2 := f2.call(t, "POST", "/api/v1/bootstrap/import", kernelCaller(), body2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("symlink import = %d, want 400", rec2.Code)
	}
	if _, err := os.Stat(f2.cfgPath); !os.IsNotExist(err) {
		t.Fatal("failed import must leave no config behind")
	}
}

func TestBootstrapValidationFailureDoesNotConsumeToken(t *testing.T) {
	f := newBootstrapFixture(t, bootstrap.VerbInit)
	// Bad port is a validation failure: token must survive for retry.
	bad := `{"token":` + jsonStr(f.token) + `,"mtu":1}`
	// MTU 1 is below MinMTU, so conf.Validate must reject it.
	rec := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(), bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid init = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(f.dir, "bootstrap.json")); err != nil {
		t.Fatal("state must survive a validation failure")
	}
	good := `{"token":` + jsonStr(f.token) + `,"stun":false}`
	rec2 := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(), good)
	if rec2.Code != http.StatusOK {
		t.Fatalf("retry after validation failure = %d: %s", rec2.Code, rec2.Body.String())
	}
}

// TestBootstrapServeOverUnixSocket exercises the real bootstrap
// listener: a client dialing the socket is identified by SO_PEERCRED,
// so the token must be minted for the test process's own uid. On
// success Serve shuts down gracefully and returns the outcome for
// the in-process transition.
func TestBootstrapServeOverUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test")
	}
	t.Setenv("SKEIN_NO_STUN", "1")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skein.toml")
	keyPath := filepath.Join(dir, "identity.key")
	accPath := filepath.Join(dir, "access")
	statePath := filepath.Join(dir, "bootstrap.json")
	socketPath := filepath.Join(dir, "control.sock")
	caller := fmt.Sprintf("uid:%d", os.Getuid())
	raw, err := bootstrap.Mint(statePath, caller, bootstrap.VerbInit)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := NewBootstrap(BootstrapOptions{
		ConfigPath: cfgPath, IdentityPath: keyPath,
		StatePath: statePath, AccessPath: accPath, Socket: socketPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serveErr := make(chan error, 1)
	var out BootstrapOutcome
	go func() {
		var err error
		out, err = bs.Serve(ctx)
		serveErr <- err
	}()
	// Wait for the socket to appear, then bootstrap over it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bootstrap socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}}
	body := `{"token":` + jsonStr(base64.RawURLEncoding.EncodeToString(raw)) + `,"name":"served","stun":false}`
	resp, err := client.Post("http://bootstrap/api/v1/bootstrap/init", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serve init = %d", resp.StatusCode)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after a successful bootstrap")
	}
	if out.Config.Node.Name != "served" || out.CallerID != caller {
		t.Fatalf("outcome: %+v", out)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatal("config must exist after Serve bootstrap")
	}
	clear(raw)
}

func TestBootstrapNoMeshActivityInHandler(t *testing.T) {
	// Bootstrap mode must not create mesh artefacts: after a
	// successful bootstrap via the handler alone (no Serve, no
	// connLoop), the only files created are config, identity,
	// access, and the configstore lock file. There is no TUN, UDP,
	// mDNS, or firewall side effect reachable from the handler.
	f := newBootstrapFixture(t, bootstrap.VerbInit)
	rec := f.call(t, "POST", "/api/v1/bootstrap/init", kernelCaller(),
		`{"token":`+jsonStr(f.token)+`,"stun":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("init = %d", rec.Code)
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, want := range []string{"skein.toml", "identity.key", "access", "skein.toml.lock"} {
		if !names[want] {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	if len(names) != 4 {
		t.Fatalf("unexpected files in bootstrap dir: %v", names)
	}
	// No lock file is left held: a second store must be able to lock.
	// (The fixture's Bootstrap still holds its lifetime lock, so this
	// checks the file set only, not lock release — lock release is
	// covered by Close() in app.RunBootstrap's transition.)
	_ = context.Background
}
