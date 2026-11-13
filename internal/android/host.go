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
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/bootstrap"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/daemon"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/inproc"
	"github.com/Utsav-X-bit/skein/internal/tun"
)

var (
	// Serialise daemon generations through resource and config-lock release.
	lifecycleMu sync.Mutex
	hostMu      sync.Mutex
	filesDir    string
	d           *daemon.Daemon
	cancel      context.CancelFunc
	runDone     chan struct{}
	lastErr     error

	// Bootstrap-mode server state (onboarding only). The bootstrap
	// daemon holds the config-store lock from NewBootstrap until
	// Close, so it must be stopped — by success, by cancel, or by
	// Start — before the normal daemon can start.
	bsMu    sync.Mutex
	bs      *daemon.Bootstrap
	bsSrv   *http.Server
	bsToken string
	bsVerb  string
)

// FilesDir returns the app-private files dir SetFilesDir recorded,
// or "" before the Java side reports it (MainActivity.onCreate).
func FilesDir() string {
	hostMu.Lock()
	defer hostMu.Unlock()
	return filesDir
}

// ConfigExists reports whether a network config already exists in
// the app-private files dir.
func ConfigExists() bool {
	hostMu.Lock()
	dir := filesDir
	hostMu.Unlock()
	if dir == "" {
		dir = app.SystemConfigPath()
		// SystemConfigPath already joins the dir; use it directly.
		_, err := os.Stat(dir)
		return err == nil
	}
	_, err := os.Stat(filepath.Join(dir, "skein.toml"))
	return err == nil
}

// EnsureBootstrap mints a single-use bootstrap token for verb and
// serves the daemon's bootstrap-only API over the in-process
// transport, so the GUI's SetupService.Init/Join/Import can post
// exactly as on desktop — but with no helper and no system service:
// the token is minted in-process (same UID, no elevation) and the
// endpoint is served in-process. The token is stored here so the
// services layer returns this exact token instead of minting a
// second one: bootstrap.Mint refuses while a valid token exists
// ("already in progress"), and NewBootstrap requires a valid state.
//
// Callers must call StopBootstrap when onboarding is abandoned; a
// successful bootstrap stops itself via the outcome watcher below.
func EnsureBootstrap(verb string) error {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	hostMu.Lock()
	dir := filesDir
	hostMu.Unlock()
	if dir == "" {
		return errors.New("android: files dir not set; call SetFilesDir first")
	}
	bsMu.Lock()
	defer bsMu.Unlock()
	if bs != nil {
		if bsVerb == verb {
			return nil
		}
		// Switching verbs: the stored token is bound to the old
		// verb, so cancel it before minting for the new one.
		_ = stopBootstrapLocked()
	}
	if _, err := os.Stat(filepath.Join(dir, "skein.toml")); err == nil {
		return errors.New("android: already configured; bootstrap is closed")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("android: bootstrap config check: %w", err)
	}
	token, err := app.MintBootstrapToken(verb)
	if err != nil {
		return fmt.Errorf("android: mint bootstrap token: %w", err)
	}
	bsrv, err := daemon.NewBootstrap(daemon.BootstrapOptions{
		ConfigPath:   filepath.Join(dir, "skein.toml"),
		IdentityPath: filepath.Join(dir, "identity.key"),
		StatePath:    app.BootstrapStatePath(),
		AccessPath:   app.AccessPath,
	})
	if err != nil {
		_ = bootstrap.DeleteState(app.BootstrapStatePath())
		return fmt.Errorf("android: bootstrap server: %w", err)
	}
	// Serve the bootstrap-only handler over the in-process listener
	// with the same ConnContext as the normal control server, so the
	// handler sees the app's UID as the kernel caller.
	srv := &http.Server{Handler: bsrv.Handler(), ConnContext: api.CallerContext}
	// A normal daemon's shutdown closes its listener too. Onboarding after
	// reset must get a new generation, not serve the closed daemon listener.
	inproc.Reset()
	listener := inproc.Current()
	go func() { _ = srv.Serve(listener) }()
	bs, bsSrv, bsToken, bsVerb = bsrv, srv, token, verb
	go watchBootstrapOutcome(bsrv)
	return nil
}

// watchBootstrapOutcome closes a committed bootstrap (releasing the
// config-store lock) and resets the in-process listener, so the
// normal daemon can start cleanly afterwards. It watches until the
// bootstrap token's TTL is safely past (12 minutes), then gives up:
// a failed or abandoned bootstrap is left for StopBootstrap. The
// watcher must outlive the user's form-filling, not just the POST.
func watchBootstrapOutcome(bsrv *daemon.Bootstrap) {
	for i := 0; i < 3600; i++ {
		time.Sleep(200 * time.Millisecond)
		bsMu.Lock()
		if bs != bsrv {
			bsMu.Unlock()
			return
		}
		if _, ok := bsrv.Outcome(); ok {
			_ = stopBootstrapLocked()
			bsMu.Unlock()
			return
		}
		bsMu.Unlock()
	}
}

// TakeBootstrapToken returns the token EnsureBootstrap minted for
// verb, exactly once. The SetupService mint override calls this so a
// second mint (which bootstrap.Mint would refuse) never happens.
func TakeBootstrapToken(verb string) (string, bool) {
	bsMu.Lock()
	defer bsMu.Unlock()
	if bs == nil || bsVerb != verb || bsToken == "" {
		return "", false
	}
	tok := bsToken
	bsToken = ""
	return tok, true
}

// BootstrapRunning reports whether the onboarding bootstrap server
// is up.
func BootstrapRunning() bool {
	bsMu.Lock()
	defer bsMu.Unlock()
	return bs != nil
}

// StopBootstrap closes the bootstrap server (releasing the config
// lock) and resets the in-process listener. Safe to call when no
// bootstrap is running.
func StopBootstrap() error {
	bsMu.Lock()
	defer bsMu.Unlock()
	return stopBootstrapLocked()
}

func stopBootstrapLocked() error {
	if bs == nil {
		return nil
	}
	bsrv := bs
	srv := bsSrv
	bs, bsSrv, bsToken, bsVerb = nil, nil, "", ""
	// Close the HTTP server first so Serve unblocks, then release
	// the config-store lock; Reset drops the listener the bootstrap
	// handler served, so a later daemon gets a fresh one (host.Start
	// also Resets, but an abandoned onboarding must not wait for that).
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
	inproc.Reset()
	// Delete the token state too. A failed attempt (bad invite, bad
	// port) is rejected BEFORE the token is marked used, so the
	// state file still holds a valid, unused token — and Mint
	// refuses a fresh token while one is valid ("a bootstrap is
	// already in progress"). Without this delete, cancelling a
	// failed attempt would leave the user stuck until the token
	// expires, and switching verbs would fail the same way. On
	// success the state is already gone (commit deletes it), so
	// this is a no-op there; a missing file is not an error.
	_ = bootstrap.DeleteState(app.BootstrapStatePath())
	return bsrv.Close()
}

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
	if filesDir == abs {
		return nil
	}
	if d != nil {
		return errors.New("android: cannot change files dir while the daemon is running")
	}
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

// Connection reports the live dataplane state, separately from daemon liveness.
func Connection() api.Connection {
	hostMu.Lock()
	dd, err := d, lastErr
	hostMu.Unlock()
	if dd != nil {
		return dd.Connection()
	}
	st := api.Connection{State: daemon.ConnPaused}
	if err != nil {
		st.State, st.LastError = daemon.ConnError, err.Error()
	}
	return st
}

// Start loads the Android config and identity and runs the daemon in
// the background. The daemon starts paused; Connect resumes it once
// the VpnService has established the TUN and SetTunFd has delivered
// the FD.
func Start() error {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	// An abandoned onboarding bootstrap holds the config-store lock;
	// stop it before the normal daemon tries to acquire it.
	_ = StopBootstrap()
	hostMu.Lock()
	defer hostMu.Unlock()
	if d != nil {
		return nil
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
	// Keep the validated, persistable config intact. Android selects the
	// in-process listener independently; devTCPEnabled forbids TCP here.
	// Erasing these fields breaks every later config validation and save.

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
	done := make(chan struct{})
	d, cancel, runDone, lastErr = nd, cancelFn, done, nil
	go func() {
		err := nd.Run(ctx)
		_ = nd.Close()
		hostMu.Lock()
		if d == nd {
			d, cancel, runDone, lastErr = nil, nil, nil, err
		}
		hostMu.Unlock()
		close(done)
	}()
	return nil
}

// Stop cancels the daemon's run loop, waits briefly for it to exit,
// then releases its resources (TUN, link, config lock). It is safe
// to call when the daemon is not running.
func Stop() error {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	hostMu.Lock()
	dd, cancelFn, done := d, cancel, runDone
	hostMu.Unlock()
	if dd == nil {
		tun.DiscardAndroidFd()
		return nil
	}
	cancelFn()
	select {
	case <-done:
		tun.DiscardAndroidFd()
		return nil
	case <-time.After(5 * time.Second):
		// Closing the devices also releases any blocked packet read.
		_ = dd.Close()
	}
	select {
	case <-done:
		tun.DiscardAndroidFd()
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("android: daemon did not stop; retry after it releases the VPN")
	}
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
	// VpnService fixes the interface MTU when it establishes this generation.
	mtu := view.MTU
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
