//go:build android

package android

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/inproc"
)

func TestHostRestartKeepsControlAndReleasesPendingTUN(t *testing.T) {
	dir := t.TempDir()
	if err := SetFilesDir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Stop() })
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cfg := conf.Default()
	cfg.Node.Name = "android-lifecycle-test"
	cfg.Node.OverlayIPv4 = netip.MustParseAddr("10.144.144.88")
	cfg.Node.NetworkSecret = make([]byte, 32)
	cfg.API.Token = make([]byte, 32)
	cfg.Discovery.MDNS, cfg.Discovery.STUN = false, false
	remote, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Peers = []conf.Peer{{
		ID: remote.ID, PublicKey: remote.PublicKey,
		Addrs: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:11010")}, Dialer: true,
	}}
	raw, err := conf.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skein.toml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.key"), id.PrivateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return inproc.Dial(ctx) },
	}}
	for generation := range 3 {
		if err := Start(); err != nil {
			t.Fatal(err)
		}
		if err := Start(); err != nil {
			t.Fatalf("repeated start must reuse the live daemon: %v", err)
		}
		resp, err := client.Get("http://skein/api/v1/status")
		if err != nil {
			t.Fatal(err)
		}
		var status api.Status
		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || status.Connection.State != "paused" {
			t.Fatalf("control plane after start: status=%d state=%+v error=%v", resp.StatusCode, status.Connection, err)
		}
		name := fmt.Sprintf("android-settings-%d", generation)
		payload, err := json.Marshal(api.ConfigPatch{
			Name: &name, ListenPort: new(uint16(12000 + generation)),
			MTU: new(1300), MDNS: new(false),
			STUNServer: new("stun.example.org:3478"), PublicAddr: new(""),
		})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPatch, "http://skein/api/v1/config", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Android settings save: status=%d body=%s error=%v", resp.StatusCode, body, err)
		}
		saved, err := conf.Load(filepath.Join(dir, "skein.toml"))
		if err != nil {
			t.Fatalf("settings no longer load after save: %v", err)
		}
		if saved.Node.Name != name || saved.Node.ListenPort != uint16(12000+generation) ||
			saved.Node.MTU != 1300 || saved.Discovery.MDNS || saved.Discovery.STUNServer != "stun.example.org:3478" {
			t.Fatalf("settings were not persisted: node=%+v discovery=%+v", saved.Node, saved.Discovery)
		}
		var fds [2]int
		if err := syscall.Pipe(fds[:]); err != nil {
			t.Fatal(err)
		}
		if err := SetTunFd(fds[0]); err != nil {
			t.Fatal(err)
		}
		if err := Stop(); err != nil {
			t.Fatal(err)
		}
		var st syscall.Stat_t
		if err := syscall.Fstat(fds[0], &st); err != syscall.EBADF {
			t.Fatalf("pending TUN still open after Stop: %v", err)
		}
		syscall.Close(fds[1])
		if Started() || Connection().State == "connected" {
			t.Fatal("stopped daemon still reported running/connected")
		}
	}
}

func TestBootstrapCancellationAllowsImmediateRetry(t *testing.T) {
	if err := SetFilesDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = StopBootstrap() })
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return inproc.Dial(ctx) },
	}}
	for _, verb := range []string{"join", "init", "join"} {
		if err := EnsureBootstrap(verb); err != nil {
			t.Fatalf("prepare %s after cancel: %v", verb, err)
		}
		if token, ok := TakeBootstrapToken(verb); !ok || token == "" {
			t.Fatal("bootstrap token missing")
		}
		resp, err := client.Get("http://skein/api/v1/status")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err := StopBootstrap(); err != nil {
			t.Fatal(err)
		}
	}
}
