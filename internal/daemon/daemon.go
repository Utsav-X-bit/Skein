// Package daemon composes the link, TUN, sessions, discovery, control, and API
// into one running node.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/control"
	"github.com/Utsav-X-bit/skein/internal/dataplane"
	"github.com/Utsav-X-bit/skein/internal/discovery"
	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/firewall"
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
	eng      *dataplane.Engine
	ctl      *control.Controller

	link  *link.Switch
	tun   tun.Device
	mdns  *discovery.MDNS
	api   *api.Server
	http  *http.Server
	start time.Time

	events    chan api.Event
	closeOnce sync.Once
	closeErr  error
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
		start:    time.Now(),
		events:   make(chan api.Event, 64),
	}

	for _, p := range cfg.Peers {
		if err := d.registerPeer(p.ID, p.PublicKey, p.Addrs, p.Dialer, p.Name); err != nil {
			return nil, err
		}
	}

	udp, err := link.Listen(cfg.Node.ListenPort)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("%w (another daemon is already running; stop the service with `systemctl stop skein` or `sc stop skein`)", err)
		}
		return nil, err
	}
	dev, err := tun.Open(cfg.Node.Interface, cfg.Node.MTU)
	if err != nil {
		udp.Close()
		return nil, err
	}

	eng, err := dataplane.New(dataplane.Config{
		LocalID:   id.ID,
		Identity:  id,
		OverlayV4: cfg.Node.OverlayIPv4,
		OverlayV6: cfg.Node.OverlayIPv6,
		PrefixV4:  cfg.Node.OverlayIPv4Prefix,
		PrefixV6:  cfg.Node.OverlayIPv6Prefix,
		MTU:       cfg.Node.MTU,
	}, dataplane.Deps{
		TUN:      dev,
		Link:     udp,
		Peers:    peers,
		Routes:   routes,
		Sessions: sessions,
	})
	if err != nil {
		dev.Close()
		udp.Close()
		return nil, err
	}

	ctl, err := control.New(control.Local{
		ID:         id.ID,
		Name:       cfg.Node.Name,
		PublicKey:  id.PublicKey,
		OverlayV4:  cfg.Node.OverlayIPv4,
		OverlayV6:  cfg.Node.OverlayIPv6,
		Candidates: localCandidates(cfg),
	}, peers, routes, merger, eng)
	if err != nil {
		dev.Close()
		udp.Close()
		return nil, err
	}

	merger.OnPeer = func(peerID uint64, publicKey []byte) error {
		// Learned peers use the same rule as mDNS: the lower ID dials.
		return d.registerPeer(peerID, publicKey, nil, d.id.ID < peerID, "")
	}

	d.link = udp
	d.tun = dev
	d.eng = eng
	d.ctl = ctl
	d.api = api.NewServer(cfg.API.Token, d, d, d)

	eng.SetControlHandler(ctl.HandleControl)
	eng.SetPingHandler(ctl.HandlePing)
	eng.SetSessionHandler(d.sessionEstablished)
	if !udp.HasTCP() {
		log.Printf("skein: warning: TCP fallback unavailable on port %d; UDP only", cfg.Node.ListenPort)
	}
	// A peer that proves the network secret may join without a configured
	// entry; the invite carries the secret, so holding it is membership.
	sessions.SetAcceptUnknown(true)
	sessions.SetPeerLearnedHandler(d.peerLearned)

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
		d.ctl.SetLocalCandidates(allCandidates(ctx, d.cfg))
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
	if err := d.peers.Upsert(peer.Peer{
		ID:        id,
		PublicKey: publicKey,
		Dialer:    dialer,
		State:     peer.StateDown,
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
}

// persistPeers rewrites the configuration so learned peers survive a restart.
// The daemon owns the peer list; stop it before editing peers by hand.
func (d *Daemon) persistPeers() {
	if d.cfg.Path == "" {
		return
	}
	cfg := configWithPeers(d.cfg, d.peers.List())
	text, err := conf.Render(cfg)
	if err != nil {
		log.Printf("skein: warning: cannot persist peers: %v", err)
		return
	}
	tmp := cfg.Path + ".tmp"
	if err := os.WriteFile(tmp, text, 0o600); err != nil {
		log.Printf("skein: warning: cannot persist peers: %v", err)
		return
	}
	if err := fileperm.Restrict(tmp); err != nil {
		log.Printf("skein: warning: cannot restrict %s: %v", tmp, err)
	}
	// The daemon runs as root; keep the config owned by the user who owns it.
	if err := fileperm.PreserveOwner(tmp, cfg.Path); err != nil {
		log.Printf("skein: warning: cannot preserve config ownership: %v", err)
	}
	if err := os.Rename(tmp, cfg.Path); err != nil {
		log.Printf("skein: warning: cannot persist peers: %v", err)
		return
	}
	d.cfg = cfg
}

func (d *Daemon) sessionEstablished(peerID uint64) {
	d.emit(api.Event{Type: "peer_up", PeerID: fmt.Sprintf("%016x", peerID)})
}

func (d *Daemon) emit(ev api.Event) {
	ev.TimeMS = time.Now().UnixMilli()
	select {
	case d.events <- ev:
	default:
		// A slow consumer must never block the dataplane.
	}
}

// Run configures the interface and runs every loop until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	addresses := []netip.Prefix{netip.PrefixFrom(d.cfg.Node.OverlayIPv4, d.cfg.Node.OverlayIPv4Prefix.Bits())}
	routes := []netip.Prefix{d.cfg.Node.OverlayIPv4Prefix}
	if d.cfg.Node.OverlayIPv6.IsValid() {
		addresses = append(addresses, netip.PrefixFrom(d.cfg.Node.OverlayIPv6, d.cfg.Node.OverlayIPv6Prefix.Bits()))
		routes = append(routes, d.cfg.Node.OverlayIPv6Prefix)
	}
	if warnings, err := netcfg.Conflicts(routes); err == nil {
		for _, w := range warnings {
			log.Printf("skein: warning: %s", w)
		}
	}
	if err := netcfg.Reconcile(d.cfg.Node.Interface, routes, routes); err != nil {
		log.Printf("skein: warning: reconcile: %v", err)
	}
	if err := netcfg.Configure(d.cfg.Node.Interface, d.cfg.Node.MTU, addresses, routes); err != nil {
		return err
	}
	// Open the underlay port and trust the overlay interface so any protocol
	// works without manual rules.
	if err := firewall.Ensure(firewall.Config{Interface: d.cfg.Node.Interface, Port: d.cfg.Node.ListenPort}); err != nil {
		log.Printf("skein: warning: firewall: %v", err)
	}
	defer func() {
		if err := netcfg.Remove(d.cfg.Node.Interface, addresses, routes); err != nil {
			log.Printf("skein: warning: remove network configuration: %v", err)
		}
	}()

	for _, p := range d.peers.List() {
		addrs := make([]string, 0, len(p.Candidates))
		for _, c := range p.Candidates {
			addrs = append(addrs, c.Addr.String())
		}
		log.Printf("skein: peer %016x dialer=%v candidates=%v", p.ID, p.Dialer, addrs)
	}
	log.Printf("skein: mesh status: run \"skein dashboard\" in another terminal")

	if d.cfg.Discovery.MDNS {
		inv := identity.Invite{ID: d.id.ID, PublicKey: d.id.PublicKey, Addrs: localCandidates(d.cfg)}
		mdns, err := discovery.StartMDNS(ctx, inv, discovery.Fingerprint(d.cfg.Node.NetworkSecret), d.cfg.Node.ListenPort, d.discovered)
		if err != nil {
			log.Printf("skein: warning: mDNS unavailable: %v", err)
		} else {
			d.mdns = mdns
			defer mdns.Close()
		}
	}

	apiCtx, stopAPI := context.WithCancel(ctx)
	defer stopAPI()
	go d.serveAPI(apiCtx)
	go d.refreshWAN(ctx)

	go func() { _ = d.ctl.Run(ctx) }()
	return d.eng.Run(ctx)
}

func (d *Daemon) serveAPI(ctx context.Context) {
	d.http = &http.Server{
		Addr:              d.cfg.API.Listen,
		Handler:           d.api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ln, err := net.Listen("tcp", d.cfg.API.Listen)
	if err != nil {
		log.Printf("skein: error: API listen: %v", err)
		return
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = d.http.Shutdown(shutdownCtx)
	}()
	if err := d.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("skein: error: API server: %v", err)
	}
}

func (d *Daemon) discovered(inv identity.Invite) {
	if inv.ID == d.id.ID {
		return
	}
	dialer := d.id.ID < inv.ID
	if err := d.registerPeer(inv.ID, inv.PublicKey, inv.Addrs, dialer, ""); err != nil {
		return
	}
	for _, addr := range inv.Addrs {
		d.peers.AddCandidate(inv.ID, addr, peer.SourceMDNS)
	}
	d.emit(api.Event{Type: "peer_discovered", PeerID: fmt.Sprintf("%016x", inv.ID)})
}

// Close releases every resource. It is safe to call more than once.
func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		if d.mdns != nil {
			d.mdns.Close()
		}
		if d.eng != nil {
			_ = d.eng.Close()
		}
		if d.http != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = d.http.Shutdown(ctx)
			cancel()
		}
		if d.tun != nil {
			if err := d.tun.Close(); err != nil {
				d.closeErr = err
			}
		}
		if d.link != nil {
			if err := d.link.Close(); err != nil && d.closeErr == nil {
				d.closeErr = err
			}
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
	snap := d.eng.Counters()
	st := api.Status{
		ID:            fmt.Sprintf("%016x", d.id.ID),
		OverlayIPv4:   d.cfg.Node.OverlayIPv4.String(),
		UptimeSeconds: int64(time.Since(d.start).Seconds()),
		Version:       version.Version,
		ListenPort:    d.cfg.Node.ListenPort,
		PeerCount:     d.peers.Len(),
		RxBytes:       snap.RxBytes,
		TxBytes:       snap.TxBytes,
	}
	if d.cfg.Node.OverlayIPv6.IsValid() {
		st.OverlayIPv6 = d.cfg.Node.OverlayIPv6.String()
	}
	return st
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
		if !p.LastSeen.IsZero() {
			row.LastSeenMS = now.Sub(p.LastSeen).Milliseconds()
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
		out = append(out, api.Route{
			Destination: fmt.Sprintf("%016x", r.Destination),
			NextHop:     fmt.Sprintf("%016x", r.NextHop),
			CostMs:      float64(r.Cost) / float64(time.Millisecond),
			Hops:        r.Hops,
		})
	}
	return out
}

// Config implements api.StateReader with secrets redacted.
func (d *Daemon) Config() api.ConfigView {
	view := api.ConfigView{
		OverlayIPv4Prefix: d.cfg.Node.OverlayIPv4Prefix.String(),
		OverlayIPv6Prefix: d.cfg.Node.OverlayIPv6Prefix.String(),
		OverlayIPv4:       d.cfg.Node.OverlayIPv4.String(),
		ListenPort:        d.cfg.Node.ListenPort,
		MTU:               d.cfg.Node.MTU,
		Interface:         d.cfg.Node.Interface,
		APIListen:         d.cfg.API.Listen,
		MDNS:              d.cfg.Discovery.MDNS,
		NetworkSecret:     "redacted",
		APIToken:          "redacted",
	}
	if d.cfg.Node.OverlayIPv6.IsValid() {
		view.OverlayIPv6 = d.cfg.Node.OverlayIPv6.String()
	}
	for _, p := range d.cfg.Peers {
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
	return nil
}

// SetName implements api.PeerWriter.
func (d *Daemon) SetName(name string) error {
	name = strings.TrimSpace(name)
	if err := conf.ValidateName(name); err != nil {
		return err
	}
	d.cfg.Node.Name = name
	d.ctl.SetLocalName(name)
	d.persistPeers()
	d.emit(api.Event{Type: "name_changed"})
	return nil
}

// RemovePeer implements api.PeerWriter.
func (d *Daemon) RemovePeer(id uint64) error {
	if _, ok := d.peers.Get(id); !ok {
		return fmt.Errorf("daemon: peer %016x is unknown", id)
	}
	d.peers.Remove(id)
	d.sessions.RemovePeer(id)
	d.routes.RemoveDirect(id)
	d.emit(api.Event{Type: "peer_removed", PeerID: fmt.Sprintf("%016x", id)})
	d.persistPeers()
	return nil
}

// ProbePeer implements api.Prober.
func (d *Daemon) ProbePeer(ctx context.Context, id uint64) error {
	return d.ctl.Probe(ctx, id)
}
