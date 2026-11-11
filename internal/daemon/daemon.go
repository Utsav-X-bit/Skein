// Package daemon composes the link, TUN, sessions, discovery, control, and API
// into one running node.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/activity"
	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/configstore"
	"github.com/Utsav-X-bit/skein/internal/control"
	"github.com/Utsav-X-bit/skein/internal/dataplane"
	"github.com/Utsav-X-bit/skein/internal/discovery"
	"github.com/Utsav-X-bit/skein/internal/identity"
	"github.com/Utsav-X-bit/skein/internal/link"
	"github.com/Utsav-X-bit/skein/internal/netcfg"
	"github.com/Utsav-X-bit/skein/internal/path"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/route"
	"github.com/Utsav-X-bit/skein/internal/session"
	"github.com/Utsav-X-bit/skein/internal/tun"
	"github.com/Utsav-X-bit/skein/internal/version"
	"github.com/Utsav-X-bit/skein/internal/wan"
)

// Daemon is one running Skein node.
type Daemon struct {
	cfg conf.Config
	id  identity.Identity

	peers    *peer.Table
	routes   *route.Table
	sessions *session.Manager
	merger   *discovery.Merger
	activity *activity.Cache
	eng      *dataplane.Engine
	ctl      *control.Controller

	link  *link.Switch
	tun   tun.Device
	mdns  *discovery.MDNS
	api   *api.Server
	http  *http.Server
	start time.Time

	// handoff is the control listener bound by bootstrap mode, adopted
	// by Run instead of rebinding it (B16).
	handoff *ControlHandoff

	events      chan api.Event
	hub         *api.Hub
	logs        *logRing
	redact      *redactor
	conn        *conn
	dataPlaneMu sync.Mutex
	// mtuMu guards the per-connection MTU negotiation: one report per
	// directly connected peer, applied once when all have reported.
	mtuMu     sync.Mutex
	mtuReport map[uint64]int
	mtuDone   bool
	// setMTU applies the negotiated interface MTU; tests replace it.
	setMTU    func(name string, mtu int) error
	closeOnce sync.Once
	closeErr  error
	persistMu sync.Mutex
	cfgMu     sync.RWMutex
	// sysMu serialises Step 7 system operations (rotate/reset/leave/
	// import/update) against each other. It does not replace
	// persistMu (config writes) — a system op holds sysMu for its
	// whole multi-step sequence, and persistLocked holds persistMu
	// for each write inside it.
	sysMu sync.Mutex
	// updateState is the last update outcome (in-memory copy of
	// what the detached apply process writes to the state file).
	// Guarded by sysMu.
	updateState *api.UpdateState
	// updateLauncherOverride spawns the detached apply process.
	// Nil means the platform spawnDetached. Tests swap it for a
	// recorder.
	updateLauncherOverride func(exe string, args []string, logPath string) error
	// stagingDirOverride, when set, is the update staging directory
	// (tests). Production derives it from the installed service
	// binary's directory, falling back to the running exe's dir.
	stagingDirOverride string
	// store is the single config writer (ADR-001 §4). It holds the
	// cross-process lock for the daemon's lifetime when the daemon was
	// built from a config file; nil for in-memory/test daemons with no
	// config path, where persistLocked returns early anyway.
	store *configstore.Store
}

// config returns a snapshot safe to read outside the daemon lock.
func (d *Daemon) config() conf.Config {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	cfg := d.cfg
	cfg.Peers = append([]conf.Peer(nil), d.cfg.Peers...)
	return cfg
}

// New builds a daemon and opens its TUN device and UDP socket.
func New(cfg conf.Config, id identity.Identity) (*Daemon, error) {
	peers := peer.NewTable()
	routes := route.NewTable(id.ID)
	sessions, err := session.NewManager(id, cfg.Node.NetworkSecret)
	if err != nil {
		return nil, err
	}
	merger := discovery.NewMerger(id.ID, peers, routes)

	if cfg.Node.Name == "" {
		cfg.Node.Name = conf.DefaultNodeName()
	}
	d := &Daemon{
		cfg:      cfg,
		id:       id,
		peers:    peers,
		routes:   routes,
		sessions: sessions,
		merger:   merger,
		activity: activity.NewCache(32),
		start:    time.Now(),
		events:   make(chan api.Event, 64),
		hub:      api.NewHub(),
		logs:     &logRing{},
		setMTU:   netcfg.SetMTU,
	}
	d.redact = newRedactor(cfg)
	if st := loadPausedState(); st.Paused {
		// A service restart honors a persisted pause: stay down until Connect.
		d.conn = &conn{state: ConnPaused, reason: st.PausedReason, since: time.Now()}
	} else {
		d.conn = &conn{state: ConnConnecting, since: time.Now()}
	}

	for _, p := range cfg.Peers {
		if err := d.registerPeer(p.ID, p.PublicKey, p.Addrs, p.Dialer, p.Name); err != nil {
			return nil, err
		}
	}

	// The underlay, TUN, and engine are (re)opened by the connection loop so
	// a pause can close them without touching the service. The controller
	// talks to the live engine through an adapter that survives swaps.
	ctl, err := control.New(control.Local{
		ID:         id.ID,
		Name:       cfg.Node.Name,
		PublicKey:  id.PublicKey,
		OverlayV4:  cfg.Node.OverlayIPv4,
		OverlayV6:  cfg.Node.OverlayIPv6,
		Candidates: localCandidates(cfg),
	}, peers, routes, merger, engineAdapter{d})
	if err != nil {
		return nil, err
	}

	merger.SetLocalOverlay(cfg.Node.OverlayIPv4, cfg.Node.OverlayIPv6)
	merger.SetLocalPrefixes(cfg.Node.OverlayIPv4Prefix, cfg.Node.OverlayIPv6Prefix)
	merger.OnPeer = func(peerID uint64, publicKey []byte) error {
		// Learned peers use the same rule as mDNS: the lower ID dials.
		return d.registerPeer(peerID, publicKey, nil, d.id.ID < peerID, "")
	}
	merger.OnActivity = func(origin, seq uint64, records []activity.Record, expires time.Time) {
		for _, record := range records {
			d.activity.Put(origin, seq, record, expires)
			d.emitActivitySample(origin, seq, record, expires)
		}
	}

	ctl.SetEventHandler(func(typ string, peerID uint64) {
		d.emit(api.Event{Type: typ, PeerID: fmt.Sprintf("%016x", peerID)})
	})
	ctl.SetMTUHandler(d.applyNegotiatedMTU)
	d.ctl = ctl
	d.api = api.NewServer(cfg.API.Token, d, d, d)
	// Step 1: authorisation table from the access file, and the audit
	// hook. A missing access file leaves the table unconfigured (kernel
	// callers read-only); a malformed one fails daemon creation, so the
	// daemon never runs with a silently ignored grant file. Audit records
	// carry caller, method, path, and status only — never a secret, even
	// for secret endpoints (there the record is flagged, not filled).
	if err := d.api.LoadGrantsFromFile(controlAccessFile); err != nil {
		return nil, fmt.Errorf("daemon: access file: %w", err)
	}
	d.api.Audit(func(ev api.AuditEvent) {
		// Step 5: denied attempts log required vs held role.
		// The record contains no token, secret, or request body
		// by construction (api.AuditEvent has no such field),
		// so nothing secret-bearing can reach this log line.
		if ev.Denied {
			log.Printf("skein: audit denied caller=%s %s %s status=%d required=%s held=%s", ev.Caller, ev.Method, ev.Path, ev.Status, ev.Required, ev.Held)
			return
		}
		if ev.Secret {
			log.Printf("skein: audit caller=%s %s %s status=%d secret_endpoint=true", ev.Caller, ev.Method, ev.Path, ev.Status)
			return
		}
		log.Printf("skein: audit caller=%s %s %s status=%d", ev.Caller, ev.Method, ev.Path, ev.Status)
	})
	log.SetOutput(io.MultiWriter(os.Stderr, &logWriter{ring: d.logs, red: d.redact, hub: d.hub}))
	// A peer that proves the network secret may join without a configured
	// entry; the invite carries the secret, so holding it is membership.
	sessions.SetAcceptUnknown(true)
	sessions.SetPeerLearnedHandler(d.peerLearned)

	// Step 3 (ADR-001 §4): the daemon holds the config lock for its
	// lifetime. A second daemon for the same config fails here, before
	// any mesh work, instead of racing the first daemon's persists.
	// Daemons built without a config path (tests, in-memory) have no
	// file to lock and skip this.
	if cfg.Path != "" {
		store, err := configstore.Open(cfg.Path)
		if err != nil {
			return nil, fmt.Errorf("daemon: config store: %w", err)
		}
		if err := store.Lock(); err != nil {
			store.Close()
			return nil, fmt.Errorf("daemon: %w", err)
		}
		d.store = store
	}

	return d, nil
}

// localCandidates lists this node's interface endpoints.
func localCandidates(cfg conf.Config) []netip.AddrPort {
	cands, err := path.LocalCandidates(cfg.Node.ListenPort)
	if err != nil {
		return nil
	}
	return path.ExcludePrefixes(cands, []netip.Prefix{
		cfg.Node.OverlayIPv4Prefix,
		cfg.Node.OverlayIPv6Prefix,
	})
}

// allCandidates adds the configured public address and the STUN-discovered
// one, so invites and STATE advertise where peers can actually reach us.
func allCandidates(ctx context.Context, cfg conf.Config) []netip.AddrPort {
	return wan.Merge(localCandidates(cfg), wan.Candidates(ctx, cfg))
}

// refreshWAN keeps the advertised public endpoint current.
func (d *Daemon) refreshWAN(ctx context.Context) {
	update := func() {
		d.ctl.SetLocalCandidates(allCandidates(ctx, d.config()))
	}
	update()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

func (d *Daemon) registerPeer(id uint64, publicKey []byte, addrs []netip.AddrPort, dialer bool, name string) error {
	if id == d.id.ID {
		return errors.New("daemon: refusing to register the local node as a peer")
	}
	if err := d.sessions.SetPeer(id, publicKey); err != nil {
		return err
	}
	// A discovery/gossip update must not flip the configured dialer role and
	// leave both sides waiting for the other to connect.
	if existing, ok := d.peers.Get(id); ok {
		dialer = existing.Dialer
	}
	if err := d.peers.Upsert(peer.Peer{
		ID:        id,
		PublicKey: publicKey,
		Dialer:    dialer,
	}); err != nil {
		return err
	}
	for _, addr := range addrs {
		d.peers.AddCandidate(id, addr, peer.SourceConfig)
	}
	d.peers.SetName(id, name)
	return nil
}

func (d *Daemon) peerLearned(id uint64, publicKey []byte) {
	if id == d.id.ID {
		return
	}
	if _, ok := d.peers.Get(id); ok {
		return
	}
	if err := d.peers.Upsert(peer.Peer{ID: id, PublicKey: publicKey, Dialer: d.id.ID < id, State: peer.StateDown}); err != nil {
		log.Printf("skein: warning: learned peer %016x rejected: %v", id, err)
		return
	}
	d.emit(api.Event{Type: "peer_joined", PeerID: fmt.Sprintf("%016x", id)})
	d.persistPeers()
	d.emitPeerChanged()
}

// persistPeers rewrites the configuration so learned peers survive a restart.
// The daemon owns the peer list; stop it before editing peers by hand.
func (d *Daemon) persistPeers() {
	d.persistMu.Lock()
	defer d.persistMu.Unlock()
	d.persistLocked(d.config())
}

// persistLocked rewrites cfg with the current peer list. The caller holds
// persistMu. Step 3: the write goes through configstore, the single
// config writer — never a direct temp + rename here again. A daemon built
// by New holds the lifetime lock, so this is the fast path; a daemon
// built by hand (tests) without a store takes a brief lock instead.
func (d *Daemon) persistLocked(base conf.Config) {
	if base.Path == "" {
		return
	}
	cfg := configWithPeers(base, d.peers.List())
	store := d.store
	if store == nil {
		var err error
		store, err = configstore.Open(base.Path)
		if err != nil {
			log.Printf("skein: warning: cannot persist peers: %v", err)
			return
		}
		defer store.Close()
	}
	if err := store.Write(cfg); err != nil {
		log.Printf("skein: warning: cannot persist peers: %v", err)
		return
	}
	d.cfgMu.Lock()
	d.cfg = cfg
	d.cfgMu.Unlock()
}

func (d *Daemon) sessionEstablished(peerID uint64) {
	// STATE sequence counters are process-local. A newly authenticated session
	// can therefore belong to a restarted peer whose next state starts at one.
	d.merger.Reset(peerID)
	d.routes.ResetOrigin(peerID)
	d.activity.ResetOrigin(peerID)
	d.emit(api.Event{Type: "peer_up", PeerID: fmt.Sprintf("%016x", peerID)})
	d.emitPeerChanged()
	d.emitRouteChanged()
}

// AdoptControlHandoff makes Run serve the control API on the listener
// bootstrap mode already bound, instead of closing and rebinding it.
// It must be called before Run.
func (d *Daemon) AdoptControlHandoff(h *ControlHandoff) { d.handoff = h }

// Run runs every loop until ctx is cancelled. The dataplane (interface,
// routes, TUN, link, engine) is owned by connLoop so it can be paused and
// resumed without stopping the service.
func (d *Daemon) Run(ctx context.Context) error {
	cfg := d.config()

	// Step 2 (ADR-001 §5): the single control listener starts
	// synchronously, before mDNS, TUN creation, firewall changes, or
	// connLoop. A configured transport that fails to bind is fatal:
	// Run returns the error and the process exits, instead of running
	// headless with a dead control plane (the old serveControl
	// behaviour). An unconfigured transport (dev mode) returns a no-op
	// stop and is logged loudly by StartControlListeners.
	apiCtx, stopAPI := context.WithCancel(ctx)
	defer stopAPI()
	stopControl, err := d.StartControlListeners(apiCtx)
	if err != nil {
		return fmt.Errorf("control listener: %w", err)
	}
	defer stopControl()

	for _, p := range d.peers.List() {
		addrs := make([]string, 0, len(p.Candidates))
		for _, c := range p.Candidates {
			addrs = append(addrs, c.Addr.String())
		}
		log.Printf("skein: peer %016x dialer=%v candidates=%v", p.ID, p.Dialer, addrs)
	}
	log.Printf("skein: mesh status: run \"skein dashboard\" in another terminal")

	if cfg.Discovery.MDNS {
		inv := identity.Invite{ID: d.id.ID, PublicKey: d.id.PublicKey, Addrs: localCandidates(cfg)}
		mdns, err := discovery.StartMDNS(ctx, inv, discovery.Fingerprint(cfg.Node.NetworkSecret), cfg.Node.ListenPort, d.discovered)
		if err != nil {
			log.Printf("skein: warning: mDNS unavailable: %v", err)
		} else {
			d.mdns = mdns
			defer mdns.Close()
		}
	}

	// Step 5 (ADR-001 §5): dev TCP is dev-only. It starts only when
	// no kernel transport (socket/pipe) is configured — a config
	// with a kernel transport never gets a second TCP control plane,
	// even if api.listen is also set: that combination is refused
	// outright for system configs at the app layer (see
	// app.checkSystemDevTCP), and here the daemon logs loudly and
	// serves the kernel plane only. Dev TCP still serves the same
	// wrapRole-gated handler, so a bearer client never exceeds read
	// (Step 1), including on secret-role endpoints.
	if devTCPEnabled(cfg) {
		d.http = &http.Server{
			Addr:              cfg.API.Listen,
			Handler:           d.api.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    64 << 10,
			BaseContext:       func(net.Listener) context.Context { return apiCtx },
		}
		go d.serveDevTCP(apiCtx, d.http)
	} else if cfg.API.Listen != "" {
		log.Printf("skein: warning: api.listen ignored: kernel control transport is configured; control API is socket/pipe only")
	}
	go d.refreshWAN(ctx)
	go d.pruneStalePeers(ctx)

	return d.connLoop(ctx)
}

// devTCPEnabled reports whether the dev-only TCP control listener may
// start for cfg. Step 5: dev TCP exists only for dev configs (listen
// set, no socket/pipe). Any config with a kernel transport — in
// particular every system/service config — gets the kernel plane
// only; api.listen on such a config is ignored here and refused at
// the app layer for system configs.
func devTCPEnabled(cfg conf.Config) bool {
	return cfg.API.Listen != "" && cfg.API.Socket == "" && cfg.API.Pipe == ""
}

// serveDevTCP is the dev-only loopback TCP control listener. It is
// never started for a service config (see devTCPEnabled), and it
// serves the same authorisation-gated handler as the kernel plane,
// so bearer clients are capped at read and secret-role endpoints
// stay 403 over TCP.
func (d *Daemon) serveDevTCP(ctx context.Context, srv *http.Server) {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Printf("skein: error: API listen: %v", err)
		return
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("skein: error: API server: %v", err)
	}
}

func (d *Daemon) discovered(inv identity.Invite) {
	if inv.ID == d.id.ID {
		return
	}
	dialer := d.id.ID < inv.ID
	if err := d.registerPeer(inv.ID, inv.PublicKey, inv.Addrs, dialer, ""); err != nil {
		log.Printf("skein: warning: discovered peer %016x rejected: %v", inv.ID, err)
		return
	}
	for _, addr := range inv.Addrs {
		d.peers.AddCandidate(inv.ID, addr, peer.SourceMDNS)
	}
	d.emit(api.Event{Type: "peer_discovered", PeerID: fmt.Sprintf("%016x", inv.ID)})
	d.emitPeerChanged()
}

// Close releases every resource. It is safe to call more than once.
func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		if d.mdns != nil {
			d.mdns.Close()
		}
		d.dataPlaneMu.Lock()
		eng, tunDev, linkDev := d.eng, d.tun, d.link
		d.eng, d.tun, d.link = nil, nil, nil
		d.dataPlaneMu.Unlock()
		if eng != nil {
			_ = eng.Close()
		}
		if d.http != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := d.http.Shutdown(ctx); err != nil {
				_ = d.http.Close()
			}
			cancel()
		}
		if tunDev != nil {
			if err := tunDev.Close(); err != nil {
				d.closeErr = err
			}
		}
		if linkDev != nil {
			if err := linkDev.Close(); err != nil && d.closeErr == nil {
				d.closeErr = err
			}
		}
		// Releasing the config lock lets a restarted daemon re-acquire
		// it; the OS would release it on process death anyway.
		if d.store != nil {
			_ = d.store.Close()
		}
	})
	return d.closeErr
}

// configWithPeers returns a copy of cfg whose peer list mirrors the live table.
func configWithPeers(cfg conf.Config, peers []peer.Peer) conf.Config {
	cfg.Peers = nil
	for _, p := range peers {
		peerCfg := conf.Peer{ID: p.ID, Name: p.Name, PublicKey: p.PublicKey, Dialer: p.Dialer}
		for _, cand := range p.Candidates {
			peerCfg.Addrs = append(peerCfg.Addrs, cand.Addr)
		}
		cfg.Peers = append(cfg.Peers, peerCfg)
	}
	return cfg
}

// Status implements api.StateReader.
func (d *Daemon) Status() api.Status {
	var snap dataplane.Snapshot
	if e := d.engine(); e != nil {
		snap = e.Counters()
	}
	cfg := d.config()
	st := api.Status{
		ID:            fmt.Sprintf("%016x", d.id.ID),
		Name:          d.ctl.LocalName(),
		OverlayIPv4:   cfg.Node.OverlayIPv4.String(),
		UptimeSeconds: int64(time.Since(d.start).Seconds()),
		Version:       version.Info(),
		ListenPort:    cfg.Node.ListenPort,
		PeerCount:     d.peers.Len(),
		RxBytes:       snap.RxBytes,
		TxBytes:       snap.TxBytes,
		Connection:    d.Connection(),
	}
	if cfg.Node.OverlayIPv6.IsValid() {
		st.OverlayIPv6 = cfg.Node.OverlayIPv6.String()
	}
	st.Health = d.health(st.Connection)
	return st
}

func (d *Daemon) health(c api.Connection) string {
	if c.State == "error" {
		return "error"
	}
	if c.State == "paused" {
		return "offline"
	}
	for _, p := range d.peers.List() {
		if p.State != "up" {
			return "degraded"
		}
	}
	return "ok"
}

// Peers implements api.StateReader.
func (d *Daemon) Peers() []api.Peer {
	list := d.peers.List()
	out := make([]api.Peer, 0, len(list))
	now := time.Now()
	for _, p := range list {
		row := api.Peer{
			ID:      fmt.Sprintf("%016x", p.ID),
			Name:    p.Name,
			State:   string(p.State),
			Path:    p.Path,
			RTTMs:   float64(p.RTT) / float64(time.Millisecond),
			Dialer:  p.Dialer,
			Direct:  p.Direct,
			RxBytes: p.RxBytes,
			TxBytes: p.TxBytes,
		}
		if p.OverlayV4.IsValid() {
			row.OverlayIPv4 = p.OverlayV4.String()
		}
		if p.OverlayV6.IsValid() {
			row.OverlayIPv6 = p.OverlayV6.String()
		}
		if p.ActiveEndpoint.IsValid() {
			row.Endpoint = p.ActiveEndpoint.String()
		}
		row.Transport = p.Transport
		row.TransportRx = p.RxTransport
		if !p.LastSeen.IsZero() {
			row.LastSeenMS = now.Sub(p.LastSeen).Milliseconds()
		}
		if !p.SessionStart.IsZero() {
			ms := p.SessionStart.UnixMilli()
			row.SessionStartMS = &ms
		}
		if !p.LastHandshake.IsZero() {
			ms := p.LastHandshake.UnixMilli()
			row.LastHandshakeMS = &ms
		}
		for _, c := range p.Candidates {
			row.Candidates = append(row.Candidates, c.Addr.String())
		}
		out = append(out, row)
	}
	return out
}

// Routes implements api.StateReader.
func (d *Daemon) Routes() []api.Route {
	list := d.routes.Routes()
	out := make([]api.Route, 0, len(list))
	for _, r := range list {
		row := api.Route{
			Destination: fmt.Sprintf("%016x", r.Destination),
			NextHop:     fmt.Sprintf("%016x", r.NextHop),
			CostMs:      float64(r.Cost) / float64(time.Millisecond),
			Hops:        r.Hops,
		}
		for _, id := range r.Path {
			row.Path = append(row.Path, fmt.Sprintf("%016x", id))
		}
		out = append(out, row)
	}
	return out
}

// Activities exposes fresh authenticated aggregate samples for the dashboard.
func (d *Daemon) Activities() []api.Activity {
	samples := d.activity.Snapshot(time.Now())
	out := make([]api.Activity, 0, len(samples))
	for _, sample := range samples {
		row := api.Activity{Origin: fmt.Sprintf("%016x", sample.Origin), Destination: fmt.Sprintf("%016x", sample.Destination), Seq: sample.Seq, RxBytes: sample.RxBytes, TxBytes: sample.TxBytes, RTTMs: float64(sample.RTT) / float64(time.Millisecond), JitterMs: float64(sample.Jitter) / float64(time.Millisecond), Transport: sample.Transport.String(), Direct: sample.Direct, ObservedMS: sample.ObservedAt.UnixMilli(), ExpiresMS: sample.ExpiresAt.UnixMilli()}
		for _, id := range sample.Path {
			row.Path = append(row.Path, fmt.Sprintf("%016x", id))
		}
		out = append(out, row)
	}
	return out
}

// Config implements api.StateReader with secrets redacted.
func (d *Daemon) Config() api.ConfigView {
	cfg := d.config()
	view := api.ConfigView{
		Name:              cfg.Node.Name,
		OverlayIPv4Prefix: cfg.Node.OverlayIPv4Prefix.String(),
		OverlayIPv6Prefix: cfg.Node.OverlayIPv6Prefix.String(),
		OverlayIPv4:       cfg.Node.OverlayIPv4.String(),
		ListenPort:        cfg.Node.ListenPort,
		MTU:               cfg.Node.MTU,
		Interface:         cfg.Node.Interface,
		APIListen:         cfg.API.Listen,
		APISocket:         cfg.API.Socket,
		APIPipe:           cfg.API.Pipe,
		MDNS:              cfg.Discovery.MDNS,
		STUN:              cfg.Discovery.STUN,
		STUNServer:        cfg.Discovery.STUNServer,
		ConfigPath:        cfg.Path,
		Departed:          cfg.Node.Departed,
		Protocol:          version.Protocol,
		NetworkSecret:     "redacted",
		APIToken:          "redacted",
	}
	if cfg.Node.OverlayIPv6.IsValid() {
		view.OverlayIPv6 = cfg.Node.OverlayIPv6.String()
	}
	if cfg.Node.PublicAddr.IsValid() {
		view.PublicAddr = cfg.Node.PublicAddr.String()
	}
	for _, p := range cfg.Peers {
		view.Peers = append(view.Peers, fmt.Sprintf("%016x", p.ID))
	}
	return view
}

// Events implements api.StateReader.
func (d *Daemon) Events() <-chan api.Event { return d.events }

// AddPeer implements api.PeerWriter.
func (d *Daemon) AddPeer(invite string) error {
	inv, err := identity.DecodeInvite(invite)
	if err != nil {
		return err
	}
	if inv.ID == d.id.ID {
		return errors.New("daemon: invite is for this node")
	}
	dialer := d.id.ID < inv.ID
	if err := d.registerPeer(inv.ID, inv.PublicKey, inv.Addrs, dialer, ""); err != nil {
		return err
	}
	for _, addr := range inv.Addrs {
		d.peers.AddCandidate(inv.ID, addr, peer.SourceConfig)
	}
	d.emit(api.Event{Type: "peer_added", PeerID: fmt.Sprintf("%016x", inv.ID)})
	d.persistPeers()
	d.emitPeerChanged()
	return nil
}

// SetName implements api.PeerWriter.
func (d *Daemon) SetName(name string) error {
	name = strings.TrimSpace(name)
	if err := conf.ValidateName(name); err != nil {
		return err
	}
	d.persistMu.Lock()
	d.cfgMu.Lock()
	d.cfg.Node.Name = name
	cfg := d.cfg
	d.cfgMu.Unlock()
	d.ctl.SetLocalName(name)
	d.persistLocked(cfg)
	d.persistMu.Unlock()
	d.emit(api.Event{Type: "name_changed"})
	return nil
}

// Step 12 (ADR-002): Daemon.RemovePeer is deleted with the DELETE
// endpoint. No peer removes another peer; a peer leaves itself via
// the system/leave endpoint. (session.Manager.RemovePeer stays: it
// is internal session cleanup used by leave/rotate/stale-sweep, not
// a peer-removal API, and the stale-sweeper's own peer_removed
// event stays with it.)

// ProbePeer implements api.Prober.
func (d *Daemon) ProbePeer(ctx context.Context, id uint64) error {
	return d.ctl.Probe(ctx, id)
}

// Probe implements api.GUI: it validates the peer, starts the probe in the
// background, and reports pending. The result arrives as a peer:probe event.
func (d *Daemon) Probe(ctx context.Context, id uint64) (api.ProbeResult, error) {
	if _, ok := d.peers.Get(id); !ok {
		return api.ProbeResult{}, &api.StatusError{
			Status:      http.StatusNotFound,
			Code:        api.ErrCodeNotFound,
			Message:     fmt.Sprintf("peer %016x is unknown", id),
			Remediation: "refresh the peer list",
		}
	}
	if d.engine() == nil {
		return api.ProbeResult{}, &api.StatusError{
			Status:      http.StatusServiceUnavailable,
			Code:        api.ErrCodeDaemonUnavailable,
			Message:     "connection is paused",
			Remediation: "connect first, then probe",
		}
	}
	go d.runProbe(id)
	return api.ProbeResult{PeerID: fmt.Sprintf("%016x", id), State: "pending"}, nil
}

func (d *Daemon) runProbe(id uint64) {
	res := api.ProbeResult{PeerID: fmt.Sprintf("%016x", id), State: "error"}
	ctx, cancel := context.WithTimeout(context.Background(), control.ProbeTimeout+time.Second)
	defer cancel()
	if err := d.ctl.Probe(ctx, id); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			res.State = "timeout"
		}
		res.Error = err.Error()
	} else {
		res.State = "up"
		if p, ok := d.peers.Get(id); ok {
			res.RTTMs = float64(p.RTT) / float64(time.Millisecond)
		}
	}
	d.publish("peer:probe", res)
}

// stalePeerTTL is how long a peer that has never connected may stay in the
// table. A legitimate node connects at least once; a stale identity from an
// earlier mesh never does.
const stalePeerTTL = 15 * time.Minute

func stalePeer(p peer.Peer, now time.Time, ttl time.Duration) bool {
	return p.State == peer.StateDown && p.LastSeen.IsZero() && !p.Added.IsZero() && now.Sub(p.Added) >= ttl
}

// pruneStalePeers removes configured peers that have never been seen, so old
// invites and regenerated keys do not linger in the peers list or the map.
func (d *Daemon) pruneStalePeers(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			removed := false
			for _, p := range d.peers.List() {
				if !stalePeer(p, now, stalePeerTTL) {
					continue
				}
				d.peers.Remove(p.ID)
				d.routes.RemoveDirect(p.ID)
				if e := d.engine(); e != nil {
					e.DropSession(p.ID)
				}
				removed = true
				d.emit(api.Event{Type: "peer_removed", PeerID: fmt.Sprintf("%016x", p.ID)})
			}
			if removed {
				// One rewrite per pass, not one per removed peer.
				d.persistPeers()
				d.emitPeerChanged()
				d.emitRouteChanged()
			}
		}
	}
}
