package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/dataplane"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/link"
	"github.com/Utsav-X-bit/skein/internal/netcfg"
	"github.com/Utsav-X-bit/skein/internal/tun"
	"github.com/Utsav-X-bit/skein/internal/version"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

// Dataplane connection states, separate from the service state.
const (
	ConnConnected  = "connected"
	ConnPaused     = "paused"
	ConnConnecting = "connecting"
	ConnError      = "error"
)

// stateFile holds the paused flag across service restarts. It lives in the
// persistent state directory (StateDirectory=skein), not the ephemeral
// runtime directory.
func stateFile() string {
	if stateFileOverride != "" {
		return stateFileOverride
	}
	if p := securityEnv("SKEIN_STATE_FILE"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Skein", "state.json")
	}
	return "/var/lib/skein/state.json"
}

// connState is the persisted form of the paused flag.
type connState struct {
	Paused       bool   `json:"paused"`
	PausedReason string `json:"paused_reason,omitempty"`
	SinceMS      int64  `json:"since_ms,omitempty"`
}

func loadPausedState() connState {
	raw, err := os.ReadFile(stateFile())
	if err != nil {
		return connState{}
	}
	var st connState
	if err := json.Unmarshal(raw, &st); err != nil {
		log.Printf("skein: warning: state file unreadable, starting connected: %v", err)
	}
	return st
}

func savePausedState(st connState) error {
	path := stateFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// conn tracks the live connection state and the channels that move it.
type conn struct {
	mu        sync.Mutex
	state     string
	reason    string
	lastErr   string
	since     time.Time
	engCancel context.CancelFunc
	ctlCancel context.CancelFunc
	pauseCh   chan struct{} // closed by Pause to stop the dataplane
	resumeCh  chan struct{} // closed by Resume to start it
	downCh    chan struct{} // closed after teardown, for Pause to wait on
}

func (c *conn) setGeneration(engCancel, ctlCancel context.CancelFunc, pauseCh, resumeCh, downCh chan struct{}) {
	c.mu.Lock()
	c.engCancel, c.ctlCancel = engCancel, ctlCancel
	c.pauseCh, c.resumeCh, c.downCh = pauseCh, resumeCh, downCh
	c.mu.Unlock()
}

func (c *conn) snapshot() api.Connection {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// snapshotLocked is snapshot for callers already holding c.mu.
func (c *conn) snapshotLocked() api.Connection {
	return api.Connection{State: c.state, PausedReason: c.reason, LastError: c.lastErr, SinceMS: c.since.UnixMilli()}
}

func (c *conn) set(state, reason string, err error) {
	c.mu.Lock()
	if c.state != state {
		c.state = state
		c.since = time.Now()
	}
	c.reason, c.lastErr = reason, ""
	if err != nil {
		c.lastErr = err.Error()
	}
	c.mu.Unlock()
}

func (c *conn) isPaused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == ConnPaused
}

// Connection implements api.GUI.
func (d *Daemon) Connection() api.Connection { return d.conn.snapshot() }

// Hub implements api.GUI.
func (d *Daemon) Hub() *api.Hub { return d.hub }

// Identity implements api.GUI.
func (d *Daemon) Identity() api.Identity {
	return api.Identity{
		ID:        fmt.Sprintf("%016x", d.id.ID),
		PublicKey: base64.StdEncoding.EncodeToString(d.id.PublicKey),
		Name:      d.ctl.LocalName(),
	}
}

// Capabilities implements api.GUI.
func (d *Daemon) Capabilities() api.Capabilities {
	return api.Capabilities{
		AppVersion: version.Info(),
		Protocol:   version.Protocol,
		Platform:   runtime.GOOS,
		Features: map[string]bool{
			"connection_pause": true,
			"config_patch":     true,
			"invites":          true,
			"probe_async":      true,
			"firewall_repair":  true,
			"diagnostics":      true,
			"security":         true,
		},
	}
}

// Disconnect pauses the dataplane: engine stops, TUN/link close, interface
// and routes are removed (firewall rules stay), sessions drop, and the flag
// is persisted so a service restart stays paused.
func (d *Daemon) Disconnect(ctx context.Context) (api.Connection, error) {
	d.conn.mu.Lock()
	switch d.conn.state {
	case ConnPaused, ConnError:
		st := d.conn.snapshotLocked()
		d.conn.mu.Unlock()
		return st, &api.StatusError{Status: http.StatusConflict, Code: api.ErrCodeConflict, Message: "connection is " + st.State}
	case ConnConnecting:
		st := d.conn.snapshotLocked()
		d.conn.mu.Unlock()
		return st, &api.StatusError{Status: http.StatusConflict, Code: api.ErrCodeConflict, Message: "connection is changing"}
	}
	// Mark paused before signalling the loop. Otherwise the loop can observe
	// ConnConnecting after teardown and restart the dataplane before we set
	// the paused state, which made Disconnect look like a no-op.
	d.conn.state = ConnPaused
	d.conn.reason = "user"
	d.conn.lastErr = ""
	d.conn.since = time.Now()
	pauseCh, downCh := d.conn.pauseCh, d.conn.downCh
	engCancel, ctlCancel := d.conn.engCancel, d.conn.ctlCancel
	d.conn.mu.Unlock()

	engCancel()
	ctlCancel()
	close(pauseCh)
	// Wait for the loop to finish teardown (bounded: teardown is local IO).
	select {
	case <-downCh:
	case <-time.After(15 * time.Second):
	}
	if err := savePausedState(connState{Paused: true, PausedReason: "user", SinceMS: time.Now().UnixMilli()}); err != nil {
		log.Printf("skein: warning: persisting paused state: %v", err)
	}
	d.publishConnection()
	return d.Connection(), nil
}

// Connect resumes the dataplane after a pause. A departed node
// (Step 7, ADR-002) refuses: rejoin with a fresh invite first
// (system join clears the flag), then Connect. Otherwise a
// Connect alone would silently undo a leave.
func (d *Daemon) Connect(ctx context.Context) (api.Connection, error) {
	if d.departed() {
		return d.Connection(), &api.StatusError{Status: http.StatusConflict, Code: api.ErrCodeConflict, Message: "node has left its network; rejoin with a fresh invite first", Remediation: "call POST /api/v1/system/join with a fresh invite (confirm:true)"}
	}
	d.conn.mu.Lock()
	switch d.conn.state {
	case ConnConnected, ConnConnecting:
		st := d.conn.snapshotLocked()
		d.conn.mu.Unlock()
		return st, &api.StatusError{Status: http.StatusConflict, Code: api.ErrCodeConflict, Message: "connection is " + st.State}
	}
	d.conn.state = ConnConnecting
	resumeCh := d.conn.resumeCh
	d.conn.mu.Unlock()
	close(resumeCh)

	// The loop does the restart; poll for the settled state.
	deadline := time.Now().Add(30 * time.Second)
	for {
		st := d.conn.snapshot()
		if st.State != ConnConnecting {
			if st.State == ConnConnected {
				if err := savePausedState(connState{}); err != nil {
					log.Printf("skein: warning: persisting unpaused state: %v", err)
				}
			}
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, &api.StatusError{Status: http.StatusGatewayTimeout, Code: api.ErrCodeInternal, Message: "connection did not settle"}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (d *Daemon) publishConnection() {
	d.publish("connection:changed", d.conn.snapshot())
}

// connLoop owns the dataplane lifecycle across pause/resume cycles until ctx
// is cancelled. Step 5 (ADR-001 §5): a terminal dataplane error no
// longer kills the process — and with it the control plane. Instead
// the loop sets ConnError (with the error + remediation visible in
// GET /api/v1/status), tears the dataplane down, and waits for an
// explicit Connect retry or ctx cancellation. The control listener
// started by Daemon.Run keeps answering throughout: diagnostics
// stay queryable while the mesh side is down. Only a control-plane
// failure (bind failure in Run, before connLoop) is still fatal.
// waitForRetry reports whether Connect asked for a retry (true) or
// ctx was cancelled (false).
func waitForRetry(ctx context.Context, resumeCh chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	case <-resumeCh:
		return true
	}
}

// connLoop owns the dataplane lifecycle across pause/resume cycles until ctx
// is cancelled.
func (d *Daemon) connLoop(ctx context.Context) error {
	for {
		engCtx, engCancel := context.WithCancel(ctx)
		ctlCtx, ctlCancel := context.WithCancel(ctx)
		pauseCh := make(chan struct{})
		resumeCh := make(chan struct{})
		downCh := make(chan struct{})
		d.conn.setGeneration(engCancel, ctlCancel, pauseCh, resumeCh, downCh)

		if d.conn.isPaused() || d.departed() {
			// Service restarted while paused or departed: stay
			// down until Connect (paused) or rejoin+Connect
			// (departed — Connect alone refuses while departed,
			// so a departed node cannot wake itself here).
			select {
			case <-ctx.Done():
				return nil
			case <-resumeCh:
			}
			engCancel()
			ctlCancel()
			close(downCh)
			continue
		}

		if err := d.startDataPlane(); err != nil {
			engCancel()
			ctlCancel()
			close(downCh)
			d.conn.set(ConnError, "", err)
			d.publishConnection()
			log.Printf("skein: connection error: %v (control plane stays up; retry with Connect)", err)
			// Step 5: stay alive in ConnError for diagnostics and
			// an explicit retry, instead of exiting and taking
			// the control plane down with the mesh.
			if !waitForRetry(ctx, resumeCh) {
				return nil
			}
			continue
		}
		// The TUN interface now exists; add the interface-scoped firewall
		// rules without blocking the dataplane or the control API.
		cfg := d.config()
		go func() {
			if firewallDisabled() {
				return
			}
			if err := firewall.Ensure(firewall.Config{
				Interface: cfg.Node.Interface,
				Port:      cfg.Node.ListenPort,
				PrefixV4:  cfg.Node.OverlayIPv4Prefix.String(),
				PrefixV6:  cfg.Node.OverlayIPv6Prefix.String(),
			}); err != nil {
				log.Printf("skein: warning: firewall: %v", err)
			}
		}()
		d.conn.set(ConnConnected, "", nil)
		d.publishConnection()

		go func() { _ = d.ctl.Run(ctlCtx) }()
		errCh := make(chan error, 1)
		go func() { errCh <- d.eng.Run(engCtx) }()

		select {
		case <-ctx.Done():
		case <-pauseCh:
		case <-errCh:
		}
		engCancel()
		ctlCancel()
		engErr := <-errCh
		d.teardownDataPlane()
		close(downCh)
		if ctx.Err() != nil {
			return nil
		}
		if d.conn.isPaused() {
			continue
		}
		if engErr != nil {
			d.conn.set(ConnError, "", engErr)
			d.publishConnection()
			log.Printf("skein: connection error: %v (control plane stays up; retry with Connect)", engErr)
			// Step 5: same ConnError survival as the startup
			// path above — the mesh side restarts on Connect,
			// the process and its control plane do not die.
			if !waitForRetry(ctx, resumeCh) {
				return nil
			}
			continue
		}
	}
}

// startDataPlane opens the underlay and TUN (if a pause closed them), builds
// a fresh engine, and configures the interface and routes.
func (d *Daemon) startDataPlane() error {
	cfg := d.config()
	var err error
	d.dataPlaneMu.Lock()
	defer d.dataPlaneMu.Unlock()
	if d.link == nil {
		d.link, err = link.Listen(cfg.Node.ListenPort)
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				return fmt.Errorf("%w (another daemon is already running; stop the service with `systemctl stop skein` or `sc stop skein`)", err)
			}
			return err
		}
	}
	if d.tun == nil {
		d.tun, err = tun.Open(cfg.Node.Interface, cfg.Node.MTU)
		if err != nil {
			_ = d.link.Close()
			d.link = nil
			return err
		}
	}

	addresses, routes := overlayPrefixes(cfg, d.overlayV6)
	if warnings, err := netcfg.Conflicts(routes); err == nil {
		for _, w := range warnings {
			log.Printf("skein: warning: %s", w)
		}
	}
	if err := netcfg.Reconcile(cfg.Node.Interface, routes, routes); err != nil {
		log.Printf("skein: warning: reconcile: %v", err)
	}
	if err := netcfg.Configure(cfg.Node.Interface, cfg.Node.MTU, addresses, routes); err != nil {
		d.closeDevicesLocked()
		return err
	}
	// An address the host cannot answer on would be advertised to peers as
	// reachable. Verify it now, before the engine and the controller are
	// built, and fall back to IPv4-only when it does not answer.
	if d.overlayV6.IsValid() && !netcfg.OverlayIPv6Usable(cfg.Node.Interface, d.overlayV6) {
		log.Printf("skein: warning: overlay IPv6 %s does not answer on %s; running IPv4-only", d.overlayV6, cfg.Node.Interface)
		_ = netcfg.Remove(cfg.Node.Interface,
			[]netip.Prefix{netip.PrefixFrom(d.overlayV6, cfg.Node.OverlayIPv6Prefix.Bits())},
			[]netip.Prefix{cfg.Node.OverlayIPv6Prefix})
		d.overlayV6 = netip.Addr{}
		if d.ctl != nil {
			d.ctl.SetLocalOverlay(cfg.Node.OverlayIPv4, netip.Addr{})
		}
		d.merger.SetLocalOverlay(cfg.Node.OverlayIPv4, netip.Addr{})
	}

	eng, err := dataplane.New(dataplane.Config{
		LocalID:   d.id.ID,
		Identity:  d.id,
		OverlayV4: cfg.Node.OverlayIPv4,
		OverlayV6: d.overlayV6,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		MTU:       cfg.Node.MTU,
	}, dataplane.Deps{
		TUN:      d.tun,
		Link:     d.link,
		Peers:    d.peers,
		Routes:   d.routes,
		Sessions: d.sessions,
	})
	if err != nil {
		d.closeDevicesLocked()
		return err
	}
	eng.SetControlHandler(d.ctl.HandleControl)
	eng.SetPingHandler(d.ctl.HandlePing)
	eng.SetSessionHandler(d.sessionEstablished)
	if !d.link.HasTCP() {
		log.Printf("skein: warning: TCP fallback unavailable on port %d; UDP only", cfg.Node.ListenPort)
	}
	d.eng = eng

	// A new interface starts at the configured MTU; measure the paths again.
	d.resetMTUNegotiation()
	if d.ctl != nil {
		d.ctl.ResetMTUProbes()
	}
	return nil
}

// closeDevicesLocked releases the TUN and link; the caller holds
// dataPlaneMu.
func (d *Daemon) closeDevicesLocked() {
	if d.tun != nil {
		_ = d.tun.Close()
		d.tun = nil
	}
	if d.link != nil {
		_ = d.link.Close()
		d.link = nil
	}
}

// teardownDataPlane stops the engine and removes the dataplane, leaving the
// firewall rules in place.
func (d *Daemon) teardownDataPlane() {
	// Remove addresses/routes BEFORE closing the engine and devices:
	// closing either deletes the adapter, so a later netsh remove
	// fails with "interface not found" (M9 §2c: every shutdown
	// logged that warning). A remove failure stays a warning; the
	// devices are closed regardless.
	cfg := d.config()
	addresses, routes := overlayPrefixes(cfg, d.overlayV6)
	if err := netcfg.Remove(cfg.Node.Interface, addresses, routes); err != nil {
		log.Printf("skein: warning: removing network configuration: %v", err)
	}

	d.dataPlaneMu.Lock()
	eng := d.eng
	d.eng = nil
	if eng != nil {
		for _, p := range d.peers.List() {
			eng.DropSession(p.ID)
		}
		_ = eng.Close()
	}
	d.closeDevicesLocked()
	d.dataPlaneMu.Unlock()
}

// overlayPrefixes lists the overlay addresses and routes for the config.
func overlayPrefixes(cfg conf.Config, overlayV6 netip.Addr) (addresses, routes []netip.Prefix) {
	addresses = []netip.Prefix{netip.PrefixFrom(cfg.Node.OverlayIPv4, cfg.Node.OverlayIPv4Prefix.Bits())}
	routes = []netip.Prefix{cfg.Node.OverlayIPv4Prefix}
	if overlayV6.IsValid() {
		addresses = append(addresses, netip.PrefixFrom(overlayV6, cfg.Node.OverlayIPv6Prefix.Bits()))
		routes = append(routes, cfg.Node.OverlayIPv6Prefix)
	}
	return
}

// engine returns the live dataplane engine, or nil while paused.
func (d *Daemon) engine() *dataplane.Engine {
	// Status must not block behind a slow TUN/firewall startup.
	if !d.dataPlaneMu.TryLock() {
		return nil
	}
	defer d.dataPlaneMu.Unlock()
	return d.eng
}

// engineAdapter forwards the control layer's dataplane calls to whatever
// engine is live, so the controller outlives pause/resume engine swaps.
type engineAdapter struct{ d *Daemon }

func (a engineAdapter) SendControl(peerID uint64, typ wire.MessageType, payload []byte) error {
	if e := a.d.engine(); e != nil {
		return e.SendControl(peerID, typ, payload)
	}
	return errors.New("dataplane paused")
}

func (a engineAdapter) SendControlTo(peerID uint64, addr netip.AddrPort, typ wire.MessageType, payload []byte) error {
	if e := a.d.engine(); e != nil {
		return e.SendControlTo(peerID, addr, typ, payload)
	}
	return errors.New("dataplane paused")
}

func (a engineAdapter) Rekey(peerID uint64) error {
	if e := a.d.engine(); e != nil {
		return e.Rekey(peerID)
	}
	return nil
}

func (a engineAdapter) HasSession(peerID uint64) bool {
	e := a.d.engine()
	return e != nil && e.HasSession(peerID)
}

func (a engineAdapter) EnsureSession(peerID uint64) error {
	if e := a.d.engine(); e != nil {
		return e.EnsureSession(peerID)
	}
	return errors.New("dataplane paused")
}

func (a engineAdapter) AllowFallbackDial(peerID uint64) {
	if e := a.d.engine(); e != nil {
		e.AllowFallbackDial(peerID)
	}
}

func (a engineAdapter) DropSession(peerID uint64) {
	if e := a.d.engine(); e != nil {
		e.DropSession(peerID)
	}
}

func (a engineAdapter) Punch(peerID uint64, addr netip.AddrPort) error {
	if e := a.d.engine(); e != nil {
		return e.Punch(peerID, addr)
	}
	return errors.New("dataplane paused")
}

func (a engineAdapter) ProbeMTU(peerID uint64, ceiling int) (int, error) {
	if e := a.d.engine(); e != nil {
		return e.ProbeMTU(peerID, ceiling)
	}
	return 0, errors.New("dataplane paused")
}
