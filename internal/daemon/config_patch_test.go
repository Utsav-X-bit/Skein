package daemon

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
)

// patchTestDaemon builds a real daemon over a temp config file: New holds
// the config lock, so PatchConfig exercises the production write path
// (configstore) without opening a TUN or a listener.
func patchTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := conf.Default()
	cfg.Path = filepath.Join(dir, "skein.toml")
	cfg.Node.Name = "patchtest"
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.1")
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.API.Listen = ""
	cfg.API.Socket = filepath.Join(dir, "control.sock")
	cfg.API.Token = make([]byte, 32)
	cfg.Discovery.MDNS = true
	d, err := New(cfg, id)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestPatchConfigAcceptsHostnameSTUNServer covers the discrepancy that broke
// the GUI: the default STUN server is a hostname, and conf.Validate accepts
// host:port, so the patch endpoint must not demand an IP literal.
func TestPatchConfigAcceptsHostnameSTUNServer(t *testing.T) {
	d := patchTestDaemon(t)
	ctx := context.Background()

	server := "stun.example.net:3478"
	res, err := d.PatchConfig(ctx, api.ConfigPatch{STUNServer: &server})
	if err != nil {
		t.Fatalf("hostname stun_server rejected: %v", err)
	}
	if !res.RestartRequired || len(res.Applied) != 1 || res.Applied[0] != "stun_server" {
		t.Fatalf("result = %+v, want stun_server applied and restart required", res)
	}
	if got := d.config().Discovery.STUNServer; got != server {
		t.Fatalf("live stun_server = %q, want %q", got, server)
	}
	loaded, err := conf.Load(d.config().Path)
	if err != nil {
		t.Fatalf("persisted config does not load: %v", err)
	}
	if loaded.Discovery.STUNServer != server {
		t.Fatalf("persisted stun_server = %q, want %q", loaded.Discovery.STUNServer, server)
	}

	// An empty value means "the default", exactly as an absent key does in
	// the file.
	empty := ""
	if _, err := d.PatchConfig(ctx, api.ConfigPatch{STUNServer: &empty}); err != nil {
		t.Fatalf("clearing stun_server failed: %v", err)
	}
	if got := d.config().Discovery.STUNServer; got != conf.DefaultSTUNServer {
		t.Fatalf("cleared stun_server = %q, want default %q", got, conf.DefaultSTUNServer)
	}

	// A custom server must survive a restart even while STUN is off, or the
	// patch would report success and silently lose the value.
	off := false
	custom := "stun.example.org:3478"
	if _, err := d.PatchConfig(ctx, api.ConfigPatch{STUN: &off, STUNServer: &custom}); err != nil {
		t.Fatalf("stun_server with STUN disabled: %v", err)
	}
	loaded, err = conf.Load(d.config().Path)
	if err != nil {
		t.Fatalf("persisted config does not load: %v", err)
	}
	if loaded.Discovery.STUN || loaded.Discovery.STUNServer != custom {
		t.Fatalf("reloaded discovery = %+v, want STUN off and stun_server %q", loaded.Discovery, custom)
	}
}

// TestPatchConfigClearsPublicAddr proves the optional public address can be
// removed; an empty value used to fail the IP:port parse and leave the old
// address in place.
func TestPatchConfigClearsPublicAddr(t *testing.T) {
	d := patchTestDaemon(t)
	ctx := context.Background()

	set := "203.0.113.7:11010"
	if _, err := d.PatchConfig(ctx, api.ConfigPatch{PublicAddr: &set}); err != nil {
		t.Fatalf("setting public_addr: %v", err)
	}
	if got := d.config().Node.PublicAddr; got != netip.MustParseAddrPort(set) {
		t.Fatalf("public_addr = %s, want %s", got, set)
	}

	empty := ""
	res, err := d.PatchConfig(ctx, api.ConfigPatch{PublicAddr: &empty})
	if err != nil {
		t.Fatalf("clearing public_addr: %v", err)
	}
	if !res.RestartRequired || len(res.Applied) != 1 || res.Applied[0] != "public_addr" {
		t.Fatalf("result = %+v, want public_addr applied and restart required", res)
	}
	if got := d.config().Node.PublicAddr; got.IsValid() {
		t.Fatalf("live public_addr = %s, want cleared", got)
	}
	loaded, err := conf.Load(d.config().Path)
	if err != nil {
		t.Fatalf("persisted config does not load: %v", err)
	}
	if loaded.Node.PublicAddr.IsValid() {
		t.Fatalf("persisted public_addr = %s, want cleared", loaded.Node.PublicAddr)
	}
}

// TestPatchConfigRejectsInvalidValues keeps the trust boundary: unsupported
// values are refused with a field-scoped validation error, and neither the
// live config nor the file moves.
func TestPatchConfigRejectsInvalidValues(t *testing.T) {
	d := patchTestDaemon(t)
	ctx := context.Background()

	// A successful write first, so "the file did not change" is meaningful.
	mtu := conf.MaxMTU
	if _, err := d.PatchConfig(ctx, api.ConfigPatch{MTU: &mtu}); err != nil {
		t.Fatalf("baseline patch: %v", err)
	}
	path := d.config().Path
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	beforeCfg := d.config()

	cases := []struct {
		name  string
		patch api.ConfigPatch
		field string
	}{
		{"stun without port", api.ConfigPatch{STUNServer: new("stun.l.google.com")}, "stun_server"},
		{"stun not host:port", api.ConfigPatch{STUNServer: new("stun.l.google.com:notaport")}, "stun_server"},
		{"public addr not an ip", api.ConfigPatch{PublicAddr: new("example.com:11010")}, "public_addr"},
		{"public addr unspecified", api.ConfigPatch{PublicAddr: new("0.0.0.0:11010")}, "public_addr"},
		{"public addr in overlay", api.ConfigPatch{PublicAddr: new("10.144.144.9:11010")}, "public_addr"},
	}
	for _, tc := range cases {
		_, err := d.PatchConfig(ctx, tc.patch)
		if err == nil {
			t.Fatalf("%s: PatchConfig = nil error, want rejection", tc.name)
		}
		var se *api.StatusError
		if !errors.As(err, &se) || se.Status != 400 || se.Details["field"] != tc.field {
			t.Fatalf("%s: error = %v, want 400 validation error on %s", tc.name, err, tc.field)
		}
	}

	after := d.config()
	if after.Discovery.STUNServer != beforeCfg.Discovery.STUNServer ||
		after.Node.PublicAddr != beforeCfg.Node.PublicAddr {
		t.Fatalf("rejected patches changed live config: %+v -> %+v", beforeCfg, after)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after rejections: %v", err)
	}
	if string(onDisk) != string(before) {
		t.Fatalf("rejected patches changed the config file:\n%s", onDisk)
	}
}

// TestPatchConfigNoOpNeedsNoRestart proves that re-submitting the current
// values is a successful no-op: nothing reported, no restart, no rewrite.
func TestPatchConfigNoOpNeedsNoRestart(t *testing.T) {
	d := patchTestDaemon(t)
	ctx := context.Background()

	mtu := conf.MaxMTU
	if _, err := d.PatchConfig(ctx, api.ConfigPatch{MTU: &mtu}); err != nil {
		t.Fatalf("baseline patch: %v", err)
	}
	cur := d.config()
	path := cur.Path
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}

	name := cur.Node.Name
	port := cur.Node.ListenPort
	publicAddr := "" // already unset
	mdns := cur.Discovery.MDNS
	stun := cur.Discovery.STUN
	server := cur.Discovery.STUNServer
	iface := cur.Node.Interface
	res, err := d.PatchConfig(ctx, api.ConfigPatch{
		Name:       &name,
		ListenPort: &port,
		MTU:        &mtu,
		Interface:  &iface,
		PublicAddr: &publicAddr,
		MDNS:       &mdns,
		STUN:       &stun,
		STUNServer: &server,
	})
	if err != nil {
		t.Fatalf("no-op patch failed: %v", err)
	}
	if res.RestartRequired || len(res.Applied) != 0 {
		t.Fatalf("no-op result = %+v, want nothing applied and no restart", res)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after no-op: %v", err)
	}
	if string(onDisk) != string(before) {
		t.Fatalf("no-op patch rewrote the config:\n%s", onDisk)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config after no-op: %v", err)
	}
	if !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("no-op patch touched the config file")
	}
}

// TestPatchConfigEmptyNameUsesDefault keeps the live name and the persisted
// file in agreement: the settings form sends an empty name when the field is
// cleared, and the file's "no name" state means the host default at startup.
func TestPatchConfigEmptyNameUsesDefault(t *testing.T) {
	d := patchTestDaemon(t)
	ctx := context.Background()

	empty := ""
	if _, err := d.PatchConfig(ctx, api.ConfigPatch{Name: &empty}); err != nil {
		t.Fatalf("empty name rejected: %v", err)
	}
	want := conf.DefaultNodeName()
	if want == "" {
		t.Skip("host name unavailable; empty name has no default to resolve to")
	}
	if got := d.config().Node.Name; got != want {
		t.Fatalf("live name = %q, want default %q", got, want)
	}
	if got := d.ctl.LocalName(); got != want {
		t.Fatalf("advertised name = %q, want default %q", got, want)
	}
	loaded, err := conf.Load(d.config().Path)
	if err != nil {
		t.Fatalf("persisted config does not load: %v", err)
	}
	if loaded.Node.Name != want {
		t.Fatalf("persisted name = %q, want %q", loaded.Node.Name, want)
	}
}

// TestPatchConfigPersistFailureReportsError proves a failed save is not a
// success: the caller gets an error, and neither the live config nor the
// advertised name moves.
func TestPatchConfigPersistFailureReportsError(t *testing.T) {
	d := patchTestDaemon(t)
	ctx := context.Background()

	before := d.config()
	beforeName := d.ctl.LocalName()

	// Removing the config directory makes the atomic write fail after the
	// merge validated.
	if err := os.RemoveAll(filepath.Dir(before.Path)); err != nil {
		t.Fatal(err)
	}

	name := "renamed-after-failed-save"
	port := before.Node.ListenPort + 1
	res, err := d.PatchConfig(ctx, api.ConfigPatch{Name: &name, ListenPort: &port})
	if err == nil {
		t.Fatalf("PatchConfig = %+v, nil error; want a persist failure", res)
	}

	if got := d.config().Node.Name; got != before.Node.Name {
		t.Fatalf("live name = %q after failed save, want %q", got, before.Node.Name)
	}
	if got := d.config().Node.ListenPort; got != before.Node.ListenPort {
		t.Fatalf("live listen_port = %d after failed save, want %d", got, before.Node.ListenPort)
	}
	if got := d.ctl.LocalName(); got != beforeName {
		t.Fatalf("advertised name = %q after failed save, want %q", got, beforeName)
	}
}
