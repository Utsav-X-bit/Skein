//go:build android

// Package android hosts the Skein daemon inside the Android app
// process: the Wails Android GUI and the daemon are one Go program
// (libwails.so), so the daemon runs in-process instead of as a
// privileged system service. The Java SkeinVpnService drives this
// package through the JNI bridge in gui/jni_android.go:
//
//	SetFilesDir(dir) -> Start() -> GetVpnConfig() -> establish TUN
//	-> SetTunFd(fd) -> Connect() ... Disconnect() -> Stop()
//
// The daemon starts paused (see daemon.New) and Connect resumes it
// once the TUN FD is in hand. Configuration and identity live in the
// app-private files dir; onboarding (creating them) is the GUI's
// Setup flow (Phase 3) — Start fails loudly when they are absent
// rather than inventing a network.
package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/inproc"
	"github.com/Utsav-X-bit/skein/internal/tun"
)

var (
	hostMu   sync.Mutex
	filesDir string
	d        *daemon.Daemon
	cancel   context.CancelFunc
	runDone  chan error
)

// SetFilesDir points the daemon and the app paths at the app's
// private files dir (Context.getFilesDir() from Java). It must be
// called before Start.
func SetFilesDir(dir string) error {
	if dir == "" {
		return errors.New("android: files dir must not be empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("android: files dir %q: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("android: files dir %q: %w", abs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("android: files dir %q is not a directory", abs)
	}
	hostMu.Lock()
	defer hostMu.Unlock()
	filesDir = abs
	app.SetFilesDir(abs)
	daemon.SetAndroidFilesDir(abs)
	return nil
}

// Started reports whether the daemon is running in this process.
func Started() bool {
	hostMu.Lock()
	defer hostMu.Unlock()
	return d != nil
}

// Start loads the Android config and identity and runs the daemon in
// the background. The daemon starts paused; Connect resumes it once
// the VpnService has established the TUN and SetTunFd has delivered
// the FD.
func Start() error {
	hostMu.Lock()
	defer hostMu.Unlock()
	if d != nil {
		return errors.New("android: daemon already running")
	}
	if filesDir == "" {
		return errors.New("android: files dir not set; call SetFilesDir first")
	}
	// A previous daemon in this process had its listener closed by
	// http.Server.Shutdown; serve a fresh one.
	inproc.Reset()

	configPath := filepath.Join(filesDir, "skein.toml")
	cfg, err := conf.Load(configPath)
	if err != nil {
		return fmt.Errorf("android: load config %s (onboard first): %w", configPath, err)
	}
	if err := conf.CheckPermissions(configPath); err != nil {
		return fmt.Errorf("android: config permissions: %w", err)
	}
	// The control API is in-process only on Android. Never also serve
	// a dev TCP listener, and never point at a socket path the file
	// may carry from a desktop install.
	cfg.API.Listen = ""
	cfg.API.Socket = ""
	cfg.API.Pipe = ""

	keyPath := filepath.Join(filesDir, "identity.key")
	id, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("android: load identity %s (onboard first): %w", keyPath, err)
	}
	nd, err := daemon.New(cfg, id)
	if err != nil {
		return fmt.Errorf("android: daemon: %w", err)
	}
	ctx, cancelFn := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- nd.Run(ctx) }()
	d, cancel, runDone = nd, cancelFn, done
	return nil
}

// Stop cancels the daemon's run loop, waits briefly for it to exit,
// then releases its resources (TUN, link, config lock). It is safe
// to call when the daemon is not running.
func Stop() error {
	hostMu.Lock()
	dd, cancelFn, done := d, cancel, runDone
	d, cancel, runDone = nil, nil, nil
	hostMu.Unlock()
	if dd == nil {
		return nil
	}
	cancelFn()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return dd.Close()
}

// Connect resumes the dataplane after the TUN FD is in hand. The
// caller supplies the context; daemon.Connect polls up to 30s for
// the connection to settle, so call this off the Android main
// thread.
func Connect(ctx context.Context) (api.Connection, error) {
	hostMu.Lock()
	dd := d
	hostMu.Unlock()
	if dd == nil {
		return api.Connection{}, errors.New("android: daemon not running")
	}
	return dd.Connect(ctx)
}

// Disconnect pauses the dataplane (engine stops, TUN/link close).
// The daemon keeps running and its control plane stays up.
func Disconnect(ctx context.Context) (api.Connection, error) {
	hostMu.Lock()
	dd := d
	hostMu.Unlock()
	if dd == nil {
		return api.Connection{}, errors.New("android: daemon not running")
	}
	return dd.Disconnect(ctx)
}

// VpnConfig is the overlay configuration SkeinVpnService needs to
// build the TUN. Network addresses are the masked prefixes
// (addRoute wants the network, not the host address).
type VpnConfig struct {
	Name              string `json:"name"`
	OverlayIPv4       string `json:"overlay_ipv4"`
	OverlayIPv4Prefix int    `json:"overlay_ipv4_prefix"`
	OverlayIPv4Net    string `json:"overlay_ipv4_network"`
	OverlayIPv6       string `json:"overlay_ipv6,omitempty"`
	OverlayIPv6Prefix int    `json:"overlay_ipv6_prefix,omitempty"`
	OverlayIPv6Net    string `json:"overlay_ipv6_network,omitempty"`
	MTU               int    `json:"mtu"`
	ListenPort        uint16 `json:"listen_port"`
}

// GetVpnConfig returns the running daemon's overlay configuration as
// JSON, for SkeinVpnService.Builder.
func GetVpnConfig() ([]byte, error) {
	hostMu.Lock()
	dd := d
	hostMu.Unlock()
	if dd == nil {
		return nil, errors.New("android: daemon not running")
	}
	view := dd.Config()
	v4, err := netip.ParsePrefix(view.OverlayIPv4Prefix)
	if err != nil {
		return nil, fmt.Errorf("android: overlay IPv4 prefix %q: %w", view.OverlayIPv4Prefix, err)
	}
	mtu := view.EffectiveMTU
	if mtu <= 0 {
		mtu = view.MTU
	}
	out := VpnConfig{
		Name:              view.Name,
		OverlayIPv4:       view.OverlayIPv4,
		OverlayIPv4Prefix: v4.Bits(),
		OverlayIPv4Net:    v4.Masked().Addr().String(),
		MTU:               mtu,
		ListenPort:        view.ListenPort,
	}
	if view.OverlayIPv6 != "" {
		v6, err := netip.ParsePrefix(view.OverlayIPv6Prefix)
		if err != nil {
			return nil, fmt.Errorf("android: overlay IPv6 prefix %q: %w", view.OverlayIPv6Prefix, err)
		}
		out.OverlayIPv6 = view.OverlayIPv6
		out.OverlayIPv6Prefix = v6.Bits()
		out.OverlayIPv6Net = v6.Masked().Addr().String()
	}
	return json.Marshal(out)
}

// SetTunFd hands the TUN file descriptor from
// VpnService.Builder.establish() to the daemon (see tun.SetAndroidFd).
func SetTunFd(fd int) error { return tun.SetAndroidFd(fd) }
